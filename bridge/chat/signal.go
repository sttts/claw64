package chat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	signalUserPrefix  = "user:"
	signalGroupPrefix = "group:"
)

// SignalChannel implements Channel using signal-cli subprocesses.
type SignalChannel struct {
	account string
	config  string
	target  string

	mu sync.Mutex
}

// NewSignal creates a Signal backend bound to one signal-cli account.
func NewSignal(account, config, target string) *SignalChannel {
	return &SignalChannel{account: account, config: config, target: target}
}

func (s *SignalChannel) Name() string { return "signal" }

// Start polls signal-cli receive and dispatches incoming messages.
func (s *SignalChannel) Start(ctx context.Context, handler MessageHandler) error {
	log.Printf("signal: ready on %s target=%s trigger=%s", s.account, s.target, s.triggerLogValue())

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		events, err := s.receiveBatch(ctx)
		if err != nil {
			return err
		}
		for _, evt := range events {
			if evt.userID != s.target {
				continue
			}

			text, ok := s.incomingText(evt.text)
			if !ok {
				continue
			}

			err := handler(ctx, evt.userID, text)
			if err != nil {
				log.Printf("signal: handler error for %s: %v", evt.userID, err)
				if sendErr := s.Send(ctx, evt.userID, fmt.Sprintf("error: %v", err)); sendErr != nil {
					log.Printf("signal: send reply to %s: %v", evt.userID, sendErr)
				}
			}
		}
	}
}

func (s *SignalChannel) incomingText(text string) (string, bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", false
	}
	if strings.HasPrefix(s.target, signalGroupPrefix) {
		return stripJoystickTrigger(text)
	}
	return text, true
}

func (s *SignalChannel) triggerLogValue() string {
	if strings.HasPrefix(s.target, signalGroupPrefix) {
		return fmt.Sprintf("%q", joystickTrigger)
	}
	return "none"
}

// Send sends a text reply to a Signal user or group.
func (s *SignalChannel) Send(ctx context.Context, user, text string) error {
	args := s.baseArgs()
	args = append(args, "send", "--message-from-stdin")
	args = appendSignalRecipient(args, user)

	cmd := exec.CommandContext(ctx, "signal-cli", args...)
	cmd.Stdin = strings.NewReader(formatSignalMessage(text))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("signal send: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func formatSignalMessage(text string) string {
	return strings.TrimSpace(text)
}

func (s *SignalChannel) Typing(ctx context.Context, user string, active bool) error {
	args := s.signalTypingArgs(user, active)

	out, err := exec.CommandContext(ctx, "signal-cli", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("signal typing: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (s *SignalChannel) signalTypingArgs(user string, active bool) []string {
	args := s.baseArgs()
	args = append(args, "sendTyping")
	if !active {
		args = append(args, "--stop")
	}
	return appendSignalRecipient(args, user)
}

func appendSignalRecipient(args []string, user string) []string {
	switch {
	case strings.HasPrefix(user, signalGroupPrefix):
		return append(args, "--group-id", strings.TrimPrefix(user, signalGroupPrefix))
	case strings.HasPrefix(user, signalUserPrefix):
		return append(args, strings.TrimPrefix(user, signalUserPrefix))
	default:
		return append(args, user)
	}
}

func (s *SignalChannel) Stop() error { return nil }

type signalEvent struct {
	userID string
	text   string
}

type signalEnvelope struct {
	Envelope struct {
		Source       string `json:"source"`
		SourceNumber string `json:"sourceNumber"`
		DataMessage  *struct {
			Message   string `json:"message"`
			GroupInfo *struct {
				GroupID string `json:"groupId"`
				Type    string `json:"type"`
			} `json:"groupInfo"`
		} `json:"dataMessage"`
		SyncMessage interface{} `json:"syncMessage"`
	} `json:"envelope"`
}

func (s *SignalChannel) receiveBatch(ctx context.Context) ([]signalEvent, error) {
	args := s.baseArgs()
	args = append(args,
		"receive",
		"--timeout", "5",
		"--max-messages", "100",
		"--ignore-attachments",
		"--ignore-stories",
	)

	cmd := exec.CommandContext(ctx, "signal-cli", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		text := strings.TrimSpace(string(out))
		if text == "" {
			text = err.Error()
		}
		return nil, fmt.Errorf("signal receive: %s", text)
	}

	var events []signalEvent
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var env signalEnvelope
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			log.Printf("signal: ignoring unparsable line: %s", line)
			continue
		}
		if env.Envelope.SyncMessage != nil {
			continue
		}
		if env.Envelope.DataMessage == nil {
			continue
		}

		text := strings.TrimSpace(env.Envelope.DataMessage.Message)
		if text == "" {
			continue
		}

		userID := signalUserPrefix + firstNonEmpty(env.Envelope.SourceNumber, env.Envelope.Source)
		if group := env.Envelope.DataMessage.GroupInfo; group != nil && group.GroupID != "" {
			userID = signalGroupPrefix + group.GroupID
		}
		if userID == signalUserPrefix {
			continue
		}

		events = append(events, signalEvent{userID: userID, text: text})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("signal scan: %w", err)
	}

	// Avoid a busy loop when no messages arrived.
	if len(events) == 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return events, nil
}

func (s *SignalChannel) baseArgs() []string {
	args := []string{}
	if s.config != "" {
		args = append(args, "--config", s.config)
	}
	args = append(args, "--output", "json")
	if s.account != "" {
		args = append(args, "--account", s.account)
	}
	return args
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

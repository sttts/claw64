package chat

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os/exec"
	"strings"
	"sync"
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

// Start streams signal-cli receive and dispatches incoming messages.
func (s *SignalChannel) Start(ctx context.Context, handler MessageHandler) error {
	log.Printf("signal: ready on %s target=%s trigger=%s", s.account, s.target, s.triggerLogValue())

	events := make(chan signalEvent, 32)
	errs := make(chan error, 1)
	go func() {
		errs <- s.receiveStream(ctx, events)
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errs:
			if err != nil {
				return err
			}
			return nil
		case evt := <-events:
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

func (s *SignalChannel) receiveStream(ctx context.Context, events chan<- signalEvent) error {
	args := s.baseArgs()
	args = append(args,
		"receive",
		"--timeout", "-1",
		"--ignore-attachments",
		"--ignore-stories",
	)

	cmd := exec.CommandContext(ctx, "signal-cli", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("signal receive stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("signal receive stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("signal receive start: %w", err)
	}

	stderrDone := make(chan string, 1)
	go func() {
		data, _ := io.ReadAll(stderr)
		stderrDone <- strings.TrimSpace(string(data))
	}()

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		evt, ok := parseSignalLine(scanner.Text())
		if !ok {
			continue
		}
		select {
		case <-ctx.Done():
			_ = cmd.Wait()
			return ctx.Err()
		case events <- evt:
		}
	}
	if err := scanner.Err(); err != nil {
		_ = cmd.Wait()
		stderrText := <-stderrDone
		if stderrText != "" {
			return fmt.Errorf("signal scan: %w: %s", err, stderrText)
		}
		return fmt.Errorf("signal scan: %w", err)
	}

	err = cmd.Wait()
	stderrText := <-stderrDone
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		if stderrText == "" {
			stderrText = err.Error()
		}
		return fmt.Errorf("signal receive: %s", stderrText)
	}
	return fmt.Errorf("signal receive stopped")
}

func parseSignalLine(raw string) (signalEvent, bool) {
	line := strings.TrimSpace(raw)
	if line == "" {
		return signalEvent{}, false
	}

	var env signalEnvelope
	if err := json.Unmarshal([]byte(line), &env); err != nil {
		log.Printf("signal: ignoring unparsable line: %s", line)
		return signalEvent{}, false
	}
	if env.Envelope.SyncMessage != nil {
		return signalEvent{}, false
	}
	if env.Envelope.DataMessage == nil {
		return signalEvent{}, false
	}

	text := strings.TrimSpace(env.Envelope.DataMessage.Message)
	if text == "" {
		return signalEvent{}, false
	}

	userID := signalUserPrefix + firstNonEmpty(env.Envelope.SourceNumber, env.Envelope.Source)
	if group := env.Envelope.DataMessage.GroupInfo; group != nil && group.GroupID != "" {
		userID = signalGroupPrefix + group.GroupID
	}
	if userID == signalUserPrefix {
		return signalEvent{}, false
	}

	return signalEvent{userID: userID, text: text}, true
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

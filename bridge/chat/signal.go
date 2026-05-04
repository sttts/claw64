package chat

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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

	mu  sync.Mutex
	rpc *signalRPC
}

// NewSignal creates a Signal backend bound to one signal-cli account.
func NewSignal(account, config, target string) *SignalChannel {
	return &SignalChannel{account: account, config: config, target: target}
}

func (s *SignalChannel) Name() string { return "signal" }

// Start runs signal-cli JSON-RPC and dispatches incoming messages.
func (s *SignalChannel) Start(ctx context.Context, handler MessageHandler) error {
	log.Printf("signal: ready on %s target=%s trigger=%s", s.account, s.target, s.triggerLogValue())

	events := make(chan signalEvent, 32)
	errs := make(chan error, 1)
	go func() {
		errs <- s.runJSONRPC(ctx, events)
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
	if rpc := s.currentRPC(); rpc != nil {
		return rpc.call(ctx, "send", signalMessageParams(user, text))
	}

	args := s.baseArgs()
	args = append(args, "send", "--message-from-stdin")
	args = appendSignalRecipient(args, user)

	cmd := signalCommand(ctx, args...)
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
	if rpc := s.currentRPC(); rpc != nil {
		return rpc.call(ctx, "sendTyping", signalTypingParams(user, active))
	}

	args := s.signalTypingArgs(user, active)

	out, err := signalCommand(ctx, args...).CombinedOutput()
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

type signalRPC struct {
	stdin io.WriteCloser

	mu      sync.Mutex
	nextID  uint64
	pending map[string]chan signalRPCResponse
}

type signalRPCResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *signalRPCError `json:"error"`
}

type signalRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

type signalEnvelope struct {
	Envelope signalEnvelopeEnvelope `json:"envelope"`
}

type signalJSONRPCLine struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *signalRPCError `json:"error"`
}

type signalReceiveParams struct {
	Envelope *signalEnvelopeEnvelope `json:"envelope"`
	Result   *struct {
		Envelope *signalEnvelopeEnvelope `json:"envelope"`
	} `json:"result"`
}

type signalEnvelopeEnvelope struct {
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
}

func (s *SignalChannel) runJSONRPC(ctx context.Context, events chan<- signalEvent) error {
	args := s.baseArgs()
	args = append(args,
		"jsonRpc",
		"--ignore-attachments",
		"--ignore-stories",
	)

	cmd := signalCommand(ctx, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("signal jsonRpc stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("signal jsonRpc stderr: %w", err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("signal jsonRpc stdin: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("signal jsonRpc start: %w", err)
	}

	rpc := &signalRPC{
		stdin:   stdin,
		pending: map[string]chan signalRPCResponse{},
	}
	s.setRPC(rpc)
	defer s.setRPC(nil)
	defer rpc.closePending("signal jsonRpc stopped")

	stderrDone := make(chan string, 1)
	go func() {
		data, _ := io.ReadAll(stderr)
		stderrDone <- strings.TrimSpace(string(data))
	}()

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if rpc.dispatchResponse(line) {
			continue
		}

		evt, ok := parseSignalLine(line)
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
			return fmt.Errorf("signal jsonRpc scan: %w: %s", err, stderrText)
		}
		return fmt.Errorf("signal jsonRpc scan: %w", err)
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
		return fmt.Errorf("signal jsonRpc: %s", stderrText)
	}
	return fmt.Errorf("signal jsonRpc stopped")
}

func parseSignalLine(raw string) (signalEvent, bool) {
	line := strings.TrimSpace(raw)
	if line == "" {
		return signalEvent{}, false
	}

	if evt, ok := parseSignalJSONRPCNotification(line); ok {
		return evt, true
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

func parseSignalJSONRPCNotification(line string) (signalEvent, bool) {
	var msg signalJSONRPCLine
	if err := json.Unmarshal([]byte(line), &msg); err != nil {
		return signalEvent{}, false
	}
	if msg.Method != "receive" || len(msg.Params) == 0 {
		return signalEvent{}, false
	}

	var params signalReceiveParams
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		log.Printf("signal: ignoring unparsable receive notification: %s", line)
		return signalEvent{}, false
	}

	env := params.Envelope
	if env == nil && params.Result != nil {
		env = params.Result.Envelope
	}
	if env == nil || env.SyncMessage != nil || env.DataMessage == nil {
		return signalEvent{}, false
	}

	text := strings.TrimSpace(env.DataMessage.Message)
	if text == "" {
		return signalEvent{}, false
	}

	userID := signalUserPrefix + firstNonEmpty(env.SourceNumber, env.Source)
	if group := env.DataMessage.GroupInfo; group != nil && group.GroupID != "" {
		userID = signalGroupPrefix + group.GroupID
	}
	if userID == signalUserPrefix {
		return signalEvent{}, false
	}

	return signalEvent{userID: userID, text: text}, true
}

func (s *SignalChannel) setRPC(rpc *signalRPC) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rpc = rpc
}

func (s *SignalChannel) currentRPC() *signalRPC {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rpc
}

func (r *signalRPC) call(ctx context.Context, method string, params map[string]interface{}) error {
	id := strconv.FormatUint(atomic.AddUint64(&r.nextID, 1), 10)
	ch := make(chan signalRPCResponse, 1)

	r.mu.Lock()
	r.pending[id] = ch
	req := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}
	line, err := json.Marshal(req)
	if err == nil {
		_, err = fmt.Fprintf(r.stdin, "%s\n", line)
	}
	if err != nil {
		delete(r.pending, id)
		r.mu.Unlock()
		return fmt.Errorf("signal jsonRpc %s: %w", method, err)
	}
	r.mu.Unlock()

	select {
	case <-ctx.Done():
		r.removePending(id)
		return ctx.Err()
	case resp := <-ch:
		if resp.Error != nil {
			return fmt.Errorf("signal jsonRpc %s: %s", method, resp.Error.Message)
		}
		return nil
	}
}

func (r *signalRPC) dispatchResponse(raw string) bool {
	var msg signalJSONRPCLine
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &msg); err != nil {
		return false
	}
	if len(msg.ID) == 0 {
		return false
	}

	id, ok := decodeJSONRPCID(msg.ID)
	if !ok {
		return false
	}

	r.mu.Lock()
	ch := r.pending[id]
	delete(r.pending, id)
	r.mu.Unlock()
	if ch == nil {
		return true
	}

	ch <- signalRPCResponse{Result: msg.Result, Error: msg.Error}
	return true
}

func (r *signalRPC) removePending(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.pending, id)
}

func (r *signalRPC) closePending(message string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, ch := range r.pending {
		delete(r.pending, id)
		ch <- signalRPCResponse{Error: &signalRPCError{Message: message}}
	}
}

func decodeJSONRPCID(raw json.RawMessage) (string, bool) {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, true
	}

	var n uint64
	if err := json.Unmarshal(raw, &n); err == nil {
		return strconv.FormatUint(n, 10), true
	}
	return "", false
}

func signalMessageParams(user, text string) map[string]interface{} {
	params := map[string]interface{}{
		"message": formatSignalMessage(text),
	}
	addSignalRPCRecipient(params, user)
	return params
}

func signalTypingParams(user string, active bool) map[string]interface{} {
	params := map[string]interface{}{}
	if !active {
		params["stop"] = true
	}
	addSignalRPCRecipient(params, user)
	return params
}

func addSignalRPCRecipient(params map[string]interface{}, user string) {
	switch {
	case strings.HasPrefix(user, signalGroupPrefix):
		params["groupId"] = strings.TrimPrefix(user, signalGroupPrefix)
	case strings.HasPrefix(user, signalUserPrefix):
		params["recipient"] = []string{strings.TrimPrefix(user, signalUserPrefix)}
	default:
		params["recipient"] = []string{user}
	}
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

func signalCommand(ctx context.Context, args ...string) *exec.Cmd {
	path, err := exec.LookPath("signal-cli")
	if err != nil {
		path = "signal-cli"
	}
	return exec.CommandContext(ctx, path, args...)
}

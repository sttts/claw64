// Package relay routes messages between chat, LLM, and the C64 serial link.
// It is not an agent — the C64 is the agent. The relay just forwards.
package relay

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/sttts/claw64/bridge/llm"
	"github.com/sttts/claw64/bridge/serial"
	"github.com/sttts/claw64/bridge/termstyle"
)

// maxIterations caps the LLM loop to prevent infinite cycles.
// No iteration limit — the event loop runs until TEXT is returned or an error occurs.

// Relay routes messages between chat, LLM, and the C64 serial link.
// The C64 drives the agent loop; the relay just forwards.
type Relay struct {
	Link           *serial.Link
	LLM            llm.Completer
	History        *History
	DebugDir       string
	MonitorAddr    string
	SymbolPath     string
	SystemPrompt   string // received from C64
	promptChunks   map[int]string
	resultChunks   map[int]string
	textBuf        []byte // accumulates multi-frame TEXT chunks (receive)
	textOutQueue   []byte // pending TEXT data to send in chunks (send)
	textInFlight   []byte // current TEXT chunk sent, waiting for forwarded ack
	toolInFlight   []byte // current tool payload sent, waiting for RESULT/ERROR
	waitingTool    bool
	lastToolCallID string
	lastToolName   string
	toolStartedAt  time.Time
	basicRunning   bool
	pendingFrames  []serial.Frame
}

const textChunkMax = 62
const textAckTimeout = 8 * time.Second
const textAckWhileRunningTimeout = 2 * time.Minute
const toolAckTimeout = 12 * time.Second
const toolAckWhileRunningTimeout = 2 * time.Minute
const execAckTimeout = 2 * time.Minute
const c64FrameTimeout = 8 * time.Second

var llmTools = []llm.Tool{
	llm.BasicExecTool,
	llm.TextScreenshotTool,
	llm.BasicStopTool,
	llm.BasicStatusTool,
}

// handleSystemFrame assembles SYSTEM prompt chunks from the C64.
func (r *Relay) handleSystemFrame(f serial.Frame) {
	if len(f.Payload) < 2 {
		return
	}
	idx := int(f.Payload[0])
	total := int(f.Payload[1])
	text := string(f.Payload[2:])

	if r.promptChunks == nil {
		r.promptChunks = make(map[int]string)
	}
	r.promptChunks[idx] = text
	log.Printf("     ← C64 soul [%d/%d] %d bytes", idx+1, total, len(text))

	if len(r.promptChunks) == total {
		var prompt string
		for i := 0; i < total; i++ {
			prompt += r.promptChunks[i]
		}
		r.SystemPrompt = prompt
		r.promptChunks = nil
		log.Printf("     C64 soul received (%d bytes)", len(prompt))
	}
}

// handleResultFrame assembles chunked RESULT output from the C64.
func (r *Relay) handleResultFrame(f serial.Frame) (string, bool) {
	if len(f.Payload) < 2 {
		return "", false
	}
	idx := int(f.Payload[0])
	total := int(f.Payload[1])
	text := string(f.Payload[2:])
	if r.lastToolName == "screen" {
		log.Printf("C64 → bridge: screen RESULT chunk %d/%d payload=%s text=%q", idx+1, total, hex.EncodeToString(f.Payload), text)
	}

	if r.resultChunks == nil {
		r.resultChunks = make(map[int]string)
	}
	r.resultChunks[idx] = text

	if len(r.resultChunks) != total {
		return "", false
	}

	var result string
	for i := 0; i < total; i++ {
		result += r.resultChunks[i]
	}
	r.resultChunks = nil
	return result, true
}

// logStream prints a log-style prefix without a trailing newline.
// Characters can be appended to the same line afterwards.
func logStream(format string, args ...any) {
	ts := time.Now().Format("2006/01/02 15:04:05")
	msg := fmt.Sprintf(format, args...)
	fmt.Fprint(os.Stderr, termstyle.Dim(fmt.Sprintf("%s %s", ts, msg)))
}

// SetupProgress installs a send-progress callback on the serial link
// that prints payload bytes char-by-char as they are sent.
func (r *Relay) SetupProgress() {
	r.Link.OnSendByte = func(typeName string, payload []byte, idx int) {
		if idx == -1 {
			fmt.Fprint(os.Stderr, termstyle.Dim("\n"))
			return
		}
		if payload[idx] == '\n' {
			fmt.Fprint(os.Stderr, termstyle.Dim(`\n`))
		} else {
			fmt.Fprint(os.Stderr, termstyle.Dim(string(payload[idx])))
		}
	}

	// receive callback: print header on first byte, then stream chars
	r.Link.OnRecvByte = func(frameType byte, idx int, b byte) {
		if idx == 0 {
			name := serial.TypeName(frameType)
			switch frameType {
			case serial.FrameLLM:
				logStream("C64 → LLM:   ")
			case serial.FrameResult:
				logStream("C64 → LLM:   RESULT ")
			case serial.FrameStatus:
				logStream("C64 → LLM:   STATUS ")
			case serial.FrameText:
				logStream("C64 → USER:  ")
			case serial.FrameSystem:
				logStream("C64 → soul:  ")
			default:
				logStream("C64 → ???:   %s ", name)
			}
		}

		// SYSTEM and RESULT frames start with [chunk_index, total_chunks], not text.
		if (frameType == serial.FrameSystem || frameType == serial.FrameResult) && idx < 2 {
			return
		}
		if b == '\n' {
			fmt.Fprint(os.Stderr, termstyle.Dim(`\n`))
		} else {
			fmt.Fprint(os.Stderr, termstyle.Dim(string(b)))
		}
	}
}

// basicExecArgs is the JSON structure the LLM passes to exec.
type basicExecArgs struct {
	Command string `json:"command"`
}

// HandleMessage relays a user message through LLM and C64.
func (r *Relay) HandleMessage(ctx context.Context, userID string, text string) (string, error) {
	text = serial.ToASCII(text)
	r.History.Append(userID, llm.Message{Role: "user", Content: text})

	// send user message to C64 (header now, chars stream via callback)
	logStream("USER → C64:  MSG ")
	msgFrame := serial.Frame{Type: serial.FrameMsg, Payload: []byte(text)}
	if err := r.sendVerified(ctx, msgFrame, "MSG"); err != nil {
		return "", fmt.Errorf("send MSG: %w", err)
	}

	return r.eventLoop(ctx, userID)
}

// eventLoop waits for C64 frames and reacts.
func (r *Relay) eventLoop(ctx context.Context, userID string) (string, error) {
	for {
		f, err := r.recvFromC64(ctx, len(r.textOutQueue) > 0)
		if err != nil {
			return "", err
		}

		switch f.Type {
		case serial.FrameLLM:
			fmt.Fprintln(os.Stderr) // newline after streamed payload
			if err := r.callAndDispatch(ctx, userID); err != nil {
				return "", err
			}
			if len(r.textBuf) > 0 && len(r.textOutQueue) == 0 {
				text := string(r.textBuf)
				r.textBuf = nil
				return text, nil
			}

		case serial.FrameResult:
			fmt.Fprintln(os.Stderr) // newline after streamed payload
			r.toolInFlight = nil
			r.waitingTool = false
			r.basicRunning = false
			resultText, complete := r.handleResultFrame(f)
			if !complete {
				continue
			}

			resultPrefix := "[C64 screen output]: "
			if r.lastToolName == "screen" {
				resultPrefix = "[C64 text screen screenshot]: "
			}
			result := resultPrefix + resultText
			if resultText == "" {
				result = resultPrefix + "(empty)"
			}
			r.appendToolResult(userID, result)
			if err := r.callAndDispatch(ctx, userID); err != nil {
				return "", err
			}
			if len(r.textBuf) > 0 && len(r.textOutQueue) == 0 {
				text := string(r.textBuf)
				r.textBuf = nil
				return text, nil
			}

		case serial.FrameStatus:
			fmt.Fprintln(os.Stderr)
			r.toolInFlight = nil
			r.waitingTool = false
			status := string(f.Payload)
			if status == "" {
				status = "UNKNOWN"
			}
			r.basicRunning = status == "RUNNING" || status == "STOP REQUESTED"
			log.Printf("C64 → LLM:   STATUS %s", status)
			r.appendToolResult(userID, "[C64 BASIC status]: "+status)
			if err := r.callAndDispatch(ctx, userID); err != nil {
				return "", err
			}
			if len(r.textBuf) > 0 && len(r.textOutQueue) == 0 {
				text := string(r.textBuf)
				r.textBuf = nil
				return text, nil
			}

		case serial.FrameError:
			log.Printf("C64 → LLM:   ERROR (timeout)")
			r.toolInFlight = nil
			r.waitingTool = false
			r.basicRunning = false
			r.appendToolResult(userID, "ERROR: command timed out on C64")
			if err := r.callAndDispatch(ctx, userID); err != nil {
				return "", err
			}
			if len(r.textBuf) > 0 && len(r.textOutQueue) == 0 {
				text := string(r.textBuf)
				r.textBuf = nil
				return text, nil
			}

		case serial.FrameText:
			// TEXT forwarded by C64 for the user — accumulate until the
			// C64 finishes this burst, then return it to the chat frontend.
			fmt.Fprintln(os.Stderr)
			r.textBuf = append(r.textBuf, f.Payload...)
			text := string(r.textBuf)
			r.textBuf = nil
			return text, nil

		case serial.FrameSystem:
			fmt.Fprintln(os.Stderr) // newline after streamed payload
			r.handleSystemFrame(f)
			continue

		case serial.FrameAck:
			fmt.Fprintln(os.Stderr)
			log.Printf("C64 → bridge: unexpected ACK frame")
			continue

		case serial.FrameHeartbeat:
			continue

		default:
			log.Printf("C64 → ???:   unknown frame 0x%02X", f.Type)
		}
	}
}

// callAndDispatch calls the LLM and dispatches the response to the C64.
func (r *Relay) callAndDispatch(ctx context.Context, userID string) error {
	history := r.History.Get(userID)
	msgs := make([]llm.Message, 0, 1+len(history))
	if r.SystemPrompt == "" {
		return fmt.Errorf("no soul — C64 has not sent system prompt")
	}
	msgs = append(msgs, llm.Message{Role: "system", Content: r.SystemPrompt})
	msgs = append(msgs, history...)

	r.logLLMRequest(msgs, llmTools)
	resp, err := r.LLM.Complete(ctx, msgs, llmTools)
	if err != nil {
		return fmt.Errorf("llm: %w", err)
	}
	r.History.Append(userID, resp)

	// Text responses still flow through the C64. The bridge only translates
	// protocols; the C64 remains the user-facing agent.
	if len(resp.ToolCalls) == 0 {
		if resp.Content == "" {
			logStream("LLM → C64:  TEXT ")
			fmt.Fprintln(os.Stderr, "(empty)")
			return nil
		}

		r.textOutQueue = []byte(serial.ToC64Text(resp.Content))
		return r.sendNextTextChunk(ctx)
	}

	// Tools are sequential here. The C64 is stateful, so each tool result
	// must feed the next model decision before another tool is dispatched.
	tc := resp.ToolCalls[0]
	if len(resp.ToolCalls) > 1 {
		log.Printf("LLM → bridge: extra tool calls ignored in this turn (%d total)", len(resp.ToolCalls))
	}
	r.lastToolCallID = tc.ID
	r.lastToolName = tc.Function.Name

	switch tc.Function.Name {
	case "exec":
		var args basicExecArgs
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			log.Printf("LLM → ???:   bad args: %v", err)
			r.History.Append(userID, llm.Message{
				Role: "tool", Content: fmt.Sprintf("ERROR: %v", err), ToolCallID: tc.ID,
			})
			return nil
		}
		// Sanitize: strip newlines, take first line only, truncate to the
		// maximum EXEC payload the C64 frame receiver can hold.
		cmd := serial.ToASCII(args.Command)
		if i := strings.IndexAny(cmd, "\n\r"); i >= 0 {
			cmd = cmd[:i]
		}
		if len(cmd) > 127 {
			cmd = cmd[:127]
		}
		logStream("LLM → C64:  EXEC ")
		if err := r.sendExecVerified(ctx, []byte(cmd)); err != nil {
			return fmt.Errorf("send EXEC: %w", err)
		}

	case "screen":
			logStream("LLM → C64:  SCREENSHOT ")
			fmt.Fprintln(os.Stderr)
			screenFrame := serial.Frame{Type: serial.FrameScreenshot}
			if err := r.sendVerifiedOrSemantic(ctx, screenFrame, "SCREENSHOT"); err != nil {
				return fmt.Errorf("send SCREENSHOT: %w", err)
			}
			r.toolInFlight = r.toolInFlight[:0]
			r.startToolWait()

	case "stop":
			logStream("LLM → C64:  STOP ")
			fmt.Fprintln(os.Stderr)
			stopFrame := serial.Frame{Type: serial.FrameStop}
			if err := r.sendVerifiedOrSemantic(ctx, stopFrame, "STOP"); err != nil {
				return fmt.Errorf("send STOP: %w", err)
			}
			r.toolInFlight = r.toolInFlight[:0]
			r.startToolWait()

	case "status":
			logStream("LLM → C64:  STATUS ")
			fmt.Fprintln(os.Stderr)
			statusFrame := serial.Frame{Type: serial.FrameStatusReq}
			if err := r.sendVerifiedOrSemantic(ctx, statusFrame, "STATUS"); err != nil {
				return fmt.Errorf("send STATUS: %w", err)
			}
			r.toolInFlight = r.toolInFlight[:0]
			r.startToolWait()

	default:
		log.Printf("LLM → ???:   unknown tool %q", tc.Function.Name)
		r.History.Append(userID, llm.Message{
			Role: "tool", Content: "ERROR: unknown tool", ToolCallID: tc.ID,
		})
	}
	return nil
}

func (r *Relay) logLLMRequest(messages []llm.Message, tools []llm.Tool) {
	if d, ok := r.LLM.(llm.RequestDescriber); ok {
		url, body, err := d.DescribeRequest(messages, tools)
		if err != nil {
			log.Printf("     → LLM:  request preview failed: %v", err)
			return
		}
		log.Printf("     → LLM:  %s", url)
		log.Printf("     → LLM:  %s", string(body))
		return
	}
	log.Printf("     → LLM:  request")
}

func (r *Relay) sendNextTextChunk(ctx context.Context) error {
	for len(r.textOutQueue) > 0 {
		chunk := r.textOutQueue
		if len(chunk) > textChunkMax {
			chunk = chunk[:textChunkMax]
		}
		r.textOutQueue = r.textOutQueue[len(chunk):]
		r.textInFlight = append(r.textInFlight[:0], chunk...)
		logStream("LLM → C64:  TEXT ")
		if err := r.sendTextChunk(ctx, chunk); err != nil {
			fmt.Fprintln(os.Stderr)
			return fmt.Errorf("send TEXT: %w", err)
		}
		r.textInFlight = r.textInFlight[:0]
	}
	return nil
}

func (r *Relay) sendTextChunk(ctx context.Context, chunk []byte) error {
	frame := serial.Frame{Type: serial.FrameText, Payload: chunk}
	expected := ackFingerprint(frame)
	attempts := 3
	timeout := textAckTimeout
	if r.basicRunning {
		attempts = 1
		timeout = textAckWhileRunningTimeout
	}

	for attempt := 1; attempt <= attempts; attempt++ {
		if err := r.sendWithRetry(frame); err != nil {
			return err
		}

		waitCtx, cancel := context.WithTimeout(ctx, timeout)
		ok, err := r.waitForTextDelivery(waitCtx, expected)
		cancel()
		if err == nil && ok {
			return nil
		}
		if err != nil {
			log.Printf("     ! TEXT ack attempt %d failed: %v", attempt, err)
			continue
		}
		log.Printf("     ! TEXT ack attempt %d failed: no confirmation", attempt)
	}
	return fmt.Errorf("TEXT delivery could not be verified after %d attempt(s)", attempts)
}

func (r *Relay) waitForTextDelivery(ctx context.Context, expected []byte) (bool, error) {
	for {
		f, err := r.recvFromC64(ctx, false)
		if err != nil {
			return false, err
		}
		switch f.Type {
		case serial.FrameAck:
			fmt.Fprintln(os.Stderr)
			return string(f.Payload) == string(expected), nil
		case serial.FrameText:
			fmt.Fprintln(os.Stderr)
			r.textBuf = append(r.textBuf, f.Payload...)
			continue
		case serial.FrameSystem:
			fmt.Fprintln(os.Stderr)
			r.handleSystemFrame(f)
		case serial.FrameHeartbeat:
			continue
		default:
			return false, fmt.Errorf("unexpected frame while waiting for TEXT delivery: %s", serial.TypeName(f.Type))
		}
	}
}

func (r *Relay) startToolWait() {
	r.waitingTool = true
	r.toolStartedAt = time.Now()
}

func (r *Relay) sendExecVerified(ctx context.Context, cmd []byte) error {
	execFrame := serial.Frame{Type: serial.FrameExec, Payload: cmd}
	if err := r.sendVerified(ctx, execFrame, "EXEC"); err != nil {
		return err
	}

	goFrame := serial.Frame{Type: serial.FrameExecGo}
	if err := r.sendWithRetry(goFrame); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr)

	r.toolInFlight = append(r.toolInFlight[:0], cmd...)
	r.startToolWait()
	return nil
}

func (r *Relay) sendVerified(ctx context.Context, frame serial.Frame, name string) error {
	expected := ackFingerprint(frame)
	for attempt := 1; attempt <= 3; attempt++ {
		if err := r.sendWithRetry(frame); err != nil {
			return err
		}

		ack, err := r.waitForAck(ctx)
		if err != nil {
			log.Printf("     ! %s ack attempt %d failed: %v", name, attempt, err)
			continue
		}
		if string(ack.Payload) != string(expected) {
			log.Printf("     ! %s ack attempt %d mismatch: got %v want %v", name, attempt, ack.Payload, expected)
			continue
		}
		return nil
	}
	return fmt.Errorf("%s delivery could not be verified after 3 attempts", name)
}

func (r *Relay) sendVerifiedOrSemantic(ctx context.Context, frame serial.Frame, name string) error {
	expected := ackFingerprint(frame)
	attempts := 3
	timeout := r.currentToolAckTimeout(name)
	if r.basicRunning {
		attempts = 1
		timeout = toolAckWhileRunningTimeout
	}

	for attempt := 1; attempt <= attempts; attempt++ {
		if err := r.sendWithRetry(frame); err != nil {
			return err
		}

		ok, err := r.waitForAckOrSemantic(ctx, expected, timeout)
		if err != nil {
			log.Printf("     ! %s ack attempt %d failed: %v", name, attempt, err)
			continue
		}
		if ok {
			return nil
		}
		log.Printf("     ! %s ack attempt %d failed: no confirmation", name, attempt)
	}
	return fmt.Errorf("%s delivery could not be verified after %d attempt(s)", name, attempts)
}

func ackFingerprint(frame serial.Frame) []byte {
	var chk byte = frame.Type & 0x7F
	length := byte(len(frame.Payload) & 0x7F)
	chk ^= length
	for _, b := range frame.Payload {
		m := b & 0x7F
		chk ^= m
	}
	return []byte{frame.Type & 0x7F, length, chk}
}

func (r *Relay) waitForAck(ctx context.Context) (serial.Frame, error) {
	waitCtx, cancel := context.WithTimeout(ctx, toolAckTimeout)
	defer cancel()

	for {
		f, err := r.recvFromC64(waitCtx, false)
		if err != nil {
			return serial.Frame{}, err
		}
		switch f.Type {
		case serial.FrameAck:
			fmt.Fprintln(os.Stderr)
			return f, nil
		case serial.FrameText:
			fmt.Fprintln(os.Stderr)
			r.textBuf = append(r.textBuf, f.Payload...)
		case serial.FrameSystem:
			fmt.Fprintln(os.Stderr)
			r.handleSystemFrame(f)
		case serial.FrameHeartbeat:
			continue
		default:
			return serial.Frame{}, fmt.Errorf("unexpected frame while waiting for ACK: %s", serial.TypeName(f.Type))
		}
	}
}

func (r *Relay) currentToolAckTimeout(name string) time.Duration {
	if name == "EXEC" || r.lastToolName == "exec" {
		return execAckTimeout
	}
	return toolAckTimeout
}

func (r *Relay) waitForAckOrSemantic(ctx context.Context, expected []byte, timeout time.Duration) (bool, error) {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	for {
		f, err := r.recvFromC64(waitCtx, false)
		if err != nil {
			return false, err
		}

		switch f.Type {
		case serial.FrameAck:
			fmt.Fprintln(os.Stderr)
			return string(f.Payload) == string(expected), nil
		case serial.FrameText:
			fmt.Fprintln(os.Stderr)
			r.textBuf = append(r.textBuf, f.Payload...)
		case serial.FrameSystem:
			fmt.Fprintln(os.Stderr)
			r.handleSystemFrame(f)
		case serial.FrameHeartbeat:
			continue
		case serial.FrameStatus, serial.FrameResult, serial.FrameError, serial.FrameLLM:
			r.pendingFrames = append(r.pendingFrames, f)
			return true, nil
		default:
			return false, fmt.Errorf("unexpected frame while waiting for ACK: %s", serial.TypeName(f.Type))
		}
	}
}

func (r *Relay) appendToolResult(userID, result string) {
	r.History.Append(userID, llm.Message{
		Role:       "tool",
		Content:    result,
		ToolCallID: r.lastToolCallID,
	})
}

func (r *Relay) recvFromC64(ctx context.Context, waitingTextAck bool) (serial.Frame, error) {
	if len(r.pendingFrames) > 0 {
		f := r.pendingFrames[0]
		r.pendingFrames = r.pendingFrames[1:]
		return f, nil
	}

	resultCh := make(chan struct {
		frame serial.Frame
		err   error
	}, 1)
	stallDumped := false
	go func() {
		f, err := r.Link.Recv()
		resultCh <- struct {
			frame serial.Frame
			err   error
		}{frame: f, err: err}
	}()

	for {
		if ctx.Err() != nil {
			return serial.Frame{}, ctx.Err()
		}

		var timeout <-chan time.Time
		if waitingTextAck {
			timeout = time.After(textAckTimeout)
		} else if r.waitingTool {
			if r.basicRunning {
				timeout = time.After(toolAckWhileRunningTimeout)
			} else {
				timeout = time.After(r.currentToolAckTimeout(r.lastToolName))
			}
		} else if r.basicRunning {
			timeout = nil
		} else {
			timeout = time.After(c64FrameTimeout)
		}

		select {
		case <-ctx.Done():
			return serial.Frame{}, ctx.Err()

		case res := <-resultCh:
			if res.err != nil {
				return serial.Frame{}, fmt.Errorf("recv: %w", res.err)
			}
			stallDumped = false
			if res.frame.Type == serial.FrameHeartbeat {
				go func() {
					f, err := r.Link.Recv()
					resultCh <- struct {
						frame serial.Frame
						err   error
					}{frame: f, err: err}
				}()
				continue
			}
			return res.frame, nil

		case <-timeout:
			if stallDumped {
				continue
			}
			if waitingTextAck {
				r.dumpTextAckStall()
				stallDumped = true
				continue
			}
			if r.waitingTool {
				r.dumpToolAckStall()
				stallDumped = true
				continue
			}
			r.dumpC64SilenceStall()
			stallDumped = true
		}
	}
}

func (r *Relay) dumpTextAckStall() {
	filename, err := writeStallDump(
		r.DebugDir,
		r.MonitorAddr,
		r.SymbolPath,
		"waiting for TEXT ack from C64",
		r.currentTextChunk(),
	)
	if err != nil {
		log.Printf("     ! text ack stall; debug dump failed: %v", err)
		return
	}
	log.Printf("     ! text ack stall; wrote debug dump to %s", filename)
}

func (r *Relay) currentTextChunk() []byte {
	if len(r.textInFlight) == 0 {
		return nil
	}
	return append([]byte(nil), r.textInFlight...)
}

func (r *Relay) dumpToolAckStall() {
	reason := "waiting for tool result from C64"
	if r.lastToolName != "" {
		reason = fmt.Sprintf("waiting for %s result from C64", r.lastToolName)
	}

	filename, err := writeStallDump(
		r.DebugDir,
		r.MonitorAddr,
		r.SymbolPath,
		reason,
		r.currentToolPayload(),
	)
	if err != nil {
		log.Printf("     ! tool stall; debug dump failed: %v", err)
		return
	}
	log.Printf("     ! tool stall; wrote debug dump to %s", filename)
}

func (r *Relay) currentToolPayload() []byte {
	if len(r.toolInFlight) == 0 {
		return nil
	}
	return append([]byte(nil), r.toolInFlight...)
}

func (r *Relay) dumpC64SilenceStall() {
	filename, err := writeStallDump(
		r.DebugDir,
		r.MonitorAddr,
		r.SymbolPath,
		"waiting for any C64 frame",
		nil,
	)
	if err != nil {
		log.Printf("     ! c64 silence stall; debug dump failed: %v", err)
		return
	}
	log.Printf("     ! c64 silence stall; wrote debug dump to %s", filename)
}

func (r *Relay) sendWithRetry(f serial.Frame) error {
	var retryBackoff = []time.Duration{500 * time.Millisecond, 1 * time.Second, 2 * time.Second}
	var err error
	for i, backoff := range retryBackoff {
		err = r.Link.Send(f)
		if err == nil {
			return nil
		}
		log.Printf("     ! send attempt %d failed: %v", i+1, err)
		time.Sleep(backoff)
	}
	return err
}

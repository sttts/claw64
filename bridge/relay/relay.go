// Package relay routes messages between chat, LLM, and the C64 serial link.
// It is not an agent — the C64 is the agent. The relay just forwards.
package relay

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/sttts/claw64/bridge/llm"
	"github.com/sttts/claw64/bridge/serial"
	"github.com/sttts/claw64/bridge/termstyle"
)

// The relay loop stays active while immediate C64/LLM work remains.
// User-visible TEXT is optional; silence is a valid completion.

// Relay routes messages between chat, LLM, and the C64 serial link.
// The C64 drives the agent loop; the relay just forwards.
type Relay struct {
	Link            *serial.Link
	LLM             llm.Completer
	History         *History
	DebugDir        string
	MonitorAddr     string
	SymbolPath      string
	StreamOut       io.Writer // streaming progress output (default: os.Stderr)
	SystemPrompt    string    // received from C64
	promptChunks    map[int]string
	resultChunks    map[int]string
	textBuf         []byte // accumulates multi-frame TEXT chunks (receive)
	textOutQueue    []byte // pending TEXT data to send in chunks (send)
	textInFlight    []byte // current TEXT chunk sent, waiting for forwarded ack
	toolInFlight    []byte // current tool payload sent, waiting for RESULT/ERROR
	waitingTool     bool
	lastToolCallID  string
	lastToolName    string
	toolStartedAt   time.Time
	basicRunning    bool
	completionDrain bool   // hold TEXT while RUNNING→RESULT drains
	nextTxID        byte   // bridge→C64 transport ID counter
	rxLastID        byte   // last accepted C64→bridge transport ID
	rxLastType      byte   // frame type of last accepted C64→bridge frame
	pendingAcks     []byte // queued ACK IDs to send during next quiet window
	pendingFrames   []queuedFrame
	lateAckIDs      map[byte]struct{}
	lastAckSentAt   time.Time
	lastC64FrameAt  time.Time
	recvOnce        sync.Once
	recvCh          chan recvResult
}

type recvResult struct {
	frame serial.Frame
	err   error
}

type queuedFrame struct {
	frame    serial.Frame
	accepted bool
}

const textChunkMax = 62

// ackTimeout is the universal timeout for any reliable frame ACK.
// With ID-based delivery, ACKs always arrive promptly regardless of
// whether BASIC is running — the C64 ACKs at transport level.
const ackTimeout = 3 * time.Second
const ackQuietWindow = 150 * time.Millisecond
const runningAckQuietWindow = 3 * time.Second
const runningRecvQuietWindow = 1 * time.Second

// textAckTimeout allows one inbound TEXT chunk, the resulting USER frame,
// and the bridge ACK back to the C64 to drain at 2400 baud before retrying.
const textAckTimeout = 7 * time.Second

// execAckTimeout covers direct EXEC commands whose first semantic boundary is
// the completed command result rather than a quick RUNNING/STORED transition.
const execAckTimeout = 8 * time.Second

// c64FrameTimeout is how long the bridge waits for any C64 frame
// before triggering a stall dump.
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
	log.Printf("%s", flowLine("LLM", "←", "C64", "SYSTEM", fmt.Sprintf("chunk [%d/%d] %d bytes", idx+1, total, len(text))))

	if len(r.promptChunks) == total {
		var prompt string
		for i := 0; i < total; i++ {
			prompt += r.promptChunks[i]
		}
		r.SystemPrompt = prompt
		r.promptChunks = nil
		log.Printf("%s", flowLine("LLM", "←", "C64", "SYSTEM", fmt.Sprintf("received (%d bytes)", len(prompt))))
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
		log.Printf("%s", flowLine("LLM", "←", "C64", "RESULT", fmt.Sprintf("screen chunk %d/%d payload=%s text=%q", idx+1, total, hex.EncodeToString(f.Payload), text)))
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

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func (r *Relay) streamOut() io.Writer {
	if r.StreamOut != nil {
		return r.StreamOut
	}
	return os.Stderr
}

// logStream prints a log-style prefix without a trailing newline.
// Characters can be appended to the same line afterwards.
func logStream(w io.Writer, format string, args ...any) {
	ts := time.Now().Format("2006/01/02 15:04:05")
	msg := fmt.Sprintf(format, args...)
	fmt.Fprint(w, termstyle.Dim(fmt.Sprintf("%s %s", ts, msg)))
}

func flowLabel(src, arrow, dst, typ string) string {
	return fmt.Sprintf("%-4s %s %s: %-11s", src, arrow, dst, typ+":")
}

func flowLine(src, arrow, dst, typ, payload string) string {
	label := flowLabel(src, arrow, dst, typ)
	if payload == "" {
		return label
	}
	return label + " " + payload
}

func logWarnf(format string, args ...any) {
	log.Println(termstyle.Warn(fmt.Sprintf(format, args...)))
}

func logErrorf(format string, args ...any) {
	log.Println(termstyle.Error(fmt.Sprintf(format, args...)))
}

// SetupProgress installs a send-progress callback on the serial link
// that prints payload bytes char-by-char as they are sent.
func (r *Relay) SetupProgress() {
	w := r.streamOut()

	r.Link.OnSendByte = func(typeName string, payload []byte, idx int) {
		if typeName == "ACK" {
			return
		}
		if sendLogSkipsTransportID(typeName) && idx == 0 {
			return
		}
		if idx == -1 {
			fmt.Fprint(w, termstyle.Dim("\n"))
			return
		}
		if payload[idx] == '\n' {
			fmt.Fprint(w, termstyle.Dim(`\n`))
		} else {
			fmt.Fprint(w, termstyle.Dim(string(payload[idx])))
		}
	}

	// Receive-side streaming is limited to SYSTEM chunks. Other semantic
	// frames are logged only after full decode/unwrap so the logs reflect
	// accepted frames rather than raw payload bytes seen before checksum
	// validation and duplicate suppression.
	r.Link.OnRecvByte = func(frameType byte, idx int, b byte) {
		if frameType == serial.FrameSystem {
			if idx == 0 {
				logStream(w, "%s ", flowLabel("LLM", "←", "C64", "SYSTEM"))
			}
			if idx < 3 {
				return
			}
			if b == '\n' {
				fmt.Fprint(w, termstyle.Dim(`\n`))
			} else {
				fmt.Fprint(w, termstyle.Dim(string(b)))
			}
			return
		}
	}
}

func sendLogSkipsTransportID(typeName string) bool {
	switch typeName {
	case "MSG", "EXEC", "STOP", "STATUS", "TEXT", "SCREENSHOT":
		return true
	default:
		return false
	}
}

// basicExecArgs is the JSON structure the LLM passes to exec.
type basicExecArgs struct {
	Command string `json:"command"`
}

// HandleMessage relays a user message through LLM and C64.
func (r *Relay) HandleMessage(ctx context.Context, userID string, text string) (string, error) {
	return r.HandleMessageStream(ctx, userID, text, nil)
}

// HandleMessageStream relays a user message and emits every C64 user-visible
// message through emit. If emit is nil, the first user-visible message is
// returned directly for compatibility with older call sites. A relay cycle may
// also complete without any user-visible text.
func (r *Relay) HandleMessageStream(ctx context.Context, userID string, text string, emit func(string) error) (string, error) {
	text = serial.ToASCII(text)

	// send user message to C64 (header now, chars stream via callback)
	logStream(r.streamOut(), "%s ", flowLabel("USER", "→", "C64", "MSG"))
	msgFrame := serial.Frame{Type: serial.FrameMsg, Payload: []byte(text)}
	if err := r.sendVerified(ctx, msgFrame, "MSG"); err != nil {
		return "", fmt.Errorf("send MSG: %w", err)
	}

	return r.eventLoop(ctx, userID, emit)
}

// eventLoop waits for C64 frames and reacts.
func (r *Relay) eventLoop(ctx context.Context, userID string, emit func(string) error) (string, error) {
	deliveredUserText := false
	completedWithoutText := false

	for {
		recvCtx := ctx
		recvCancel := func() {}
		if r.shouldUseCompletionGraceWindow(deliveredUserText, completedWithoutText) {
			graceCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
			recvCtx = graceCtx
			recvCancel = cancel
		}

		f, accepted, err := r.recvFromC64(recvCtx, len(r.textOutQueue) > 0)
		recvCancel()
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) && (deliveredUserText || completedWithoutText) {
				return "", nil
			}
			return "", err
		}
		completedWithoutText = false

		// Strip transport ID from reliable C64→bridge frames, queue ACK,
		// and suppress duplicate semantic processing.
		if !accepted {
			if r.unwrapC64Frame(&f) {
				continue
			}
		}

		// Post-unwrap semantic log — clean payload after id strip.
		// SYSTEM is streamed during receive; the other frame types are
		// logged only after full decode/unwrap.
		if f.Type == serial.FrameLLM {
			log.Printf("%s", flowLine("LLM", "←", "C64", "EVENT", string(f.Payload)))
		}
		if f.Type == serial.FrameUser {
			log.Printf("%s", flowLine("USER", "←", "C64", "TEXT", fmt.Sprintf("len=%d text=%q", len(f.Payload), truncate(string(f.Payload), 60))))
		}

		switch f.Type {
		case serial.FrameLLM:
			fmt.Fprintln(r.streamOut()) // newline after streamed payload
			r.appendC64LLMEvent(userID, string(f.Payload))
			r.drainTrailingLLMMessages(userID)
			idle, err := r.callAndDispatch(ctx, userID)
			if err != nil {
				return "", err
			}
			if idle {
				completedWithoutText = true
				continue
			}

		case serial.FrameResult:
			fmt.Fprintln(r.streamOut()) // newline after streamed payload
			r.toolInFlight = nil
			r.waitingTool = false
			r.basicRunning = false
			r.completionDrain = false
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
			idle, err := r.callAndDispatch(ctx, userID)
			if err != nil {
				return "", err
			}
			if idle {
				completedWithoutText = true
				continue
			}

		case serial.FrameStatus:
			fmt.Fprintln(r.streamOut())
			r.toolInFlight = nil
			r.waitingTool = false
			status := string(f.Payload)
			if status == "" {
				status = "UNKNOWN"
			}
			r.basicRunning = status == "RUNNING" || status == "STOP REQUESTED"
			if status == "RUNNING" {
				r.completionDrain = true
			} else if status == "READY" || status == "READY." {
				r.completionDrain = false
				r.basicRunning = false
			}

			log.Printf("%s", flowLine("LLM", "←", "C64", "STATUS!", status))
			r.appendToolResult(userID, "[C64 BASIC status]: "+status)
			idle, err := r.callAndDispatch(ctx, userID)
			if err != nil {
				return "", err
			}
			if idle {
				completedWithoutText = true
				continue
			}

		case serial.FrameError:
			log.Printf("%s", flowLine("LLM", "←", "C64", "ERROR", "timeout"))
			r.toolInFlight = nil
			r.waitingTool = false
			r.basicRunning = false
			r.completionDrain = false
			r.appendToolResult(userID, "ERROR: command timed out on C64")
			idle, err := r.callAndDispatch(ctx, userID)
			if err != nil {
				return "", err
			}
			if idle {
				completedWithoutText = true
				continue
			}

		case serial.FrameUser:
			// TEXT forwarded by C64 for the user — accumulate until the
			// C64 finishes this burst, then return it to the chat frontend.
			// Drain any immediately trailing internal frames first so the
			// next user turn does not start by consuming stale ACK/STATUS.
			fmt.Fprintln(r.streamOut())
			r.textBuf = append(r.textBuf, f.Payload...)
			r.drainTrailingAfterUserText()
			text := string(r.textBuf)
			r.textBuf = nil
			deliveredUserText = true
			if emit != nil {
				if err := emit(text); err != nil {
					return "", err
				}
				continue
			}
			return text, nil

		case serial.FrameSystem:
			fmt.Fprintln(r.streamOut()) // newline after streamed payload
			r.handleSystemFrame(f)
			continue

		case serial.FrameAck:
			fmt.Fprintln(r.streamOut())
			if ackID, ok := serial.ExtractAckID(f.Payload); ok && r.consumeLateAck(ackID) {
				continue
			}
			logWarnf("%s", flowLine("", "←", "C64", "ACK", "unexpected"))
			continue

		case serial.FrameHeartbeat:
			continue

		default:
			log.Printf("%s", flowLine("", "←", "C64", "malformed", fmt.Sprintf("unknown frame 0x%02X", f.Type)))
		}
	}
}

func (r *Relay) shouldUseCompletionGraceWindow(deliveredUserText, completedWithoutText bool) bool {
	if !deliveredUserText && !completedWithoutText {
		return false
	}
	return len(r.textOutQueue) == 0 && !r.waitingTool && !r.basicRunning
}

func (r *Relay) drainTrailingAfterUserText() {
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
		f, accepted, err := r.recvFromC64(ctx, false)
		cancel()
		if err != nil {
			return
		}

		// Unwrap reliable C64→bridge frames (strip ID, queue ACK, dedup).
		if !accepted {
			if r.unwrapC64Frame(&f) {
				continue
			}
		}

		switch f.Type {
		case serial.FrameUser:
			fmt.Fprintln(r.streamOut())
			r.textBuf = append(r.textBuf, f.Payload...)
		case serial.FrameHeartbeat:
			continue
		case serial.FrameSystem:
			fmt.Fprintln(r.streamOut())
			r.handleSystemFrame(f)
		default:
			r.pendingFrames = append(r.pendingFrames, queuedFrame{frame: f})
			return
		}
	}
}

func (r *Relay) drainTrailingLLMMessages(userID string) {
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
		f, accepted, err := r.recvFromC64(ctx, false)
		cancel()
		if err != nil {
			return
		}

		if !accepted {
			if r.unwrapC64Frame(&f) {
				continue
			}
		}

		switch f.Type {
		case serial.FrameLLM:
			fmt.Fprintln(r.streamOut())
			log.Printf("%s", flowLine("LLM", "←", "C64", "EVENT", string(f.Payload)))
			r.appendC64LLMEvent(userID, string(f.Payload))
		case serial.FrameHeartbeat:
			continue
		case serial.FrameSystem:
			fmt.Fprintln(r.streamOut())
			r.handleSystemFrame(f)
		default:
			r.pendingFrames = append(r.pendingFrames, queuedFrame{frame: f, accepted: accepted})
			return
		}
	}
}

// appendC64LLMEvent records a C64-originated LLM input event in backend history.
// Current LLM backends only understand standard chat roles, so these events are
// serialized as user messages even though they originate on the C64 side.
func (r *Relay) appendC64LLMEvent(userID, content string) {
	r.History.Append(userID, llm.Message{Role: "user", Content: content})
}

// callAndDispatch calls the LLM and dispatches the response to the C64.
// It returns idle=true when the model chose no immediate outward action:
// no tool call and no user-visible text.
func (r *Relay) callAndDispatch(ctx context.Context, userID string) (bool, error) {
	history := r.History.Get(userID)
	msgs := make([]llm.Message, 0, 1+len(history))
	if r.SystemPrompt == "" {
		return false, fmt.Errorf("no soul — C64 has not sent system prompt")
	}
	msgs = append(msgs, llm.Message{Role: "system", Content: r.SystemPrompt})
	msgs = append(msgs, history...)

	r.logLLMRequest(msgs, llmTools)
	resp, err := r.LLM.Complete(ctx, msgs, llmTools)
	if err != nil {
		return false, fmt.Errorf("llm: %w", err)
	}

	// Text responses still flow through the C64. The bridge only translates
	// protocols; the C64 remains the user-facing agent.
	if len(resp.ToolCalls) == 0 {
		if resp.Content == "" {
			log.Printf("%s", flowLine("LLM", "←", "", "response", "silent completion"))
			return true, nil
		}

		r.History.Append(userID, resp)
		r.textOutQueue = []byte(serial.ToC64Text(resp.Content))
		return false, r.sendNextTextChunk(ctx)
	}
	r.History.Append(userID, resp)

	// Tools are sequential here. The C64 is stateful, so each tool result
	// must feed the next model decision before another tool is dispatched.
	tc := resp.ToolCalls[0]
	if len(resp.ToolCalls) > 1 {
		log.Printf("%s", flowLine("", "→", "LLM", "request", fmt.Sprintf("extra tool calls ignored in this turn (%d total)", len(resp.ToolCalls))))
	}
	r.lastToolCallID = tc.ID
	r.lastToolName = tc.Function.Name

	switch tc.Function.Name {
	case "exec":
		var args basicExecArgs
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			log.Printf("%s", flowLine("", "→", "LLM", "request", fmt.Sprintf("bad args: %v", err)))
			r.History.Append(userID, llm.Message{
				Role: "tool", Content: fmt.Sprintf("ERROR: %v", err), ToolCallID: tc.ID,
			})
			return false, nil
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
		logStream(r.streamOut(), "%s ", flowLabel("LLM", "→", "C64", "EXEC"))
		if err := r.sendExec(ctx, []byte(cmd)); err != nil {
			return false, fmt.Errorf("send EXEC: %w", err)
		}

	case "screen":
		logStream(r.streamOut(), "%s ", flowLabel("LLM", "→", "C64", "SCREENSHOT"))
		fmt.Fprintln(r.streamOut())
		screenFrame := serial.Frame{Type: serial.FrameScreenshot}
		if err := r.sendVerifiedOrSemantic(ctx, screenFrame, "SCREENSHOT"); err != nil {
			return false, fmt.Errorf("send SCREENSHOT: %w", err)
		}
		r.toolInFlight = r.toolInFlight[:0]
		r.startToolWait()

	case "stop":
		logStream(r.streamOut(), "%s ", flowLabel("LLM", "→", "C64", "STOP"))
		fmt.Fprintln(r.streamOut())
		stopFrame := serial.Frame{Type: serial.FrameStop}
		if err := r.sendVerifiedOrSemantic(ctx, stopFrame, "STOP"); err != nil {
			return false, fmt.Errorf("send STOP: %w", err)
		}
		r.toolInFlight = r.toolInFlight[:0]
		r.startToolWait()

	case "status":
		logStream(r.streamOut(), "%s ", flowLabel("LLM", "→", "C64", "STATUS?"))
		fmt.Fprintln(r.streamOut())
		statusFrame := serial.Frame{Type: serial.FrameStatusReq}
		if err := r.sendVerifiedOrSemantic(ctx, statusFrame, "STATUS"); err != nil {
			return false, fmt.Errorf("send STATUS: %w", err)
		}
		r.toolInFlight = r.toolInFlight[:0]
		r.startToolWait()

	default:
		log.Printf("%s", flowLine("", "→", "LLM", "request", fmt.Sprintf("unknown tool %q", tc.Function.Name)))
		r.History.Append(userID, llm.Message{
			Role: "tool", Content: "ERROR: unknown tool", ToolCallID: tc.ID,
		})
	}
	return false, nil
}

func (r *Relay) logLLMRequest(messages []llm.Message, tools []llm.Tool) {
	if d, ok := r.LLM.(llm.RequestDescriber); ok {
		url, body, err := d.DescribeRequest(messages, tools)
		if err != nil {
			log.Printf("%s", flowLine("", "→", "LLM", "request", fmt.Sprintf("preview failed: %v", err)))
			return
		}
		log.Printf("%s", flowLine("", "→", "LLM", "request", url))
		log.Printf("%s", flowLine("", "→", "LLM", "request", string(body)))
		return
	}
	log.Printf("%s", flowLine("", "→", "LLM", "request", "request"))
}

func (r *Relay) sendNextTextChunk(ctx context.Context) error {
	for len(r.textOutQueue) > 0 {
		// Completion drain: hold TEXT while C64 is draining RESULT/STATUS.
		if r.completionDrain {
			log.Printf("%s", flowLine("", "→", "C64", "TEXT", fmt.Sprintf("queued (%d bytes), waiting for completion drain", len(r.textOutQueue))))
			return nil
		}
		chunk := r.textOutQueue
		if len(chunk) > textChunkMax {
			chunk = chunk[:textChunkMax]
		}
		r.textOutQueue = r.textOutQueue[len(chunk):]
		r.textInFlight = append(r.textInFlight[:0], chunk...)
		if err := r.sendTextChunk(ctx, chunk); err != nil {
			fmt.Fprintln(r.streamOut())
			return fmt.Errorf("send TEXT: %w", err)
		}
		r.textInFlight = r.textInFlight[:0]
	}
	return nil
}

func (r *Relay) sendTextChunk(ctx context.Context, chunk []byte) error {
	id := r.allocID()
	frame := serial.Frame{Type: serial.FrameText, Payload: serial.PrependID(id, chunk)}
	retryDelays := []time.Duration{500 * time.Millisecond, 1 * time.Second, 2 * time.Second}

	for attempt := 0; attempt < len(retryDelays); attempt++ {
		r.settlePendingAcks()
		logStream(r.streamOut(), "%s ", flowLabel("LLM", "→", "C64", "TEXT"))

		if err := r.Link.Send(frame); err != nil {
			return err
		}

		waitCtx, cancel := context.WithTimeout(ctx, textAckTimeout)
		_, err := r.waitForAckID(waitCtx, id, true)
		cancel()
		if err == nil {
			return nil
		}
		logWarnf("%s", flowLine("", "→", "C64", "TEXT", fmt.Sprintf("ack attempt %d failed: %v", attempt+1, err)))
		if attempt+1 < len(retryDelays) {
			time.Sleep(retryDelays[attempt])
		}
	}
	return fmt.Errorf("TEXT delivery could not be verified after %d attempt(s)", len(retryDelays))
}

func (r *Relay) startToolWait() {
	r.waitingTool = true
	r.toolStartedAt = time.Now()
}

func (r *Relay) sendExec(ctx context.Context, cmd []byte) error {
	execFrame := serial.Frame{Type: serial.FrameExec, Payload: cmd}
	if err := r.sendVerifiedOrSemanticWith(
		ctx,
		execFrame,
		"EXEC",
		execAckTimeout,
		[]time.Duration{1 * time.Second, 2 * time.Second},
	); err != nil {
		return err
	}
	fmt.Fprintln(r.streamOut())

	r.toolInFlight = append(r.toolInFlight[:0], cmd...)
	r.startToolWait()
	return nil
}

func (r *Relay) sendVerified(ctx context.Context, frame serial.Frame, name string) error {
	id := r.allocID()
	frame.Payload = serial.PrependID(id, frame.Payload)
	retryDelays := []time.Duration{500 * time.Millisecond, 1 * time.Second, 2 * time.Second}

	for attempt := 0; attempt < len(retryDelays); attempt++ {
		r.settlePendingAcks()

		if err := r.Link.Send(frame); err != nil {
			return err
		}

		_, err := r.waitForAckID(ctx, id, false)
		if err == nil {
			return nil
		}
		logWarnf("     ! %s ack attempt %d failed: %v", name, attempt+1, err)
		if attempt+1 < len(retryDelays) {
			time.Sleep(retryDelays[attempt])
		}
	}
	return fmt.Errorf("%s delivery could not be verified after %d attempts", name, len(retryDelays))
}

func (r *Relay) sendVerifiedOrSemantic(ctx context.Context, frame serial.Frame, name string) error {
	return r.sendVerifiedOrSemanticWith(ctx, frame, name, ackTimeout, []time.Duration{500 * time.Millisecond, 1 * time.Second, 2 * time.Second})
}

func (r *Relay) sendVerifiedOrSemanticWith(ctx context.Context, frame serial.Frame, name string, timeout time.Duration, retryDelays []time.Duration) error {
	id := r.allocID()
	frame.Payload = serial.PrependID(id, frame.Payload)
	for attempt := 0; attempt < len(retryDelays); attempt++ {
		r.settlePendingAcks()

		if err := r.Link.Send(frame); err != nil {
			return err
		}

		ok, err := r.waitForAckOrSemantic(ctx, id, timeout)
		if err != nil {
			logWarnf("     ! %s ack attempt %d failed: %v", name, attempt+1, err)
			if attempt+1 < len(retryDelays) {
				time.Sleep(retryDelays[attempt])
			}
			continue
		}
		if ok {
			return nil
		}
		logWarnf("     ! %s ack attempt %d failed: no confirmation", name, attempt+1)
		if attempt+1 < len(retryDelays) {
			time.Sleep(retryDelays[attempt])
		}
	}
	return fmt.Errorf("%s delivery could not be verified after %d attempt(s)", name, len(retryDelays))
}

// allocID returns the next transport ID and advances the counter.
// IDs start at 1; 0 is reserved for "unset".
func (r *Relay) allocID() byte {
	id := r.nextTxID
	if id == 0 {
		id = 1
	}
	r.nextTxID = id + 1
	if r.nextTxID == 0 {
		r.nextTxID = 1 // skip 0 on wraparound
	}
	return id
}

// queueAckToC64 queues an ACK ID to be sent during the next quiet window.
func (r *Relay) queueAckToC64(id byte) {
	r.pendingAcks = append(r.pendingAcks, id)
}

func (r *Relay) rememberLateAck(id byte) {
	if r.lateAckIDs == nil {
		r.lateAckIDs = make(map[byte]struct{})
	}
	r.lateAckIDs[id] = struct{}{}
}

func (r *Relay) consumeLateAck(id byte) bool {
	if _, ok := r.lateAckIDs[id]; !ok {
		return false
	}
	delete(r.lateAckIDs, id)
	return true
}

func (r *Relay) ackReliableFrameNow(f serial.Frame) {
	if !serial.IsReliableC64(f.Type) {
		return
	}
	id, _, ok := serial.StripID(f.Payload)
	if !ok {
		return
	}
	r.queueAckToC64(id)
	r.flushPendingAcks()
}

func (r *Relay) acceptC64Frame(f *serial.Frame, ackNow bool) bool {
	if !serial.IsReliableC64(f.Type) {
		return false
	}
	id, body, ok := serial.StripID(f.Payload)
	if !ok {
		log.Printf("%s", flowLine("", "←", "C64", "malformed", fmt.Sprintf("%s empty payload, cannot strip id", serial.TypeName(f.Type))))
		return false
	}
	if ackNow {
		r.queueAckToC64(id)
		r.flushPendingAcks()
	}
	f.Payload = body
	if id == r.rxLastID && f.Type == r.rxLastType {
		log.Printf("%s", flowLine("", "←", "C64", "dedup", fmt.Sprintf("type=%s id=%d", serial.TypeName(f.Type), id)))
		return true
	}
	r.rxLastID = id
	r.rxLastType = f.Type
	return false
}

// flushPendingAcks sends all queued ACK frames to the C64.
// Called in recvFromC64 before the select (quiet window) and before
// bridge→C64 sends to prevent ACK deadlocks.
func (r *Relay) flushPendingAcks() {
	sent := false
	for _, id := range r.pendingAcks {
		log.Printf("%s", flowLine("", "→", "C64", "ACK", fmt.Sprintf("id=%d", id)))
		ack := serial.Frame{Type: serial.FrameAck, Payload: []byte{id}}
		if err := r.Link.Send(ack); err != nil {
			logErrorf("     ! failed to send ACK(%d) to C64: %v", id, err)
		}
		sent = true
	}
	r.pendingAcks = r.pendingAcks[:0]
	if sent {
		r.lastAckSentAt = time.Now()
	}
}

// settlePendingAcks flushes queued bridge ACKs before sending a new
// bridge→C64 frame and gives the C64 a short window to consume them.
func (r *Relay) settlePendingAcks() {
	if len(r.pendingAcks) > 0 {
		time.Sleep(10 * time.Millisecond)
		r.flushPendingAcks()
	}
	if r.lastAckSentAt.IsZero() {
		return
	}
	quietWindow := ackQuietWindow
	if r.basicRunning {
		quietWindow = runningAckQuietWindow
	}
	if remain := quietWindow - time.Since(r.lastAckSentAt); remain > 0 {
		time.Sleep(remain)
	}
	if !r.basicRunning || r.lastC64FrameAt.IsZero() {
		return
	}
	if remain := runningRecvQuietWindow - time.Since(r.lastC64FrameAt); remain > 0 {
		time.Sleep(remain)
	}
}

// unwrapC64Frame strips the transport ID from a reliable C64→bridge frame,
// queues ACK, and returns (frame with stripped payload, isDuplicate).
func (r *Relay) unwrapC64Frame(f *serial.Frame) bool {
	return r.acceptC64Frame(f, true)
}

func (r *Relay) waitForAckID(ctx context.Context, id byte, waitingTextAck bool) (serial.Frame, error) {
	for {
		f, err := r.recvFromSocketOnly(ctx, waitingTextAck)
		if err != nil {
			return serial.Frame{}, err
		}
		switch f.Type {
		case serial.FrameAck:
			fmt.Fprintln(r.streamOut())
			ackID, ok := serial.ExtractAckID(f.Payload)
			if ok && ackID == id {
				return f, nil
			}
			if ok && r.consumeLateAck(ackID) {
				continue
			}
			logWarnf("     ! ACK id mismatch: got %d want %d", ackID, id)
			continue
		case serial.FrameUser, serial.FrameSystem, serial.FrameStatus,
			serial.FrameResult, serial.FrameError, serial.FrameLLM:
			if r.acceptC64Frame(&f, true) {
				continue
			}
			r.pendingFrames = append(r.pendingFrames, queuedFrame{frame: f, accepted: true})
			continue
		case serial.FrameHeartbeat:
			continue
		default:
			return serial.Frame{}, fmt.Errorf("unexpected frame while waiting for ACK: %s", serial.TypeName(f.Type))
		}
	}
}

func (r *Relay) waitForAckOrSemantic(ctx context.Context, id byte, timeout time.Duration) (bool, error) {
	deadline := time.Now().Add(timeout)

	for {
		waitCtx, cancel := context.WithDeadline(ctx, deadline)
		f, err := r.recvFromSocketOnly(waitCtx, false)
		cancel()
		if err != nil {
			return false, err
		}

		switch f.Type {
		case serial.FrameAck:
			fmt.Fprintln(r.streamOut())
			ackID, ok := serial.ExtractAckID(f.Payload)
			if ok && ackID == id {
				return true, nil
			}
			if ok && r.consumeLateAck(ackID) {
				continue
			}
			logWarnf("     ! ACK id mismatch: got %d want %d", ackID, id)
			continue
		case serial.FrameUser, serial.FrameSystem:
			if r.acceptC64Frame(&f, true) {
				continue
			}
			deadline = time.Now().Add(timeout)
			fmt.Fprintln(r.streamOut())
			r.pendingFrames = append(r.pendingFrames, queuedFrame{frame: f, accepted: true})
		case serial.FrameHeartbeat:
			continue
		case serial.FrameStatus, serial.FrameResult, serial.FrameError, serial.FrameLLM:
			if r.acceptC64Frame(&f, true) {
				continue
			}
			deadline = time.Now().Add(timeout)
			r.pendingFrames = append(r.pendingFrames, queuedFrame{frame: f, accepted: true})
			continue
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

func (r *Relay) ensureRecvLoop() {
	r.recvOnce.Do(func() {
		r.recvCh = make(chan recvResult, 1)
		go func() {
			for {
				f, err := r.Link.Recv()
				r.recvCh <- recvResult{frame: f, err: err}
				if err != nil {
					return
				}
			}
		}()
	})
}

func (r *Relay) recvFromSocketOnly(ctx context.Context, waitingTextAck bool) (serial.Frame, error) {
	r.ensureRecvLoop()
	stallDumped := false

	for {
		if ctx.Err() != nil {
			return serial.Frame{}, ctx.Err()
		}

		var timeout <-chan time.Time
		if waitingTextAck {
			timeout = time.After(textAckTimeout)
		} else if r.waitingTool {
			timeout = time.After(ackTimeout)
		} else if r.basicRunning {
			timeout = nil
		} else {
			timeout = time.After(c64FrameTimeout)
		}

		if !waitingTextAck {
			if len(r.pendingAcks) > 0 {
				time.Sleep(10 * time.Millisecond)
			}
			r.flushPendingAcks()
		}

		select {
		case <-ctx.Done():
			return serial.Frame{}, ctx.Err()

		case res := <-r.recvCh:
			if res.err != nil {
				return serial.Frame{}, fmt.Errorf("recv: %w", res.err)
			}
			stallDumped = false
			if res.frame.Type == serial.FrameHeartbeat {
				continue
			}
			r.lastC64FrameAt = time.Now()
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

func (r *Relay) recvFromC64(ctx context.Context, waitingTextAck bool) (serial.Frame, bool, error) {
	r.ensureRecvLoop()

	if len(r.pendingFrames) > 0 {
		q := r.pendingFrames[0]
		r.pendingFrames = r.pendingFrames[1:]
		return q.frame, q.accepted, nil
	}
	stallDumped := false

	for {
		if ctx.Err() != nil {
			return serial.Frame{}, false, ctx.Err()
		}

		var timeout <-chan time.Time
		if waitingTextAck {
			timeout = time.After(textAckTimeout)
		} else if r.waitingTool {
			timeout = time.After(ackTimeout)
		} else if r.basicRunning {
			timeout = nil
		} else {
			timeout = time.After(c64FrameTimeout)
		}

		// While waiting for a bridge→C64 TEXT ACK, do not inject queued
		// bridge ACKs back into the C64. At that point the C64 is expected
		// to emit USER and then its own ACK; extra inbound bytes would
		// reintroduce RX-side jitter during the sensitive TX window.
		if !waitingTextAck {
			// Flush queued ACKs after a brief quiet window. The C64's
			// KERNAL TX ring may still be draining the last byte's stop
			// bit. Wait 10ms to let it finish before injecting RX traffic
			// that would cause NMI jitter on the TX path.
			if len(r.pendingAcks) > 0 {
				time.Sleep(10 * time.Millisecond)
			}
			r.flushPendingAcks()
		}

		select {
		case <-ctx.Done():
			return serial.Frame{}, false, ctx.Err()

		case res := <-r.recvCh:
			if res.err != nil {
				return serial.Frame{}, false, fmt.Errorf("recv: %w", res.err)
			}
			stallDumped = false
			if res.frame.Type == serial.FrameHeartbeat {
				continue
			}
			r.lastC64FrameAt = time.Now()
			return res.frame, false, nil

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
		logErrorf("     ! text ack stall; debug dump failed: %v", err)
		return
	}
	logErrorf("     ! text ack stall; wrote debug dump to %s", filename)
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
		logErrorf("     ! tool stall; debug dump failed: %v", err)
		return
	}
	logErrorf("     ! tool stall; wrote debug dump to %s", filename)
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
		logErrorf("     ! c64 silence stall; debug dump failed: %v", err)
		return
	}
	logErrorf("     ! c64 silence stall; wrote debug dump to %s", filename)
}

// Package relay routes messages between chat, LLM, and the C64 serial link.
// It is not an agent — the C64 is the agent. The relay just forwards.
package relay

import (
	"context"
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
}

const textChunkMax = 120
const textAckTimeout = 8 * time.Second
const toolAckTimeout = 8 * time.Second
const c64FrameTimeout = 8 * time.Second

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

// basicExecArgs is the JSON structure the LLM passes to basic_exec.
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
	if err := r.sendWithRetry(msgFrame); err != nil {
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

		case serial.FrameResult:
			fmt.Fprintln(os.Stderr) // newline after streamed payload
			r.toolInFlight = nil
			r.waitingTool = false
			resultText, complete := r.handleResultFrame(f)
			if !complete {
				continue
			}

			resultPrefix := "[C64 screen output]: "
			if r.lastToolName == "text_screenshot" {
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

		case serial.FrameError:
			log.Printf("C64 → LLM:   ERROR (timeout)")
			r.toolInFlight = nil
			r.waitingTool = false
			r.appendToolResult(userID, "ERROR: command timed out on C64")
			if err := r.callAndDispatch(ctx, userID); err != nil {
				return "", err
			}

		case serial.FrameText:
			// TEXT forwarded by C64 after parsing — accumulate and send next chunk
			fmt.Fprintln(os.Stderr) // newline after streamed payload
			r.textBuf = append(r.textBuf, f.Payload...)
			r.textInFlight = nil
			if len(r.textOutQueue) > 0 {
				// more chunks to send — wait for this echo, then send next
				if err := r.sendNextTextChunk(); err != nil {
					return "", err
				}
				continue
			}
			// all chunks sent and echoed — return full text
			text := string(r.textBuf)
			r.textBuf = nil
			return text, nil

		case serial.FrameSystem:
			fmt.Fprintln(os.Stderr) // newline after streamed payload
			r.handleSystemFrame(f)
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

	r.logLLMRequest(msgs, []llm.Tool{llm.BasicExecTool, llm.TextScreenshotTool})
	resp, err := r.LLM.Complete(ctx, msgs, []llm.Tool{llm.BasicExecTool, llm.TextScreenshotTool})
	if err != nil {
		return fmt.Errorf("llm: %w", err)
	}
	r.History.Append(userID, resp)

	// text response — send to C64, which forwards to user
	if len(resp.ToolCalls) == 0 {
		if resp.Content == "" {
			logStream("LLM → C64:  TEXT ")
			fmt.Fprintln(os.Stderr, "(empty)")
			return nil
		}
		// queue TEXT chunks — eventLoop sends one at a time,
		// waiting for C64 echo before sending the next
		r.textOutQueue = []byte(serial.ToASCII(resp.Content))
		return r.sendNextTextChunk()
	}

	// tool calls
	for _, tc := range resp.ToolCalls {
		switch tc.Function.Name {
		case "basic_exec":
			var args basicExecArgs
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
				log.Printf("LLM → ???:   bad args: %v", err)
				r.History.Append(userID, llm.Message{
					Role: "tool", Content: fmt.Sprintf("ERROR: %v", err), ToolCallID: tc.ID,
				})
				continue
			}
			// sanitize: strip newlines, take first line only, truncate to 80 chars
			cmd := serial.ToASCII(args.Command)
			if i := strings.IndexAny(cmd, "\n\r"); i >= 0 {
				cmd = cmd[:i]
			}
			if len(cmd) > 80 {
				cmd = cmd[:80]
			}
			logStream("LLM → C64:  EXEC ")
			r.lastToolCallID = tc.ID
			r.lastToolName = tc.Function.Name
			r.toolInFlight = append(r.toolInFlight[:0], cmd...)
			r.waitingTool = true

			execFrame := serial.Frame{Type: serial.FrameExec, Payload: []byte(cmd)}
			if err := r.sendWithRetry(execFrame); err != nil {
				return fmt.Errorf("send EXEC: %w", err)
			}

		case "text_screenshot":
			logStream("LLM → C64:  SCREENSHOT ")
			fmt.Fprintln(os.Stderr)
			r.lastToolCallID = tc.ID
			r.lastToolName = tc.Function.Name
			r.toolInFlight = r.toolInFlight[:0]
			r.waitingTool = true

			screenFrame := serial.Frame{Type: serial.FrameScreenshot}
			if err := r.sendWithRetry(screenFrame); err != nil {
				return fmt.Errorf("send SCREENSHOT: %w", err)
			}

		default:
			log.Printf("LLM → ???:   unknown tool %q", tc.Function.Name)
			r.History.Append(userID, llm.Message{
				Role: "tool", Content: "ERROR: unknown tool", ToolCallID: tc.ID,
			})
		}
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

// sendNextTextChunk sends one TEXT chunk from the queue.
func (r *Relay) sendNextTextChunk() error {
	if len(r.textOutQueue) == 0 {
		return nil
	}
	chunk := r.textOutQueue
	if len(chunk) > textChunkMax {
		chunk = chunk[:textChunkMax]
	}
	r.textOutQueue = r.textOutQueue[len(chunk):]
	r.textInFlight = append(r.textInFlight[:0], chunk...)
	logStream("LLM → C64:  TEXT ")
	textFrame := serial.Frame{Type: serial.FrameText, Payload: chunk}
	if err := r.sendWithRetry(textFrame); err != nil {
		fmt.Fprintln(os.Stderr)
		return fmt.Errorf("send TEXT: %w", err)
	}
	return nil
}

func (r *Relay) appendToolResult(userID, result string) {
	r.History.Append(userID, llm.Message{
		Role:       "tool",
		Content:    result,
		ToolCallID: r.lastToolCallID,
	})
}

func (r *Relay) recvFromC64(ctx context.Context, waitingTextAck bool) (serial.Frame, error) {
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
			timeout = time.After(toolAckTimeout)
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

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

	"github.com/sttts/claw64/llm"
	"github.com/sttts/claw64/serial"
)

// maxIterations caps the LLM loop to prevent infinite cycles.
const maxIterations = 10


// Relay routes messages between chat, LLM, and the C64 serial link.
// The C64 drives the agent loop; the relay just forwards.
type Relay struct {
	Link           *serial.Link
	LLM            llm.Completer
	History        *History
	SystemPrompt   string // received from C64
	promptChunks   map[int]string
	textBuf        []byte // accumulates multi-frame TEXT chunks
	lastToolCallID string
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

// logStream prints a log-style prefix without a trailing newline.
// Characters can be appended to the same line afterwards.
func logStream(format string, args ...any) {
	ts := time.Now().Format("2006/01/02 15:04:05")
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(os.Stderr, "%s %s", ts, msg)
}

// SetupProgress installs a send-progress callback on the serial link
// that prints payload bytes char-by-char as they are sent.
func (r *Relay) SetupProgress() {
	r.Link.OnSendByte = func(typeName string, payload []byte, idx int) {
		if idx == -1 {
			fmt.Fprintln(os.Stderr)
			return
		}
		if payload[idx] == '\n' {
			fmt.Fprint(os.Stderr, `\n`)
		} else {
			fmt.Fprintf(os.Stderr, "%c", payload[idx])
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
		if b == '\n' {
			fmt.Fprint(os.Stderr, `\n`)
		} else {
			fmt.Fprintf(os.Stderr, "%c", b)
		}
	}
}


// basicExecArgs is the JSON structure the LLM passes to basic_exec.
type basicExecArgs struct {
	Command string `json:"command"`
}

// HandleMessage relays a user message through LLM and C64.
func (r *Relay) HandleMessage(ctx context.Context, userID string, text string) (string, error) {
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
	for i := 0; i < maxIterations; i++ {
		f, err := r.recvFromC64(ctx)
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
			// Prefix result so the LLM knows this is screen output, not human input
			result := "[C64 screen output]: " + string(f.Payload)
			if len(f.Payload) == 0 {
				result = "[C64 screen output]: (empty)"
			}
			r.appendToolResult(userID, result)
			if err := r.callAndDispatch(ctx, userID); err != nil {
				return "", err
			}

		case serial.FrameError:
			log.Printf("C64 → LLM:   ERROR (timeout)")
			r.appendToolResult(userID, "ERROR: command timed out on C64")
			if err := r.callAndDispatch(ctx, userID); err != nil {
				return "", err
			}

		case serial.FrameText:
			// TEXT forwarded by C64 — accumulate chunks.
			// Chunk of 120 bytes = more to come. Shorter = final.
			fmt.Fprintln(os.Stderr) // newline after streamed payload
			r.textBuf = append(r.textBuf, f.Payload...)
			if len(f.Payload) < 120 {
				text := string(r.textBuf)
				r.textBuf = nil
				return text, nil
			}
			continue

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

	return "", fmt.Errorf("event loop exceeded %d iterations", maxIterations)
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

	log.Printf("     → LLM:  calling model...")
	resp, err := r.LLM.Complete(ctx, msgs, []llm.Tool{llm.BasicExecTool})
	if err != nil {
		return fmt.Errorf("llm: %w", err)
	}
	r.History.Append(userID, resp)

	// text response — send to C64, which forwards to user
	if len(resp.ToolCalls) == 0 {
		logStream("LLM → C64:  TEXT ")
		if resp.Content != "" {
			// send in chunks of 120 bytes (frame max is 127 with 7-bit length)
			payload := []byte(resp.Content)
			for len(payload) > 0 {
				chunk := payload
				if len(chunk) > 120 {
					chunk = chunk[:120]
				}
				payload = payload[len(chunk):]
				textFrame := serial.Frame{Type: serial.FrameText, Payload: chunk}
				if err := r.sendWithRetry(textFrame); err != nil {
					fmt.Fprintln(os.Stderr)
					log.Printf("     ! TEXT send failed: %v", err)
					break
				}
			}
		} else {
			fmt.Fprintln(os.Stderr, "(empty)")
		}
		// C64 forwards each chunk back as TEXT frame.
		// eventLoop concatenates until a chunk < 120 bytes (= final).
		return nil
	}

	// tool calls
	for _, tc := range resp.ToolCalls {
		if tc.Function.Name != "basic_exec" {
			log.Printf("LLM → ???:   unknown tool %q", tc.Function.Name)
			r.History.Append(userID, llm.Message{
				Role: "tool", Content: "ERROR: unknown tool", ToolCallID: tc.ID,
			})
			continue
		}

		var args basicExecArgs
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			log.Printf("LLM → ???:   bad args: %v", err)
			r.History.Append(userID, llm.Message{
				Role: "tool", Content: fmt.Sprintf("ERROR: %v", err), ToolCallID: tc.ID,
			})
			continue
		}
		// sanitize: strip newlines, take first line only, truncate to 80 chars
		cmd := args.Command
		if i := strings.IndexAny(cmd, "\n\r"); i >= 0 {
			cmd = cmd[:i]
		}
		if len(cmd) > 80 {
			cmd = cmd[:80]
		}
		logStream("LLM → C64:  EXEC ")

		r.lastToolCallID = tc.ID

		execFrame := serial.Frame{Type: serial.FrameExec, Payload: []byte(cmd)}
		if err := r.sendWithRetry(execFrame); err != nil {
			return fmt.Errorf("send EXEC: %w", err)
		}
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

func (r *Relay) recvFromC64(ctx context.Context) (serial.Frame, error) {
	for {
		if ctx.Err() != nil {
			return serial.Frame{}, ctx.Err()
		}
		f, err := r.Link.Recv()
		if err != nil {
			return serial.Frame{}, fmt.Errorf("recv: %w", err)
		}
		if f.Type == serial.FrameHeartbeat {
			continue
		}
		return f, nil
	}
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

// Package relay routes messages between chat, LLM, and the C64 serial link.
// It is not an agent — the C64 is the agent. The relay just forwards.
package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/sttts/claw64/llm"
	"github.com/sttts/claw64/serial"
)

// maxIterations caps the LLM loop to prevent infinite cycles.
const maxIterations = 10

// retry backoff durations for serial send failures.
var retryBackoff = []time.Duration{500 * time.Millisecond, 1 * time.Second, 2 * time.Second}

// Relay routes messages between chat, LLM, and the C64 serial link.
// The C64 drives the agent loop; the relay just forwards.
type Relay struct {
	Link           *serial.Link
	LLM            llm.Completer
	History        *History
	lastText       string
	lastToolCallID string
}

// basicExecArgs is the JSON structure the LLM passes to basic_exec.
type basicExecArgs struct {
	Command string `json:"command"`
}

// HandleMessage relays a user message through LLM and C64.
func (r *Relay) HandleMessage(ctx context.Context, userID string, text string) (string, error) {
	r.History.Append(userID, llm.Message{Role: "user", Content: text})

	// send user message to C64
	log.Printf("USER → C64:  MSG %q", text)
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
			log.Printf("C64 → LLM:   %q", string(f.Payload))
			if err := r.callAndDispatch(ctx, userID); err != nil {
				return "", err
			}

		case serial.FrameResult:
			log.Printf("C64 → LLM:   RESULT %q", string(f.Payload))
			r.appendToolResult(userID, string(f.Payload))
			if err := r.callAndDispatch(ctx, userID); err != nil {
				return "", err
			}

		case serial.FrameError:
			log.Printf("C64 → LLM:   ERROR (timeout)")
			r.appendToolResult(userID, "ERROR: command timed out on C64")
			if err := r.callAndDispatch(ctx, userID); err != nil {
				return "", err
			}

		case serial.FrameHeartbeat:
			continue

		default:
			log.Printf("C64 → ???:   unknown frame 0x%02X", f.Type)
		}

		if r.lastText != "" {
			text := r.lastText
			r.lastText = ""
			return text, nil
		}
	}

	return "", fmt.Errorf("event loop exceeded %d iterations", maxIterations)
}

// callAndDispatch calls the LLM and dispatches the response to the C64.
func (r *Relay) callAndDispatch(ctx context.Context, userID string) error {
	history := r.History.Get(userID)
	msgs := make([]llm.Message, 0, 1+len(history))
	msgs = append(msgs, llm.Message{Role: "system", Content: llm.SystemPrompt})
	msgs = append(msgs, history...)

	log.Printf("     → LLM:  calling model...")
	resp, err := r.LLM.Complete(ctx, msgs, []llm.Tool{llm.BasicExecTool})
	if err != nil {
		return fmt.Errorf("llm: %w", err)
	}
	r.History.Append(userID, resp)

	// text response
	if len(resp.ToolCalls) == 0 {
		log.Printf("LLM → USER:  %q", resp.Content)
		if resp.Content != "" {
			textFrame := serial.Frame{Type: serial.FrameText, Payload: []byte(resp.Content)}
			if err := r.sendWithRetry(textFrame); err != nil {
				log.Printf("LLM → C64:   TEXT send failed: %v", err)
			} else {
				log.Printf("LLM → C64:   TEXT [%d bytes]", len(resp.Content))
			}
		}
		r.lastText = resp.Content
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
		log.Printf("LLM → C64:  EXEC %q", args.Command)

		r.lastToolCallID = tc.ID

		execFrame := serial.Frame{Type: serial.FrameExec, Payload: []byte(args.Command)}
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

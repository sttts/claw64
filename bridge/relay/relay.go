// Package relay routes messages between chat, LLM, and the C64 serial link.
// It is not an agent — the C64 is the agent. The relay just forwards frames.
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
	Link     *serial.Link
	LLM      llm.Completer
	History  *History
	lastText       string // set by callAndDispatch when LLM returns text
	lastToolCallID string // tracks which tool call the next RESULT belongs to
}

// basicExecArgs is the JSON structure the LLM passes to basic_exec.
type basicExecArgs struct {
	Command string `json:"command"`
}

// HandleMessage relays a user message through LLM and C64.
// Returns the final text response for the chat channel.
func (r *Relay) HandleMessage(ctx context.Context, userID string, text string) (string, error) {
	// append user message to history
	r.History.Append(userID, llm.Message{Role: "user", Content: text})

	// send to C64 — the C64 is the agent, it decides what happens next
	msgFrame := serial.Frame{Type: serial.FrameMsg, Payload: []byte(text)}
	if err := r.sendWithRetry(msgFrame); err != nil {
		return "", fmt.Errorf("send MSG: %w", err)
	}

	// wait for C64 to drive the conversation
	return r.eventLoop(ctx, userID)
}

// eventLoop waits for C64 frames and reacts. The C64 drives the flow:
// - LLM_MSG: call LLM, dispatch response (EXEC or TEXT) back to C64
// - RESULT: feed tool result to LLM, dispatch response back to C64
// - ERROR: feed error to LLM, dispatch response back to C64
// Returns when the LLM produces a text response (no tool calls).
func (r *Relay) eventLoop(ctx context.Context, userID string) (string, error) {
	for i := 0; i < maxIterations; i++ {
		// read next frame from C64
		f, err := r.recvFromC64(ctx)
		if err != nil {
			return "", err
		}

		switch f.Type {
		case serial.FrameLLM:
			// C64 asks us to call the LLM (payload is context, not a duplicate user msg)
			log.Printf("relay: LLM_MSG from C64: %q", string(f.Payload))
			if err := r.callAndDispatch(ctx, userID); err != nil {
				return "", err
			}

		case serial.FrameResult:
			// tool result — feed to LLM
			log.Printf("relay: RESULT from C64: %q", string(f.Payload))
			// find the last assistant message with tool calls to get the ID
			r.appendToolResult(userID, string(f.Payload))
			if err := r.callAndDispatch(ctx, userID); err != nil {
				return "", err
			}

		case serial.FrameError:
			// tool error — feed to LLM
			log.Printf("relay: ERROR from C64")
			r.appendToolResult(userID, "ERROR: command timed out on C64")
			if err := r.callAndDispatch(ctx, userID); err != nil {
				return "", err
			}

		case serial.FrameHeartbeat:
			continue

		default:
			log.Printf("relay: unexpected frame 0x%02X, skipping", f.Type)
		}

		// check if we just sent a TEXT frame (meaning conversation is done)
		// callAndDispatch returns the text via lastText
		if r.lastText != "" {
			text := r.lastText
			r.lastText = ""
			return text, nil
		}
	}

	return "", fmt.Errorf("event loop exceeded %d iterations", maxIterations)
}

// callAndDispatch calls the LLM and dispatches the response to the C64.
// If the LLM returns text: sends TEXT frame, sets r.lastText.
// If the LLM returns tool calls: sends EXEC frames.
func (r *Relay) callAndDispatch(ctx context.Context, userID string) error {
	// call LLM
	history := r.History.Get(userID)
	msgs := make([]llm.Message, 0, 1+len(history))
	msgs = append(msgs, llm.Message{Role: "system", Content: llm.SystemPrompt})
	msgs = append(msgs, history...)

	resp, err := r.LLM.Complete(ctx, msgs, []llm.Tool{llm.BasicExecTool})
	if err != nil {
		return fmt.Errorf("llm: %w", err)
	}
	r.History.Append(userID, resp)

	// text response — send TEXT frame to C64, signal completion
	if len(resp.ToolCalls) == 0 {
		if resp.Content != "" {
			textFrame := serial.Frame{Type: serial.FrameText, Payload: []byte(resp.Content)}
			if err := r.sendWithRetry(textFrame); err != nil {
				log.Printf("relay: failed to send TEXT: %v", err)
			}
		}
		r.lastText = resp.Content
		return nil
	}

	// tool calls — send EXEC frames to C64
	for _, tc := range resp.ToolCalls {
		if tc.Function.Name != "basic_exec" {
			r.History.Append(userID, llm.Message{
				Role: "tool", Content: "ERROR: unknown tool", ToolCallID: tc.ID,
			})
			continue
		}

		var args basicExecArgs
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			r.History.Append(userID, llm.Message{
				Role: "tool", Content: fmt.Sprintf("ERROR: %v", err), ToolCallID: tc.ID,
			})
			continue
		}
		log.Printf("relay: EXEC %q", args.Command)

		// remember tool call ID so we can match the RESULT
		r.lastToolCallID = tc.ID

		execFrame := serial.Frame{Type: serial.FrameExec, Payload: []byte(args.Command)}
		if err := r.sendWithRetry(execFrame); err != nil {
			return fmt.Errorf("send EXEC: %w", err)
		}
	}
	return nil
}

// appendToolResult appends a tool result to history using the last tool call ID.
func (r *Relay) appendToolResult(userID, result string) {
	r.History.Append(userID, llm.Message{
		Role:       "tool",
		Content:    result,
		ToolCallID: r.lastToolCallID,
	})
}

// recvFromC64 reads one frame from the C64, skipping heartbeats in a tight loop.
func (r *Relay) recvFromC64(ctx context.Context) (serial.Frame, error) {
	log.Println("relay: waiting for C64 frame...")
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

// sendWithRetry sends a frame, retrying up to 3 times with backoff.
func (r *Relay) sendWithRetry(f serial.Frame) error {
	var err error
	for i, backoff := range retryBackoff {
		err = r.Link.Send(f)
		if err == nil {
			log.Printf("relay: sent %s [%d bytes]", serial.TypeName(f.Type), len(f.Payload))
			return nil
		}
		log.Printf("relay: send attempt %d failed: %v", i+1, err)
		time.Sleep(backoff)
	}
	return err
}

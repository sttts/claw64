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
	Link    *serial.Link
	LLM     llm.Completer
	History *History
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

	// notify C64 that a chat message arrived
	msgFrame := serial.Frame{Type: serial.FrameMsg, Payload: []byte(text)}
	if err := r.sendWithRetry(msgFrame); err != nil {
		log.Printf("relay: failed to send MSG to C64: %v", err)
	}

	// call LLM with conversation history
	resp, err := r.callLLM(ctx, userID)
	if err != nil {
		return "", err
	}

	// loop: process tool calls and C64 responses
	for i := 0; i < maxIterations; i++ {
		// text response — send to C64 and return to chat
		if len(resp.ToolCalls) == 0 {
			if resp.Content != "" {
				textFrame := serial.Frame{Type: serial.FrameText, Payload: []byte(resp.Content)}
				if err := r.sendWithRetry(textFrame); err != nil {
					log.Printf("relay: failed to send TEXT to C64: %v", err)
				}
			}
			return resp.Content, nil
		}

		// tool call — extract command, send EXEC to C64
		for _, tc := range resp.ToolCalls {
			result, err := r.handleToolCall(ctx, userID, tc)
			if err != nil {
				return "", err
			}

			// append tool result to history
			r.History.Append(userID, llm.Message{
				Role:       "tool",
				Content:    result,
				ToolCallID: tc.ID,
			})
		}

		// call LLM again with updated history
		resp, err = r.callLLM(ctx, userID)
		if err != nil {
			return "", err
		}
	}

	return "", fmt.Errorf("LLM loop exceeded %d iterations", maxIterations)
}

// callLLM sends conversation history to the LLM and appends the response.
func (r *Relay) callLLM(ctx context.Context, userID string) (llm.Message, error) {
	history := r.History.Get(userID)
	msgs := make([]llm.Message, 0, 1+len(history))
	msgs = append(msgs, llm.Message{Role: "system", Content: llm.SystemPrompt})
	msgs = append(msgs, history...)

	resp, err := r.LLM.Complete(ctx, msgs, []llm.Tool{llm.BasicExecTool})
	if err != nil {
		return llm.Message{}, fmt.Errorf("llm: %w", err)
	}

	// store assistant response
	r.History.Append(userID, resp)
	return resp, nil
}

// handleToolCall sends an EXEC frame to the C64 and waits for its response.
func (r *Relay) handleToolCall(ctx context.Context, userID string, tc llm.ToolCall) (string, error) {
	if tc.Function.Name != "basic_exec" {
		return fmt.Sprintf("ERROR: unknown tool %s", tc.Function.Name), nil
	}

	// parse command from arguments
	var args basicExecArgs
	if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
		return fmt.Sprintf("ERROR: parse args: %v", err), nil
	}
	if args.Command == "" {
		return "ERROR: empty command", nil
	}
	log.Printf("relay: EXEC %q", args.Command)

	// send EXEC frame to C64
	execFrame := serial.Frame{Type: serial.FrameExec, Payload: []byte(args.Command)}
	if err := r.sendWithRetry(execFrame); err != nil {
		return "", fmt.Errorf("send EXEC: %w", err)
	}

	// wait for C64 response
	return r.waitForResult(ctx, userID)
}

// waitForResult reads frames from the C64 until a RESULT or ERROR arrives.
// LLM_MSG frames are appended to history and trigger another LLM call.
func (r *Relay) waitForResult(ctx context.Context, userID string) (string, error) {
	for {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}

		f, err := r.Link.Recv()
		if err != nil {
			return "", fmt.Errorf("recv: %w", err)
		}

		switch f.Type {
		case serial.FrameResult:
			return string(f.Payload), nil

		case serial.FrameLLM:
			log.Printf("relay: LLM_MSG from C64: %q", string(f.Payload))
			r.History.Append(userID, llm.Message{Role: "user", Content: string(f.Payload)})

		case serial.FrameError:
			return "ERROR: command timed out on C64", nil

		case serial.FrameHeartbeat:
			continue

		default:
			log.Printf("relay: unexpected frame type 0x%02X, skipping", f.Type)
		}
	}
}

// sendWithRetry sends a frame, retrying up to 3 times with backoff.
func (r *Relay) sendWithRetry(f serial.Frame) error {
	var err error
	for i, backoff := range retryBackoff {
		err = r.Link.Send(f)
		if err == nil {
			return nil
		}
		log.Printf("relay: send attempt %d failed: %v", i+1, err)
		time.Sleep(backoff)
	}
	return err
}

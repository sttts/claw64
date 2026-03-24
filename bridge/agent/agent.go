package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/sttts/claw64/llm"
	"github.com/sttts/claw64/serial"
)

// maxIterations caps the tool-call loop to prevent infinite cycles.
const maxIterations = 10

// retry backoff durations for serial send/recv failures.
var retryBackoff = []time.Duration{500 * time.Millisecond, 1 * time.Second, 2 * time.Second}

// Agent orchestrates the conversation between chat users, the LLM,
// and the C64 serial link.
type Agent struct {
	Link    *serial.Link
	LLM     *llm.Client
	History *History
}

// basicExecArgs is the JSON structure the LLM passes to basic_exec.
type basicExecArgs struct {
	Command string `json:"command"`
}

// HandleMessage processes one user message and returns the final text response.
// It runs the LLM tool-call loop: send history to LLM, execute any tool calls
// on the C64, feed results back, repeat until the LLM responds with text.
func (a *Agent) HandleMessage(ctx context.Context, userID string, text string) (string, error) {
	// append user message
	a.History.Append(userID, llm.Message{Role: "user", Content: text})

	tools := []llm.Tool{llm.BasicExecTool}

	for i := 0; i < maxIterations; i++ {
		// build full message list: system prompt + conversation history
		history := a.History.Get(userID)
		msgs := make([]llm.Message, 0, 1+len(history))
		msgs = append(msgs, llm.Message{Role: "system", Content: llm.SystemPrompt})
		msgs = append(msgs, history...)

		// call LLM
		resp, err := a.LLM.Complete(ctx, msgs, tools)
		if err != nil {
			return "", fmt.Errorf("llm: %w", err)
		}

		// store assistant response in history
		a.History.Append(userID, resp)

		// text response — done
		if len(resp.ToolCalls) == 0 {
			return resp.Content, nil
		}

		// process tool calls sequentially
		for _, tc := range resp.ToolCalls {
			result, err := a.execToolCall(ctx, tc)
			if err != nil {
				result = fmt.Sprintf("ERROR: %v", err)
			}

			// append tool result to history
			a.History.Append(userID, llm.Message{
				Role:       "tool",
				Content:    result,
				ToolCallID: tc.ID,
			})
		}
	}

	return "", fmt.Errorf("tool-call loop exceeded %d iterations", maxIterations)
}

// execToolCall dispatches a single tool call. Only basic_exec is supported.
func (a *Agent) execToolCall(ctx context.Context, tc llm.ToolCall) (string, error) {
	if tc.Function.Name != "basic_exec" {
		return "", fmt.Errorf("unknown tool: %s", tc.Function.Name)
	}

	// parse command from arguments
	var args basicExecArgs
	if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
		return "", fmt.Errorf("parse args: %w", err)
	}
	if args.Command == "" {
		return "", fmt.Errorf("empty command")
	}
	log.Printf("agent: basic_exec %q", args.Command)

	// send EXEC frame with retry
	frame := serial.Frame{Type: serial.FrameExec, Payload: []byte(args.Command)}
	if err := a.sendWithRetry(frame); err != nil {
		return "", fmt.Errorf("send EXEC: %w", err)
	}

	// wait for RESULT or ERROR frame with retry
	resp, err := a.recvWithRetry(ctx)
	if err != nil {
		return "", fmt.Errorf("recv: %w", err)
	}

	if resp.Type == serial.FrameError {
		return "ERROR: command timed out on C64", nil
	}
	return string(resp.Payload), nil
}

// sendWithRetry sends a frame, retrying up to 3 times with backoff.
func (a *Agent) sendWithRetry(f serial.Frame) error {
	var err error
	for i, backoff := range retryBackoff {
		err = a.Link.Send(f)
		if err == nil {
			return nil
		}
		log.Printf("agent: send attempt %d failed: %v", i+1, err)
		time.Sleep(backoff)
	}
	return err
}

// recvWithRetry reads a RESULT or ERROR frame, retrying on transient errors.
// Skips HEARTBEAT frames silently.
func (a *Agent) recvWithRetry(ctx context.Context) (serial.Frame, error) {
	var lastErr error
	for i, backoff := range retryBackoff {
		// inner loop to skip heartbeats
		for {
			f, err := a.Link.Recv()
			if err != nil {
				lastErr = err
				log.Printf("agent: recv attempt %d failed: %v", i+1, err)
				time.Sleep(backoff)
				break
			}

			// skip heartbeats
			if f.Type == serial.FrameHeartbeat {
				log.Printf("agent: heartbeat received, continuing")
				continue
			}
			return f, nil
		}

		// check context cancellation between retries
		if ctx.Err() != nil {
			return serial.Frame{}, ctx.Err()
		}
	}
	return serial.Frame{}, lastErr
}

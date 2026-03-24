// Package llm provides chat completion clients for different LLM backends.
// All backends implement the Completer interface and use shared message types.
package llm

import "context"

// Completer is the interface that LLM backends implement.
type Completer interface {
	Complete(ctx context.Context, messages []Message, tools []Tool) (Message, error)
}

// Message is a single entry in the chat history.
// Works for both OpenAI and Anthropic formats — the client translates.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// ToolCall is a function call requested by the model.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

// FunctionCall holds the name and raw JSON arguments of a tool call.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

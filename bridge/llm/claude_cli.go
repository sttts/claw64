package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"strings"
)

// ClaudeCLIClient uses the `claude` CLI tool for completions.
// This is the most reliable approach — the CLI handles all auth
// (OAuth, API keys, token refresh) transparently.
type ClaudeCLIClient struct {
	Model string // default: claude-sonnet-4-6
}

// NewClaudeCLI creates a CLI-based Anthropic client.
func NewClaudeCLI(model string) *ClaudeCLIClient {
	if model == "" {
		model = "claude-sonnet-4-6"
	}
	return &ClaudeCLIClient{Model: model}
}

// Complete builds a prompt from the message history and tools, calls
// `claude -p`, and parses the response back into our Message format.
func (c *ClaudeCLIClient) Complete(ctx context.Context, messages []Message, tools []Tool) (Message, error) {
	// build the prompt from messages
	var prompt strings.Builder
	for _, m := range messages {
		switch m.Role {
		case "system":
			prompt.WriteString(m.Content)
			prompt.WriteString("\n\n")
		case "user":
			prompt.WriteString("User: ")
			prompt.WriteString(m.Content)
			prompt.WriteString("\n\n")
		case "assistant":
			prompt.WriteString("Assistant: ")
			prompt.WriteString(m.Content)
			prompt.WriteString("\n\n")
		case "tool":
			prompt.WriteString("Tool result: ")
			prompt.WriteString(m.Content)
			prompt.WriteString("\n\n")
		}
	}

	// add tool instructions
	if len(tools) > 0 {
		prompt.WriteString("You have these tools available:\n")
		for _, t := range tools {
			prompt.WriteString(fmt.Sprintf("- %s: %s\n", t.Function.Name, t.Function.Description))
		}
		prompt.WriteString("\nTo use a tool, respond with EXACTLY one of these JSON formats on a single line:\n")
		prompt.WriteString(`{"tool":"basic_exec","command":"YOUR BASIC COMMAND HERE"}`)
		prompt.WriteString("\n")
		prompt.WriteString(`{"tool":"text_screenshot"}`)
		prompt.WriteString("\n\nIf you don't need a tool, just respond with plain text.\n\n")
		prompt.WriteString("Assistant: ")
	}

	log.Printf("     → LLM:  claude -p --model %s", c.Model)

	// call claude CLI
	cmd := exec.CommandContext(ctx, "claude", "-p",
		"--output-format", "text",
		"--model", c.Model,
		"--no-session-persistence",
	)
	cmd.Stdin = strings.NewReader(prompt.String())
	out, err := cmd.Output()
	if err != nil {
		return Message{}, fmt.Errorf("claude cli: %w", err)
	}

	text := strings.TrimSpace(string(out))
	log.Printf("LLM  →    :  %s", strings.ReplaceAll(text, "\n", `\n`))

	// check if response contains a tool call (first line might be JSON)
	firstLine := text
	if i := strings.Index(text, "\n"); i >= 0 {
		firstLine = strings.TrimSpace(text[:i])
	}
	if strings.HasPrefix(firstLine, "{") {
		var tc struct {
			Tool    string `json:"tool"`
			Command string `json:"command"`
		}
		if json.Unmarshal([]byte(firstLine), &tc) == nil {
			switch tc.Tool {
			case "basic_exec":
				if tc.Command == "" {
					break
				}
				return Message{
					Role: "assistant",
					ToolCalls: []ToolCall{{
						ID:   "call_cli_1",
						Type: "function",
						Function: FunctionCall{
							Name:      "basic_exec",
							Arguments: fmt.Sprintf(`{"command":%q}`, tc.Command),
						},
					}},
				}, nil
			case "text_screenshot":
				return Message{
					Role: "assistant",
					ToolCalls: []ToolCall{{
						ID:   "call_cli_1",
						Type: "function",
						Function: FunctionCall{
							Name:      "text_screenshot",
							Arguments: `{}`,
						},
					}},
				}, nil
			}
		}
	}

	return Message{Role: "assistant", Content: text}, nil
}

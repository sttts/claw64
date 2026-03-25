package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// AnthropicClient talks to the Anthropic Messages API with tool use support.
// API key is extracted from macOS Keychain (Claude Code/Desktop stored token).
type AnthropicClient struct {
	URL   string // default: https://api.anthropic.com/v1/messages
	Model string // default: claude-sonnet-4-6

	mu          sync.Mutex
	cachedToken string
}

// NewAnthropic creates an Anthropic client with sensible defaults.
// If apiKey is empty, the token is extracted from macOS Keychain.
func NewAnthropic(apiKey, model string) *AnthropicClient {
	url := "https://api.anthropic.com/v1/messages"
	if model == "" {
		model = "claude-sonnet-4-6"
	}
	return &AnthropicClient{
		URL:         url,
		Model:       model,
		cachedToken: apiKey,
	}
}

// token returns the API key, extracting from Keychain if needed.
func (c *AnthropicClient) token() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cachedToken != "" {
		return c.cachedToken, nil
	}

	// extract from macOS Keychain (Claude Code stores credentials here)
	out, err := exec.Command("security", "find-generic-password", "-s", "Claude Code-credentials", "-w").Output()
	if err != nil {
		return "", fmt.Errorf("keychain lookup failed (is Claude Code installed?): %w", err)
	}
	raw := bytes.TrimSpace(out)

	// Claude Code stores: {"claudeAiOauth":{"accessToken":"sk-ant-...","expiresAt":"..."}}
	var creds struct {
		ClaudeAiOauth struct {
			AccessToken string `json:"accessToken"`
			ExpiresAt   string `json:"expiresAt"`
		} `json:"claudeAiOauth"`
	}
	if json.Unmarshal(raw, &creds) == nil && creds.ClaudeAiOauth.AccessToken != "" {
		if creds.ClaudeAiOauth.ExpiresAt != "" {
			if t, err := time.Parse(time.RFC3339, creds.ClaudeAiOauth.ExpiresAt); err == nil && time.Now().After(t) {
				return "", fmt.Errorf("OAuth token expired at %s", creds.ClaudeAiOauth.ExpiresAt)
			}
		}
		c.cachedToken = creds.ClaudeAiOauth.AccessToken
		return c.cachedToken, nil
	}

	// try flat OAuth format: {"accessToken":"...","expiresAt":"..."}
	var flat struct {
		AccessToken string `json:"accessToken"`
		ExpiresAt   string `json:"expiresAt"`
	}
	if json.Unmarshal(raw, &flat) == nil && flat.AccessToken != "" {
		c.cachedToken = flat.AccessToken
		return c.cachedToken, nil
	}

	// fall back to raw sk-ant-* API key
	tok := string(raw)
	if !strings.HasPrefix(tok, "sk-ant-") {
		return "", fmt.Errorf("unrecognized token format in keychain")
	}
	c.cachedToken = tok
	return c.cachedToken, nil
}

// Anthropic API request types

type anthRequest struct {
	Model     string         `json:"model"`
	MaxTokens int            `json:"max_tokens"`
	System    string         `json:"system,omitempty"`
	Messages  []anthMessage  `json:"messages"`
	Tools     []anthTool     `json:"tools,omitempty"`
}

type anthMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type anthTool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema interface{} `json:"input_schema"`
}

// Anthropic API response types

type anthResponse struct {
	Content  []anthContent `json:"content"`
	StopReason string     `json:"stop_reason"`
	Error    *anthError    `json:"error,omitempty"`
}

type anthContent struct {
	Type  string          `json:"type"`            // "text" or "tool_use"
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`    // tool_use ID
	Name  string          `json:"name,omitempty"`  // tool_use function name
	Input json.RawMessage `json:"input,omitempty"` // tool_use arguments
}

type anthError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// Complete sends messages to the Anthropic Messages API and translates
// the response back to our unified Message format.
func (c *AnthropicClient) Complete(ctx context.Context, messages []Message, tools []Tool) (Message, error) {
	tok, err := c.token()
	if err != nil {
		return Message{}, err
	}

	// extract system prompt (Anthropic uses a top-level field, not a message)
	var system string
	var convMsgs []Message
	for _, m := range messages {
		if m.Role == "system" {
			system = m.Content
		} else {
			convMsgs = append(convMsgs, m)
		}
	}

	// convert messages to Anthropic format
	anthMsgs, err := toAnthMessages(convMsgs)
	if err != nil {
		return Message{}, fmt.Errorf("convert messages: %w", err)
	}

	// convert tools
	var anthTools []anthTool
	for _, t := range tools {
		anthTools = append(anthTools, anthTool{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			InputSchema: t.Function.Parameters,
		})
	}

	reqBody := anthRequest{
		Model:     c.Model,
		MaxTokens: 4096,
		System:    system,
		Messages:  anthMsgs,
		Tools:     anthTools,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return Message{}, fmt.Errorf("marshal: %w", err)
	}
	// debug: log.Printf("anthropic request: %s", string(body))

	req, err := http.NewRequestWithContext(ctx, "POST", c.URL, bytes.NewReader(body))
	if err != nil {
		return Message{}, fmt.Errorf("request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", tok)
	req.Header.Set("Anthropic-Version", "2023-06-01")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Message{}, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return Message{}, fmt.Errorf("read: %w", err)
	}

	// clear cached token on auth failure
	if resp.StatusCode == 401 {
		c.mu.Lock()
		c.cachedToken = ""
		c.mu.Unlock()
		return Message{}, fmt.Errorf("auth failed (401): %s", string(respBody))
	}
	if resp.StatusCode != http.StatusOK {
		return Message{}, fmt.Errorf("status %d: %s", resp.StatusCode, string(respBody))
	}

	var anthResp anthResponse
	if err := json.Unmarshal(respBody, &anthResp); err != nil {
		return Message{}, fmt.Errorf("unmarshal: %w", err)
	}
	if anthResp.Error != nil {
		return Message{}, fmt.Errorf("api error: %s: %s", anthResp.Error.Type, anthResp.Error.Message)
	}

	// translate response back to unified Message
	return fromAnthResponse(anthResp), nil
}

// toAnthMessages converts our unified Messages to Anthropic format.
// Anthropic uses content blocks (array of {type, text} or {type, tool_use_id, content}).
func toAnthMessages(msgs []Message) ([]anthMessage, error) {
	var out []anthMessage
	for _, m := range msgs {
		switch {
		// assistant message with tool calls
		case m.Role == "assistant" && len(m.ToolCalls) > 0:
			var blocks []interface{}
			if m.Content != "" {
				blocks = append(blocks, map[string]string{"type": "text", "text": m.Content})
			}
			for _, tc := range m.ToolCalls {
				var input json.RawMessage
				if tc.Function.Arguments != "" {
					input = json.RawMessage(tc.Function.Arguments)
				} else {
					input = json.RawMessage("{}")
				}
				blocks = append(blocks, map[string]interface{}{
					"type":  "tool_use",
					"id":    tc.ID,
					"name":  tc.Function.Name,
					"input": input,
				})
			}
			raw, err := json.Marshal(blocks)
			if err != nil {
				return nil, err
			}
			out = append(out, anthMessage{Role: "assistant", Content: raw})

		// tool result message
		case m.Role == "tool":
			block := []map[string]string{{
				"type":        "tool_result",
				"tool_use_id": m.ToolCallID,
				"content":     m.Content,
			}}
			raw, err := json.Marshal(block)
			if err != nil {
				return nil, err
			}
			out = append(out, anthMessage{Role: "user", Content: raw})

		// plain text message (user or assistant)
		default:
			raw, err := json.Marshal(m.Content)
			if err != nil {
				return nil, err
			}
			out = append(out, anthMessage{Role: m.Role, Content: raw})
		}
	}
	return out, nil
}

// fromAnthResponse converts Anthropic's content blocks to a unified Message.
func fromAnthResponse(resp anthResponse) Message {
	msg := Message{Role: "assistant"}
	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			if msg.Content != "" {
				msg.Content += "\n"
			}
			msg.Content += block.Text
		case "tool_use":
			args := string(block.Input)
			if args == "" {
				args = "{}"
			}
			msg.ToolCalls = append(msg.ToolCalls, ToolCall{
				ID:   block.ID,
				Type: "function",
				Function: FunctionCall{
					Name:      block.Name,
					Arguments: args,
				},
			})
		}
	}
	return msg
}

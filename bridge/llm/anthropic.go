package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
)

// AnthropicClient talks to the Anthropic Messages API with tool use support.
type AnthropicClient struct {
	URL   string // default: https://api.anthropic.com/v1/messages
	Model string // default: claude-sonnet-4-6

	mu          sync.Mutex
	cachedToken string
}

// NewAnthropic creates an Anthropic client with sensible defaults.
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

// Preflight verifies that Anthropic credentials are available before startup.
func (c *AnthropicClient) Preflight(context.Context) error {
	_, err := c.token()
	return err
}

// token returns the API key from flags, env, or saved config.
func (c *AnthropicClient) token() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cachedToken != "" {
		if err := validateAnthropicAPIKey(c.cachedToken); err != nil {
			return "", err
		}
		return c.cachedToken, nil
	}

	if tok := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")); tok != "" {
		if err := validateAnthropicAPIKey(tok); err != nil {
			return "", err
		}
		c.cachedToken = tok
		return c.cachedToken, nil
	}

	if tok, err := loadAnthropicStoredToken(); err == nil && tok != "" {
		if err := validateAnthropicAPIKey(tok); err != nil {
			return "", err
		}
		c.cachedToken = tok
		return c.cachedToken, nil
	} else if err != nil {
		return "", fmt.Errorf("load stored token: %w", err)
	}

	return "", fmt.Errorf("no Anthropic API key found; pass --llm-key, set ANTHROPIC_API_KEY, or run `claw64-bridge auth set-key`")
}

func validateAnthropicAPIKey(tok string) error {
	switch {
	case tok == "":
		return fmt.Errorf("empty Anthropic API key")
	case strings.HasPrefix(tok, "sk-ant-oat"):
		return fmt.Errorf("Claude subscription tokens are not supported; use a real Anthropic API key")
	case !strings.HasPrefix(tok, "sk-ant-"):
		return fmt.Errorf("invalid Anthropic API key format")
	default:
		return nil
	}
}

// Anthropic API request types

type anthRequest struct {
	Model     string         `json:"model"`
	MaxTokens int            `json:"max_tokens"`
	System    []anthSysBlock `json:"system,omitempty"`
	Messages  []anthMessage  `json:"messages"`
	Tools     []anthTool     `json:"tools,omitempty"`
}

type anthSysBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
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
	Content    []anthContent `json:"content"`
	StopReason string        `json:"stop_reason"`
	Error      *anthError    `json:"error,omitempty"`
}

type anthContent struct {
	Type  string          `json:"type"` // "text" or "tool_use"
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`    // tool_use ID
	Name  string          `json:"name,omitempty"`  // tool_use function name
	Input json.RawMessage `json:"input,omitempty"` // tool_use arguments
}

type anthError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

func (c *AnthropicClient) DescribeRequest(messages []Message, tools []Tool) (string, []byte, error) {
	var system string
	var convMsgs []Message
	for _, m := range messages {
		if m.Role == "system" {
			system = m.Content
		} else {
			convMsgs = append(convMsgs, m)
		}
	}

	anthMsgs, err := toAnthMessages(convMsgs)
	if err != nil {
		return "", nil, err
	}

	var anthTools []anthTool
	for _, t := range tools {
		anthTools = append(anthTools, anthTool{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			InputSchema: t.Function.Parameters,
		})
	}

	body, err := json.Marshal(anthRequest{
		Model:     c.Model,
		MaxTokens: 4096,
		System:    buildSystemBlocks(system),
		Messages:  anthMsgs,
		Tools:     anthTools,
	})
	if err != nil {
		return "", nil, err
	}
	return c.URL, body, nil
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

	systemBlocks := buildSystemBlocks(system)

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
		System:    systemBlocks,
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
	setAnthropicHeaders(req, tok)

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

func buildSystemBlocks(system string) []anthSysBlock {
	if system == "" {
		return nil
	}
	return []anthSysBlock{{Type: "text", Text: system}}
}

func setAnthropicHeaders(req *http.Request, token string) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Anthropic-Version", "2023-06-01")
	req.Header.Set("X-Api-Key", token)
	req.Header.Set("Anthropic-Beta", "fine-grained-tool-streaming-2025-05-14")
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

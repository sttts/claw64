package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// OpenAIClient talks to an OpenAI-compatible chat completions endpoint
// (OpenAI, Ollama, llama.cpp, vLLM, etc.).
type OpenAIClient struct {
	URL    string // e.g. "http://localhost:11434/v1/chat/completions"
	APIKey string
	Model  string

	codex *openAICodexCreds
}

type codexAuth struct {
	OpenAIAPIKey *string `json:"OPENAI_API_KEY"`
}

var ErrOpenAICodexAuthRequired = errors.New("openai codex oauth login required")

// openAI request/response types
type oaiRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Tools    []Tool    `json:"tools,omitempty"`
}

type oaiResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
}

func (c *OpenAIClient) DescribeRequest(messages []Message, tools []Tool) (string, []byte, error) {
	if c.codex != nil && c.APIKey == "" && usesOpenAIPublicAPI(c.URL) {
		body, err := buildOpenAICodexBody(c.Model, messages, tools)
		if err != nil {
			return "", nil, err
		}
		raw, err := json.Marshal(body)
		if err != nil {
			return "", nil, err
		}
		return resolveOpenAICodexURL(), raw, nil
	}

	raw, err := json.Marshal(oaiRequest{
		Model:    c.Model,
		Messages: messages,
		Tools:    tools,
	})
	if err != nil {
		return "", nil, err
	}
	return c.URL, raw, nil
}

// Complete sends the conversation to the OpenAI-compatible endpoint.
func (c *OpenAIClient) Complete(ctx context.Context, messages []Message, tools []Tool) (Message, error) {
	if err := c.Preflight(ctx); err != nil {
		return Message{}, err
	}

	if c.codex != nil && c.APIKey == "" && usesOpenAIPublicAPI(c.URL) {
		return c.completeCodex(ctx, messages, tools)
	}

	body, err := json.Marshal(oaiRequest{
		Model:    c.Model,
		Messages: messages,
		Tools:    tools,
	})
	if err != nil {
		return Message{}, fmt.Errorf("marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.URL, bytes.NewReader(body))
	if err != nil {
		return Message{}, fmt.Errorf("request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Message{}, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return Message{}, fmt.Errorf("read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return Message{}, fmt.Errorf("status %d: %s", resp.StatusCode, string(respBody))
	}

	var result oaiResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return Message{}, fmt.Errorf("unmarshal: %w", err)
	}
	if len(result.Choices) == 0 {
		return Message{}, fmt.Errorf("no choices in response")
	}

	return result.Choices[0].Message, nil
}

// Preflight resolves credentials before the first request.
func (c *OpenAIClient) Preflight(context.Context) error {
	if c.APIKey != "" || !usesOpenAIPublicAPI(c.URL) {
		return nil
	}

	if key, err := loadCodexOpenAIAPIKey(); err != nil {
		return err
	} else if key != "" {
		c.APIKey = key
		return nil
	}

	if key := strings.TrimSpace(os.Getenv("OPENAI_API_KEY")); key != "" {
		c.APIKey = key
		return nil
	}

	creds, err := loadOpenAICodexCreds()
	if err != nil {
		return err
	}
	if creds == nil {
		return ErrOpenAICodexAuthRequired
	}
	c.codex = creds
	if c.Model == "" || c.Model == "gpt-4o" {
		c.Model = "gpt-5.1"
	}
	return nil
}

func loadCodexOpenAIAPIKey() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	raw, err := os.ReadFile(filepath.Join(home, ".codex", "auth.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}

	var auth codexAuth
	if err := json.Unmarshal(raw, &auth); err != nil {
		return "", fmt.Errorf("parse ~/.codex/auth.json: %w", err)
	}
	if auth.OpenAIAPIKey == nil {
		return "", nil
	}

	key := strings.TrimSpace(*auth.OpenAIAPIKey)
	if key == "" {
		return "", nil
	}
	return key, nil
}

func usesOpenAIPublicAPI(url string) bool {
	return strings.Contains(url, "api.openai.com")
}

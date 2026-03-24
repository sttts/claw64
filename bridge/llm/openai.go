package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// OpenAIClient talks to an OpenAI-compatible chat completions endpoint
// (OpenAI, Ollama, llama.cpp, vLLM, etc.).
type OpenAIClient struct {
	URL    string // e.g. "http://localhost:11434/v1/chat/completions"
	APIKey string
	Model  string
}

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

// Complete sends the conversation to the OpenAI-compatible endpoint.
func (c *OpenAIClient) Complete(ctx context.Context, messages []Message, tools []Tool) (Message, error) {
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

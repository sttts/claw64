package llm

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func anthropicTokenPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "claw64", "anthropic-token"), nil
}

func loadAnthropicStoredToken() (string, error) {
	path, err := anthropicTokenPath()
	if err != nil {
		return "", err
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(raw)), nil
}

func saveAnthropicStoredToken(token string) (string, error) {
	path, err := anthropicTokenPath()
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(strings.TrimSpace(token)+"\n"), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// SaveAnthropicToken stores a real Anthropic API key for direct API usage.
func SaveAnthropicToken(token string) (string, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", fmt.Errorf("empty token")
	}
	if !strings.HasPrefix(token, "sk-ant-") || strings.HasPrefix(token, "sk-ant-oat") {
		return "", fmt.Errorf("expected a real Anthropic API key, not a Claude subscription token")
	}
	return saveAnthropicStoredToken(token)
}

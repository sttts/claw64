package llm

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	openAICodexAuthorizeURL = "https://auth.openai.com/oauth/authorize"
	openAICodexTokenURL     = "https://auth.openai.com/oauth/token"
	openAICodexClientID     = "app_EMoamEEZ73f0CkXaXp7hrann"
	openAICodexRedirectURI  = "http://localhost:1455/auth/callback"
	openAICodexScope        = "openid profile email offline_access"
	openAICodexAccountClaim = "https://api.openai.com/auth"
	openAICodexBaseURL      = "https://chatgpt.com/backend-api"
)

type openAICodexCreds struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token,omitempty"`
	AccountID    string `json:"account_id"`
	ExpiresAt    int64  `json:"expires_at"`
}

type codexStoredAuth struct {
	Tokens struct {
		IDToken      string `json:"id_token"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		AccountID    string `json:"account_id"`
	} `json:"tokens"`
}

type codexResponseEnvelope struct {
	Type     string             `json:"type"`
	Response *codexResponseBody `json:"response,omitempty"`
	Item     *codexResponseItem `json:"item,omitempty"`
	Message  string             `json:"message,omitempty"`
	Code     string             `json:"code,omitempty"`
}

type codexResponseBody struct {
	Status string              `json:"status"`
	Error  *codexResponseError `json:"error,omitempty"`
	Output []codexResponseItem `json:"output,omitempty"`
}

type codexResponseError struct {
	Message string `json:"message"`
}

type codexResponseItem struct {
	ID        string                 `json:"id"`
	Type      string                 `json:"type"`
	Status    string                 `json:"status"`
	Role      string                 `json:"role"`
	Name      string                 `json:"name"`
	CallID    string                 `json:"call_id"`
	Arguments string                 `json:"arguments"`
	Content   []codexResponseContent `json:"content"`
}

type codexResponseContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type codexTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

func openAICodexTokenPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "claw64", "openai-codex-oauth.json"), nil
}

func loadOpenAICodexCreds() (*openAICodexCreds, error) {
	if creds, err := loadSavedOpenAICodexCreds(); err != nil {
		return nil, err
	} else if creds != nil {
		return creds, nil
	}
	return loadCodexCLIAuth()
}

func loadSavedOpenAICodexCreds() (*openAICodexCreds, error) {
	path, err := openAICodexTokenPath()
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var creds openAICodexCreds
	if err := json.Unmarshal(raw, &creds); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if creds.AccessToken == "" || creds.RefreshToken == "" {
		return nil, nil
	}
	if creds.AccountID == "" {
		accountID, err := extractCodexAccountID(creds.AccessToken)
		if err != nil {
			return nil, err
		}
		creds.AccountID = accountID
	}
	return &creds, nil
}

func loadCodexCLIAuth() (*openAICodexCreds, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(filepath.Join(home, ".codex", "auth.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var auth codexStoredAuth
	if err := json.Unmarshal(raw, &auth); err != nil {
		return nil, fmt.Errorf("parse ~/.codex/auth.json: %w", err)
	}
	if auth.Tokens.AccessToken == "" || auth.Tokens.RefreshToken == "" {
		return nil, nil
	}
	accountID := strings.TrimSpace(auth.Tokens.AccountID)
	if accountID == "" {
		accountID, err = extractCodexAccountID(auth.Tokens.AccessToken)
		if err != nil {
			return nil, err
		}
	}
	expiresAt, _ := extractJWTExpiry(auth.Tokens.AccessToken)
	return &openAICodexCreds{
		AccessToken:  strings.TrimSpace(auth.Tokens.AccessToken),
		RefreshToken: strings.TrimSpace(auth.Tokens.RefreshToken),
		IDToken:      strings.TrimSpace(auth.Tokens.IDToken),
		AccountID:    accountID,
		ExpiresAt:    expiresAt,
	}, nil
}

func saveOpenAICodexCreds(creds *openAICodexCreds) error {
	path, err := openAICodexTokenPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o600)
}

func CanPromptForOpenAICodexAuth() bool {
	fd := os.Stdin.Fd()
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (info.Mode()&os.ModeCharDevice) != 0 && fd != 0
}

func ConfirmOpenAICodexAuth() (bool, error) {
	fmt.Fprint(os.Stderr, "🤖 Authenticate OpenAI now? [Y/n] ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return false, err
	}
	switch strings.TrimSpace(strings.ToLower(line)) {
	case "", "y", "yes":
		return true, nil
	case "n", "no":
		return false, nil
	default:
		return false, nil
	}
}

func RunOpenAICodexAuth() error {
	verifier, challenge, err := generateOpenAICodexPKCE()
	if err != nil {
		return err
	}
	state, err := randomHex(16)
	if err != nil {
		return err
	}
	authURL := buildOpenAICodexAuthorizeURL(challenge, state)
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	server := &http.Server{Addr: "127.0.0.1:1455"}
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/callback", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != state {
			http.Error(w, "State mismatch.", http.StatusBadRequest)
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "Missing authorization code.", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, "OpenAI authentication completed. You can close this window.")
		select {
		case codeCh <- code:
		default:
		}
	})
	server.Handler = mux

	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	fmt.Fprintf(os.Stderr, "🌐 Open this URL to authenticate OpenAI:\n%s\n", authURL)
	_ = openBrowser(authURL)

	var code string
	select {
	case code = <-codeCh:
	case err := <-errCh:
		return fmt.Errorf("oauth callback server: %w", err)
	}

	_ = server.Shutdown(context.Background())

	creds, err := exchangeOpenAICodexCode(code, verifier)
	if err != nil {
		return err
	}
	return saveOpenAICodexCreds(creds)
}

func (c *OpenAIClient) completeCodex(ctx context.Context, messages []Message, tools []Tool) (Message, error) {
	if c.codex == nil {
		return Message{}, fmt.Errorf("missing Codex OAuth credentials")
	}

	creds, err := ensureFreshOpenAICodexCreds(c.codex)
	if err != nil {
		return Message{}, err
	}
	c.codex = creds

	msg, err := c.completeCodexOnce(ctx, messages, tools, creds)
	if err == nil {
		return msg, nil
	}
	if !strings.Contains(err.Error(), "401") {
		return Message{}, err
	}

	creds, refreshErr := refreshOpenAICodexCreds(creds.RefreshToken)
	if refreshErr != nil {
		return Message{}, refreshErr
	}
	c.codex = creds
	_ = saveOpenAICodexCreds(creds)
	return c.completeCodexOnce(ctx, messages, tools, creds)
}

func (c *OpenAIClient) completeCodexOnce(ctx context.Context, messages []Message, tools []Tool, creds *openAICodexCreds) (Message, error) {
	body, err := buildOpenAICodexBody(c.Model, messages, tools)
	if err != nil {
		return Message{}, err
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return Message{}, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", resolveOpenAICodexURL(), bytes.NewReader(raw))
	if err != nil {
		return Message{}, err
	}
	setOpenAICodexHeaders(req, creds)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Message{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return Message{}, fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return parseOpenAICodexSSE(resp.Body)
}

func buildOpenAICodexBody(model string, messages []Message, tools []Tool) (map[string]any, error) {
	var system string
	var input []any
	msgIndex := 0
	for _, m := range messages {
		switch m.Role {
		case "system":
			system = m.Content
		case "user":
			input = append(input, map[string]any{
				"role": "user",
				"content": []map[string]any{{
					"type": "input_text",
					"text": m.Content,
				}},
			})
		case "assistant":
			if len(m.ToolCalls) > 0 {
				for i, tc := range m.ToolCalls {
					callID, itemID := splitOpenAICodexToolCallID(tc.ID, msgIndex, i)
					input = append(input, map[string]any{
						"type":      "function_call",
						"id":        itemID,
						"call_id":   callID,
						"name":      tc.Function.Name,
						"arguments": tc.Function.Arguments,
					})
				}
			}
			if m.Content != "" {
				input = append(input, map[string]any{
					"type":   "message",
					"role":   "assistant",
					"status": "completed",
					"id":     fmt.Sprintf("msg_%d", msgIndex),
					"content": []map[string]any{{
						"type":        "output_text",
						"text":        m.Content,
						"annotations": []any{},
					}},
				})
			}
		case "tool":
			callID := strings.SplitN(m.ToolCallID, "|", 2)[0]
			input = append(input, map[string]any{
				"type":    "function_call_output",
				"call_id": callID,
				"output":  m.Content,
			})
		}
		msgIndex++
	}

	body := map[string]any{
		"model":               model,
		"store":               false,
		"stream":              true,
		"instructions":        system,
		"input":               input,
		"text":                map[string]any{"verbosity": "medium"},
		"include":             []string{"reasoning.encrypted_content"},
		"tool_choice":         "auto",
		"parallel_tool_calls": true,
	}
	if len(tools) > 0 {
		body["tools"] = convertOpenAICodexTools(tools)
	}
	return body, nil
}

func convertOpenAICodexTools(tools []Tool) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		out = append(out, map[string]any{
			"type":        "function",
			"name":        t.Function.Name,
			"description": t.Function.Description,
			"parameters":  t.Function.Parameters,
			"strict":      false,
		})
	}
	return out
}

func parseOpenAICodexSSE(r io.Reader) (Message, error) {
	scanner := bufio.NewScanner(r)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)
	var dataLines []string

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if len(dataLines) == 0 {
				continue
			}
			data := strings.Join(dataLines, "\n")
			dataLines = nil

			var env codexResponseEnvelope
			if err := json.Unmarshal([]byte(data), &env); err != nil {
				continue
			}
			switch env.Type {
			case "error":
				return Message{}, fmt.Errorf("Codex error: %s", firstNonEmpty(env.Message, env.Code))
			case "response.failed":
				if env.Response != nil && env.Response.Error != nil {
					return Message{}, fmt.Errorf("Codex response failed: %s", env.Response.Error.Message)
				}
				return Message{}, fmt.Errorf("Codex response failed")
			case "response.completed", "response.done", "response.incomplete":
				if env.Response == nil {
					return Message{}, fmt.Errorf("Codex response missing payload")
				}
				return codexResponseToMessage(env.Response), nil
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := scanner.Err(); err != nil {
		return Message{}, err
	}
	return Message{}, fmt.Errorf("Codex stream ended without completion event")
}

func codexResponseToMessage(resp *codexResponseBody) Message {
	msg := Message{Role: "assistant"}
	for _, item := range resp.Output {
		switch item.Type {
		case "message":
			for _, part := range item.Content {
				if part.Type == "output_text" && part.Text != "" {
					if msg.Content != "" {
						msg.Content += "\n"
					}
					msg.Content += part.Text
				}
			}
		case "function_call":
			args := item.Arguments
			if args == "" {
				args = "{}"
			}
			msg.ToolCalls = append(msg.ToolCalls, ToolCall{
				ID:   item.CallID + "|" + item.ID,
				Type: "function",
				Function: FunctionCall{
					Name:      item.Name,
					Arguments: args,
				},
			})
		}
	}
	return msg
}

func resolveOpenAICodexURL() string {
	return openAICodexBaseURL + "/codex/responses"
}

func setOpenAICodexHeaders(req *http.Request, creds *openAICodexCreds) {
	req.Header.Set("Authorization", "Bearer "+creds.AccessToken)
	req.Header.Set("chatgpt-account-id", creds.AccountID)
	req.Header.Set("originator", "pi")
	req.Header.Set("OpenAI-Beta", "responses=experimental")
	req.Header.Set("accept", "text/event-stream")
	req.Header.Set("content-type", "application/json")
	req.Header.Set("User-Agent", fmt.Sprintf("claw64 (%s %s; %s)", runtime.GOOS, runtime.GOARCH, runtime.Version()))
}

func splitOpenAICodexToolCallID(id string, msgIndex, callIndex int) (string, string) {
	if id != "" && strings.Contains(id, "|") {
		parts := strings.SplitN(id, "|", 2)
		return parts[0], parts[1]
	}
	callID := id
	if callID == "" {
		callID = fmt.Sprintf("call_%d_%d", msgIndex, callIndex)
	}
	itemID := fmt.Sprintf("fc_%d_%d", msgIndex, callIndex)
	return callID, itemID
}

func ensureFreshOpenAICodexCreds(creds *openAICodexCreds) (*openAICodexCreds, error) {
	if creds == nil {
		return nil, ErrOpenAICodexAuthRequired
	}
	if creds.AccountID == "" {
		accountID, err := extractCodexAccountID(creds.AccessToken)
		if err != nil {
			return nil, err
		}
		creds.AccountID = accountID
	}
	if creds.ExpiresAt == 0 {
		expiresAt, err := extractJWTExpiry(creds.AccessToken)
		if err != nil {
			return nil, err
		}
		creds.ExpiresAt = expiresAt
	}
	if time.Now().Add(5*time.Minute).UnixMilli() < creds.ExpiresAt {
		return creds, nil
	}
	return refreshOpenAICodexCreds(creds.RefreshToken)
}

func refreshOpenAICodexCreds(refreshToken string) (*openAICodexCreds, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", openAICodexClientID)

	req, err := http.NewRequest("POST", openAICodexTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OpenAI token refresh failed: %s", strings.TrimSpace(string(raw)))
	}

	var tok codexTokenResponse
	if err := json.Unmarshal(raw, &tok); err != nil {
		return nil, err
	}
	accountID, err := extractCodexAccountID(tok.AccessToken)
	if err != nil {
		return nil, err
	}
	creds := &openAICodexCreds{
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		IDToken:      tok.IDToken,
		AccountID:    accountID,
		ExpiresAt:    time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second).UnixMilli(),
	}
	_ = saveOpenAICodexCreds(creds)
	return creds, nil
}

func exchangeOpenAICodexCode(code, verifier string) (*openAICodexCreds, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", openAICodexClientID)
	form.Set("code", code)
	form.Set("code_verifier", verifier)
	form.Set("redirect_uri", openAICodexRedirectURI)

	req, err := http.NewRequest("POST", openAICodexTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OpenAI token exchange failed: %s", strings.TrimSpace(string(raw)))
	}

	var tok codexTokenResponse
	if err := json.Unmarshal(raw, &tok); err != nil {
		return nil, err
	}
	accountID, err := extractCodexAccountID(tok.AccessToken)
	if err != nil {
		return nil, err
	}
	return &openAICodexCreds{
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		IDToken:      tok.IDToken,
		AccountID:    accountID,
		ExpiresAt:    time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second).UnixMilli(),
	}, nil
}

func buildOpenAICodexAuthorizeURL(challenge, state string) string {
	u, _ := url.Parse(openAICodexAuthorizeURL)
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", openAICodexClientID)
	q.Set("redirect_uri", openAICodexRedirectURI)
	q.Set("scope", openAICodexScope)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("state", state)
	q.Set("id_token_add_organizations", "true")
	q.Set("codex_cli_simplified_flow", "true")
	q.Set("originator", "pi")
	u.RawQuery = q.Encode()
	return u.String()
}

func generateOpenAICodexPKCE() (string, string, error) {
	verifier, err := randomBase64URL(32)
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge, nil
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", b), nil
}

func randomBase64URL(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func extractCodexAccountID(token string) (string, error) {
	payload, err := decodeOpenAIJWT(token)
	if err != nil {
		return "", err
	}
	auth, ok := payload[openAICodexAccountClaim].(map[string]any)
	if !ok {
		return "", fmt.Errorf("failed to extract accountId from token")
	}
	accountID, _ := auth["chatgpt_account_id"].(string)
	if accountID == "" {
		return "", fmt.Errorf("failed to extract accountId from token")
	}
	return accountID, nil
}

func extractJWTExpiry(token string) (int64, error) {
	payload, err := decodeOpenAIJWT(token)
	if err != nil {
		return 0, err
	}
	exp, ok := payload["exp"].(float64)
	if !ok || exp == 0 {
		return 0, fmt.Errorf("token missing exp")
	}
	return int64(exp * 1000), nil
}

func decodeOpenAIJWT(token string) (map[string]any, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid token")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func openBrowser(target string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", target).Start()
	case "linux":
		return exec.Command("xdg-open", target).Start()
	default:
		return nil
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

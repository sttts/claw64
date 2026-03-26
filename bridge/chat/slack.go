package chat

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/sttts/slagent"
	slagentchannel "github.com/sttts/slagent/channel"
	slagentclient "github.com/sttts/slagent/client"
	"github.com/sttts/slagent/credential"
)

// SlackChannel implements Channel using a slagent-managed Slack thread.
type SlackChannel struct {
	workspace string
	target    string
	topic     string

	thread *slagent.Thread
}

// NewSlack creates a Slack backend using slagent credentials and a target channel.
func NewSlack(workspace, target, topic string) *SlackChannel {
	return &SlackChannel{
		workspace: workspace,
		target:    target,
		topic:     topic,
	}
}

func (s *SlackChannel) Name() string { return "slack" }

func (s *SlackChannel) Start(ctx context.Context, handler MessageHandler) error {
	sc, resolved, display, ownerID, err := s.connect()
	if err != nil {
		return err
	}

	thread := slagent.NewThread(sc, resolved, slagent.WithOwner(ownerID))
	url, err := thread.Start(s.threadTopic(display))
	if err != nil {
		return fmt.Errorf("slack: start thread: %w", err)
	}
	s.thread = thread

	log.Printf("slack: workspace=%s target=%s thread=%s", workspaceLabel(s.workspace), display, url)

	for {
		messages, err := thread.Replies(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("slack: poll replies: %w", err)
		}

		for _, msg := range messages {
			switch m := msg.(type) {
			case slagent.TextMessage:
				if m.Observe {
					continue
				}
				if err := s.handleSlackMessage(ctx, handler, m.UserID, m.Text); err != nil {
					log.Printf("slack: handler error for %s: %v", m.UserID, err)
				}
			case slagent.CommandMessage:
				if err := s.handleSlackMessage(ctx, handler, m.UserID, m.Command); err != nil {
					log.Printf("slack: command error for %s: %v", m.UserID, err)
				}
			}
		}
	}
}

func (s *SlackChannel) Send(_ context.Context, _ string, text string) error {
	if s.thread == nil {
		return fmt.Errorf("slack: no active thread")
	}
	_, err := s.thread.Post(text)
	return err
}

func (s *SlackChannel) Stop() error { return nil }

func (s *SlackChannel) handleSlackMessage(ctx context.Context, handler MessageHandler, userID, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	reply, err := handler(ctx, userID, text)
	if err != nil {
		if s.thread != nil {
			_, _ = s.thread.Post(fmt.Sprintf("error: %v", err))
		}
		return err
	}
	if reply == "" || s.thread == nil {
		return nil
	}

	_, err = s.thread.Post(reply)
	return err
}

func (s *SlackChannel) connect() (*slagentclient.Client, string, string, string, error) {
	if err := ensureSlackCredentials(s.workspace); err != nil {
		return nil, "", "", "", err
	}

	creds, err := credential.Load(s.workspace)
	if err != nil {
		return nil, "", "", "", fmt.Errorf("slack credentials: %w", err)
	}

	sc := slagentclient.New(creds.EffectiveToken(), creds.Cookie)
	sc.SetEnterprise(creds.Enterprise)

	resolver, err := slagentchannel.New(sc)
	if err != nil {
		return nil, "", "", "", fmt.Errorf("slack resolver: %w", err)
	}

	resolved, display, err := resolveSlackTarget(resolver, s.target)
	if err != nil {
		return nil, "", "", "", err
	}

	auth, err := sc.AuthTest()
	if err != nil {
		return nil, "", "", "", fmt.Errorf("slack auth test: %w", err)
	}

	return sc, resolved, display, auth.UserID, nil
}

func (s *SlackChannel) threadTopic(display string) string {
	if s.topic != "" {
		return s.topic
	}
	return fmt.Sprintf("Claw64 session in %s", display)
}

func ensureSlackCredentials(workspace string) error {
	if _, err := credential.Load(workspace); err == nil {
		return nil
	}

	result, err := credential.Extract()
	if err != nil {
		return fmt.Errorf("slack credentials: %w", err)
	}

	found := workspace == ""
	for _, ws := range result.Workspaces {
		key := workspaceKey(ws.URL)
		if err := credential.Save(key, &credential.Credentials{
			Token:  ws.Token,
			Type:   "session",
			Cookie: result.Cookie,
		}); err != nil {
			return fmt.Errorf("slack credentials: save %s: %w", key, err)
		}
		if workspace != "" && key == workspace {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("slack workspace %q not found in local Slack app", workspace)
	}

	names, defaultName, _ := credential.ListWorkspaces()
	if workspace == "" && defaultName == "" && len(names) == 1 {
		if err := credential.SetDefault(names[0]); err != nil {
			return fmt.Errorf("slack credentials: set default: %w", err)
		}
	}

	return nil
}

func resolveSlackTarget(resolver *slagentchannel.Client, target string) (string, string, error) {
	switch {
	case strings.HasPrefix(target, "@"):
		id, err := resolver.ResolveUserChannel(strings.TrimPrefix(target, "@"))
		return id, target, err
	case isSlackID(target):
		return target, target, nil
	default:
		id, err := resolver.ResolveChannelByName(target)
		display := target
		if !strings.HasPrefix(display, "#") {
			display = "#" + display
		}
		return id, display, err
	}
}

func workspaceKey(url string) string {
	url = strings.TrimPrefix(url, "https://")
	url = strings.TrimPrefix(url, "http://")
	url = strings.TrimSuffix(url, "/")
	return url
}

func workspaceLabel(workspace string) string {
	if workspace == "" {
		return "default"
	}
	return workspace
}

func isSlackID(s string) bool {
	if len(s) < 2 {
		return false
	}
	switch s[0] {
	case 'C', 'D', 'G':
	default:
		return false
	}
	for _, c := range s[1:] {
		if !((c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z')) {
			return false
		}
	}
	return true
}

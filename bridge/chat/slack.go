package chat

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

// SlackChannel implements Channel using Slack Socket Mode.
type SlackChannel struct {
	botToken string
	appToken string
	api      *slack.Client
	sm       *socketmode.Client
	botID    string

	mu      sync.Mutex
	stopped bool
}

// NewSlack creates a new Slack channel backend.
// botToken is a xoxb- bot token; appToken is a xapp- app-level token with
// connections:write scope (required for Socket Mode).
func NewSlack(botToken, appToken string) *SlackChannel {
	api := slack.New(botToken, slack.OptionAppLevelToken(appToken))
	sm := socketmode.New(api)
	return &SlackChannel{
		botToken: botToken,
		appToken: appToken,
		api:      api,
		sm:       sm,
	}
}

func (s *SlackChannel) Name() string { return "slack" }

// Start connects via Socket Mode, dispatches incoming messages to handler,
// and replies in-thread. It blocks until ctx is cancelled.
func (s *SlackChannel) Start(ctx context.Context, handler MessageHandler) error {
	// Fetch our own bot user ID so we can ignore our own messages.
	auth, err := s.api.AuthTest()
	if err != nil {
		return fmt.Errorf("slack auth test: %w", err)
	}
	s.botID = auth.UserID
	botMention := fmt.Sprintf("<@%s>", s.botID)

	// Spawn a goroutine to process Socket Mode events.
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case evt, ok := <-s.sm.Events:
				if !ok {
					return
				}
				s.handleEvent(ctx, evt, handler, botMention)
			}
		}
	}()

	// Run blocks until the connection is closed or ctx expires.
	return s.sm.RunContext(ctx)
}

// handleEvent processes a single Socket Mode event.
func (s *SlackChannel) handleEvent(ctx context.Context, evt socketmode.Event, handler MessageHandler, botMention string) {
	if evt.Type != socketmode.EventTypeEventsAPI {
		return
	}

	// Acknowledge the event immediately.
	s.sm.Ack(*evt.Request)

	payload, ok := evt.Data.(slackevents.EventsAPIEvent)
	if !ok {
		return
	}
	if payload.Type != slackevents.CallbackEvent {
		return
	}

	// We only care about message events.
	inner, ok := payload.InnerEvent.Data.(*slackevents.MessageEvent)
	if !ok || inner == nil {
		return
	}

	// Ignore messages from bots (including ourselves).
	if inner.BotID != "" || inner.User == s.botID {
		return
	}

	// Only respond to DMs or messages that mention the bot.
	isDM := strings.HasPrefix(inner.Channel, "D")
	isMention := strings.Contains(inner.Text, botMention)
	if !isDM && !isMention {
		return
	}

	// Strip the bot mention from the text before passing to the handler.
	text := strings.ReplaceAll(inner.Text, botMention, "")
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}

	// Call the agent handler.
	reply, err := handler(ctx, inner.User, text)
	if err != nil {
		log.Printf("slack: handler error for user %s: %v", inner.User, err)
		reply = fmt.Sprintf("error: %v", err)
	}

	// Reply in-thread (use the message timestamp as thread root).
	threadTS := inner.TimeStamp
	if inner.ThreadTimeStamp != "" {
		threadTS = inner.ThreadTimeStamp
	}
	_, _, err = s.api.PostMessage(inner.Channel,
		slack.MsgOptionText(reply, false),
		slack.MsgOptionTS(threadTS),
	)
	if err != nil {
		log.Printf("slack: failed to post reply: %v", err)
	}
}

// Send posts a message to a channel or user.
func (s *SlackChannel) Send(ctx context.Context, user string, text string) error {
	_, _, err := s.api.PostMessageContext(ctx, user, slack.MsgOptionText(text, false))
	return err
}

// Stop disconnects the Socket Mode client.
func (s *SlackChannel) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return nil
	}
	s.stopped = true
	return nil
}

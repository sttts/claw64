// Package chat defines the pluggable chat channel interface and the
// message handler callback that connects chat backends to the agent.
package chat

import "context"

// MessageHandler is called when a chat message arrives.
// It receives the user identifier and message text, and returns the
// response text to send back (or an error).
type MessageHandler func(ctx context.Context, userID, text string) (string, error)

// Channel is the interface that chat backends implement.
type Channel interface {
	// Name returns a human-readable backend name (e.g. "slack", "whatsapp").
	Name() string

	// Start connects to the chat platform and begins dispatching incoming
	// messages to handler. It blocks until ctx is cancelled or a fatal
	// error occurs.
	Start(ctx context.Context, handler MessageHandler) error

	// Send pushes a message to a specific user/channel. Used for
	// unsolicited messages (e.g. C64 heartbeat notifications).
	Send(ctx context.Context, user string, text string) error

	// Stop disconnects from the platform and releases resources.
	Stop() error
}

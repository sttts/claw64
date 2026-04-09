package chat

import (
	"context"
	"fmt"
	"log"
	"sync"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"

	_ "modernc.org/sqlite"
)

// WhatsAppChannel implements Channel using the whatsmeow multi-device API.
type WhatsAppChannel struct {
	client  *whatsmeow.Client
	store   *sqlstore.Container
	handler MessageHandler
	target  string
	mu      sync.Mutex
}

// NewWhatsApp creates a new WhatsApp channel with SQLite session persistence.
func NewWhatsApp(dbPath, target string) (*WhatsAppChannel, error) {
	// Open the SQLite device store.
	container, err := sqlstore.New(context.Background(), "sqlite", fmt.Sprintf("file:%s?_pragma=foreign_keys(1)", dbPath), waLog.Noop)
	if err != nil {
		return nil, fmt.Errorf("whatsapp: open device store: %w", err)
	}

	// Get the first device or create a new one.
	device, err := container.GetFirstDevice(context.Background())
	if err != nil {
		return nil, fmt.Errorf("whatsapp: get device: %w", err)
	}

	client := whatsmeow.NewClient(device, waLog.Noop)

	return &WhatsAppChannel{
		client: client,
		store:  container,
		target: target,
	}, nil
}

func (w *WhatsAppChannel) Name() string {
	return "whatsapp"
}

// Start connects to WhatsApp, showing a QR code if not yet logged in,
// and dispatches incoming text messages to the handler.
func (w *WhatsAppChannel) Start(ctx context.Context, handler MessageHandler) error {
	w.handler = handler

	// Register event handler before connecting.
	w.client.AddEventHandler(w.onEvent)

	// If not logged in, do QR-based pairing.
	if w.client.Store.ID == nil {
		qrChan, _ := w.client.GetQRChannel(ctx)
		if err := w.client.Connect(); err != nil {
			return fmt.Errorf("whatsapp: connect: %w", err)
		}
		for evt := range qrChan {
			switch evt.Event {
			case "code":
				fmt.Println("Scan this QR code with WhatsApp:")
				fmt.Println(evt.Code)
			case "success":
				log.Println("whatsapp: QR login successful")
			case "timeout":
				return fmt.Errorf("whatsapp: QR code timed out")
			}
		}
	} else {
		// Already logged in, just connect.
		if err := w.client.Connect(); err != nil {
			return fmt.Errorf("whatsapp: connect: %w", err)
		}
	}

	if w.client.Store.ID != nil {
		log.Printf("whatsapp: ready as %s target=%s trigger=%q", w.client.Store.ID.String(), w.target, "🕹️")
	} else {
		log.Printf("whatsapp: ready target=%s trigger=%q", w.target, "🕹️")
	}

	// Block until the context is cancelled.
	<-ctx.Done()
	return ctx.Err()
}

// Send sends a text message to the given JID string.
func (w *WhatsAppChannel) Send(ctx context.Context, user string, text string) error {
	jid, err := types.ParseJID(user)
	if err != nil {
		return fmt.Errorf("whatsapp: parse JID %q: %w", user, err)
	}

	_, err = w.client.SendMessage(ctx, jid, &waE2E.Message{
		Conversation: &text,
	})
	if err != nil {
		return fmt.Errorf("whatsapp: send message: %w", err)
	}
	return nil
}

// Stop disconnects the WhatsApp client.
func (w *WhatsAppChannel) Stop() error {
	w.client.Disconnect()
	return nil
}

// onEvent handles incoming whatsmeow events.
func (w *WhatsAppChannel) onEvent(evt interface{}) {
	msg, ok := evt.(*events.Message)
	if !ok {
		return
	}

	// Ignore messages from self.
	if msg.Info.IsFromMe {
		return
	}

	chatJID := msg.Info.Chat.String()
	if chatJID != w.target {
		return
	}

	// Only handle plain text messages.
	text := msg.Message.GetConversation()
	if text == "" {
		// Also check ExtendedTextMessage (messages with link previews etc).
		if ext := msg.Message.GetExtendedTextMessage(); ext != nil {
			text = ext.GetText()
		}
	}
	var triggered bool
	text, triggered = stripJoystickTrigger(text)
	if !triggered {
		return
	}

	sender := chatJID

	// Serialize handler calls to avoid concurrent access issues.
	w.mu.Lock()
	defer w.mu.Unlock()

	err := w.handler(context.Background(), sender, text)
	if err != nil {
		log.Printf("whatsapp: handler error for %s: %v", sender, err)
		return
	}
}

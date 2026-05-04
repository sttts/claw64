package chat

import (
	"testing"

	"go.mau.fi/whatsmeow/types"
)

func TestWhatsAppTypingState(t *testing.T) {
	if got := whatsappTypingState(true); got != types.ChatPresenceComposing {
		t.Fatalf("active typing state = %q, want %q", got, types.ChatPresenceComposing)
	}
	if got := whatsappTypingState(false); got != types.ChatPresencePaused {
		t.Fatalf("inactive typing state = %q, want %q", got, types.ChatPresencePaused)
	}
}

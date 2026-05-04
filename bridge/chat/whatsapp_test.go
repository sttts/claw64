package chat

import (
	"testing"

	"go.mau.fi/whatsmeow/types"
)

func TestWhatsAppPrivateTargetDoesNotRequireJoystickTrigger(t *testing.T) {
	ch := &WhatsAppChannel{target: "491701234567@s.whatsapp.net"}

	text, ok := ch.incomingText("hallo claw")
	if !ok {
		t.Fatal("private WhatsApp message without trigger was ignored")
	}
	if text != "hallo claw" {
		t.Fatalf("private WhatsApp text = %q, want %q", text, "hallo claw")
	}
}

func TestWhatsAppGroupTargetRequiresJoystickTrigger(t *testing.T) {
	ch := &WhatsAppChannel{target: "120363123456789012@g.us"}

	if _, ok := ch.incomingText("hallo group"); ok {
		t.Fatal("group WhatsApp message without trigger was accepted")
	}

	text, ok := ch.incomingText("🕹️ hallo group")
	if !ok {
		t.Fatal("group WhatsApp message with trigger was ignored")
	}
	if text != "hallo group" {
		t.Fatalf("group WhatsApp text = %q, want %q", text, "hallo group")
	}
}

func TestIsWhatsAppGroupJID(t *testing.T) {
	if !isWhatsAppGroupJID("120363123456789012@g.us") {
		t.Fatal("group JID was not recognized as group")
	}
	if isWhatsAppGroupJID("491701234567@s.whatsapp.net") {
		t.Fatal("private JID was recognized as group")
	}
}

func TestWhatsAppTypingState(t *testing.T) {
	if got := whatsappTypingState(true); got != types.ChatPresenceComposing {
		t.Fatalf("active typing state = %q, want %q", got, types.ChatPresenceComposing)
	}
	if got := whatsappTypingState(false); got != types.ChatPresencePaused {
		t.Fatalf("inactive typing state = %q, want %q", got, types.ChatPresencePaused)
	}
}

package chat

import "testing"

func TestSignalPrivateTargetDoesNotRequireJoystickTrigger(t *testing.T) {
	ch := NewSignal("+491700000000", "", "user:+491711111111")

	text, ok := ch.incomingText("hello claw")
	if !ok {
		t.Fatal("private signal message without trigger was ignored")
	}
	if text != "hello claw" {
		t.Fatalf("private signal text = %q, want %q", text, "hello claw")
	}
}

func TestSignalGroupTargetRequiresJoystickTrigger(t *testing.T) {
	ch := NewSignal("+491700000000", "", "group:abc")

	if _, ok := ch.incomingText("hello group"); ok {
		t.Fatal("group signal message without trigger was accepted")
	}

	text, ok := ch.incomingText("🕹️: hello group")
	if !ok {
		t.Fatal("group signal message with trigger was ignored")
	}
	if text != "hello group" {
		t.Fatalf("group signal text = %q, want %q", text, "hello group")
	}
}

func TestSignalTriggerLogValue(t *testing.T) {
	private := NewSignal("+491700000000", "", "user:+491711111111")
	if got := private.triggerLogValue(); got != "none" {
		t.Fatalf("private trigger log = %q, want none", got)
	}

	group := NewSignal("+491700000000", "", "group:abc")
	if got := group.triggerLogValue(); got != `"🕹️"` {
		t.Fatalf("group trigger log = %q, want quoted joystick", got)
	}
}

func TestFormatSignalMessageDoesNotAddJoystickQuote(t *testing.T) {
	got := formatSignalMessage(" HALLO! BEREIT. \n")
	if got != "HALLO! BEREIT." {
		t.Fatalf("signal message = %q, want plain trimmed text", got)
	}
}

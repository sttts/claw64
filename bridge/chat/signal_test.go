package chat

import (
	"reflect"
	"testing"
)

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

func TestSignalTypingArgsForUser(t *testing.T) {
	ch := NewSignal("+491700000000", ".signal-cli", "user:+491711111111")

	got := ch.signalTypingArgs("user:+491711111111", true)
	want := []string{"--config", ".signal-cli", "--output", "json", "--account", "+491700000000", "sendTyping", "+491711111111"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("typing start args = %#v, want %#v", got, want)
	}

	got = ch.signalTypingArgs("user:+491711111111", false)
	want = []string{"--config", ".signal-cli", "--output", "json", "--account", "+491700000000", "sendTyping", "--stop", "+491711111111"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("typing stop args = %#v, want %#v", got, want)
	}
}

func TestSignalTypingArgsForGroup(t *testing.T) {
	ch := NewSignal("+491700000000", "", "group:abc")

	got := ch.signalTypingArgs("group:abc", true)
	want := []string{"--output", "json", "--account", "+491700000000", "sendTyping", "--group-id", "abc"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("group typing start args = %#v, want %#v", got, want)
	}

	got = ch.signalTypingArgs("group:abc", false)
	want = []string{"--output", "json", "--account", "+491700000000", "sendTyping", "--stop", "--group-id", "abc"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("group typing stop args = %#v, want %#v", got, want)
	}
}

func TestParseSignalLine(t *testing.T) {
	line := `{"envelope":{"sourceNumber":"+491711111111","dataMessage":{"message":" hello "}}}`

	evt, ok := parseSignalLine(line)
	if !ok {
		t.Fatal("signal line was ignored")
	}
	if evt.userID != "user:+491711111111" {
		t.Fatalf("userID = %q", evt.userID)
	}
	if evt.text != "hello" {
		t.Fatalf("text = %q", evt.text)
	}
}

func TestParseSignalLineUsesGroupTarget(t *testing.T) {
	line := `{"envelope":{"sourceNumber":"+491711111111","dataMessage":{"message":" 🕹️: hello group ","groupInfo":{"groupId":"abc","type":"DELIVER"}}}}`

	evt, ok := parseSignalLine(line)
	if !ok {
		t.Fatal("signal group line was ignored")
	}
	if evt.userID != "group:abc" {
		t.Fatalf("userID = %q", evt.userID)
	}
	if evt.text != "🕹️: hello group" {
		t.Fatalf("text = %q", evt.text)
	}
}

func TestParseSignalLineIgnoresNonMessages(t *testing.T) {
	cases := []string{
		``,
		`{"envelope":{"syncMessage":{}}}`,
		`{"envelope":{"sourceNumber":"+491711111111","dataMessage":{"message":"   "}}}`,
	}

	for _, line := range cases {
		if evt, ok := parseSignalLine(line); ok {
			t.Fatalf("line %q parsed as %#v", line, evt)
		}
	}
}

func TestParseSignalLineJSONRPCReceive(t *testing.T) {
	line := `{"jsonrpc":"2.0","method":"receive","params":{"envelope":{"sourceNumber":"+491711111111","dataMessage":{"message":" hello rpc "}}}}`

	evt, ok := parseSignalLine(line)
	if !ok {
		t.Fatal("signal jsonrpc receive line was ignored")
	}
	if evt.userID != "user:+491711111111" {
		t.Fatalf("userID = %q", evt.userID)
	}
	if evt.text != "hello rpc" {
		t.Fatalf("text = %q", evt.text)
	}
}

func TestParseSignalLineJSONRPCReceiveSubscription(t *testing.T) {
	line := `{"jsonrpc":"2.0","method":"receive","params":{"subscription":0,"result":{"envelope":{"sourceNumber":"+491711111111","dataMessage":{"message":" hello sub ","groupInfo":{"groupId":"abc","type":"DELIVER"}}}}}}`

	evt, ok := parseSignalLine(line)
	if !ok {
		t.Fatal("signal jsonrpc subscription receive line was ignored")
	}
	if evt.userID != "group:abc" {
		t.Fatalf("userID = %q", evt.userID)
	}
	if evt.text != "hello sub" {
		t.Fatalf("text = %q", evt.text)
	}
}

func TestSignalRPCParamsForUserAndGroup(t *testing.T) {
	msg := signalMessageParams("user:+491711111111", " hello ")
	if got := msg["message"]; got != "hello" {
		t.Fatalf("message param = %#v", got)
	}
	if !reflect.DeepEqual(msg["recipient"], []string{"+491711111111"}) {
		t.Fatalf("recipient param = %#v", msg["recipient"])
	}

	groupTyping := signalTypingParams("group:abc", false)
	if got := groupTyping["groupId"]; got != "abc" {
		t.Fatalf("groupId param = %#v", got)
	}
	if got := groupTyping["stop"]; got != true {
		t.Fatalf("stop param = %#v", got)
	}
}

func TestDecodeJSONRPCID(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want string
	}{
		{`"42"`, "42"},
		{`42`, "42"},
	} {
		got, ok := decodeJSONRPCID([]byte(tc.raw))
		if !ok {
			t.Fatalf("id %s was not decoded", tc.raw)
		}
		if got != tc.want {
			t.Fatalf("id %s = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

package chat

import "strings"

const joystickTrigger = "🕹️"
const slackJoystickTrigger = ":joystick:"

func stripJoystickTrigger(text string) (string, bool) {
	if strings.HasPrefix(text, joystickTrigger+" ") {
		text = strings.TrimSpace(strings.TrimPrefix(text, joystickTrigger+" "))
		if text == "" {
			return "", false
		}
		return text, true
	}
	if strings.HasPrefix(text, joystickTrigger+":") {
		text = strings.TrimSpace(strings.TrimPrefix(text, joystickTrigger+":"))
		if text == "" {
			return "", false
		}
		return text, true
	}
	return "", false
}

func stripSlackJoystickTrigger(text string) (string, bool) {
	if strings.HasPrefix(text, slackJoystickTrigger+" ") {
		text = strings.TrimSpace(strings.TrimPrefix(text, slackJoystickTrigger+" "))
		if text == "" {
			return "", false
		}
		return text, true
	}
	if strings.HasPrefix(text, slackJoystickTrigger+":") {
		text = strings.TrimSpace(strings.TrimPrefix(text, slackJoystickTrigger+":"))
		if text == "" {
			return "", false
		}
		return text, true
	}
	return "", false
}

func formatSlackJoystickQuote(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}

	lines := []string{"> " + slackJoystickTrigger}
	for _, line := range strings.Split(text, "\n") {
		lines = append(lines, "> "+line)
	}
	return strings.Join(lines, "\n")
}

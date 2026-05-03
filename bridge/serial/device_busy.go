package serial

import (
	"fmt"
	"os/exec"
	"strings"
)

func serialDeviceBusyHint(path string) string {
	out, err := exec.Command("lsof", "-n", "-P", path).CombinedOutput()
	if err != nil && len(out) == 0 {
		return fmt.Sprintf("; another process is using it; try: lsof -n -P %s", path)
	}

	text := strings.TrimSpace(string(out))
	if text == "" {
		return fmt.Sprintf("; another process is using it; try: lsof -n -P %s", path)
	}
	return "\n" + text
}

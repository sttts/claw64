//go:build !darwin && !linux

package serial

import (
	"fmt"
	"os"
)

func openDevice(path string) (*os.File, error) {
	return nil, fmt.Errorf("serial ports are not supported on this platform")
}

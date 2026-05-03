package serial

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func openDevice(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_NOCTTY|unix.O_NONBLOCK, 0)
	if err != nil {
		if errors.Is(err, unix.EBUSY) {
			return nil, fmt.Errorf("open serial port %s: %w%s", path, err, serialDeviceBusyHint(path))
		}
		return nil, fmt.Errorf("open serial port %s: %w", path, err)
	}
	if err := unix.SetNonblock(fd, false); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("set serial port blocking mode %s: %w", path, err)
	}
	if err := configureDevice(fd); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("configure serial port %s: %w", path, err)
	}
	return os.NewFile(uintptr(fd), path), nil
}

func configureDevice(fd int) error {
	t, err := unix.IoctlGetTermios(fd, unix.TIOCGETA)
	if err != nil {
		return err
	}

	// Raw 2400 8N1, no software or hardware flow control.
	t.Iflag &^= unix.BRKINT | unix.ICRNL | unix.INPCK | unix.ISTRIP | unix.IXON | unix.IXOFF
	t.Oflag &^= unix.OPOST
	t.Lflag &^= unix.ECHO | unix.ICANON | unix.IEXTEN | unix.ISIG
	t.Cflag &^= unix.CSIZE | unix.PARENB | unix.CSTOPB | unix.CRTSCTS
	t.Cflag |= unix.CS8 | unix.CREAD | unix.CLOCAL
	t.Cc[unix.VMIN] = 1
	t.Cc[unix.VTIME] = 0
	t.Ispeed = unix.B2400
	t.Ospeed = unix.B2400
	if err := unix.IoctlSetTermios(fd, unix.TIOCSETAF, t); err != nil {
		return err
	}
	return unix.IoctlSetPointerInt(fd, unix.TIOCFLUSH, unix.TCIOFLUSH)
}

package termstyle

import (
	"io"
	"os"

	"github.com/mattn/go-isatty"
)

const (
	reset = "\033[0m"
	dim   = "\033[90m"
	bold  = "\033[1m"
	cyan  = "\033[96m"
	green = "\033[92m"
	yellow = "\033[93m"
	red   = "\033[91m"
)

func enabled(file *os.File) bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	fd := file.Fd()
	return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
}

func wrap(file *os.File, style, text string) string {
	if !enabled(file) {
		return text
	}
	return style + text + reset
}

func Dim(text string) string {
	return wrap(os.Stderr, dim, text)
}

func Warn(text string) string {
	return wrap(os.Stderr, yellow, text)
}

func Error(text string) string {
	return wrap(os.Stderr, red, text)
}

func UserPrompt(text string) string {
	return wrap(os.Stdout, bold+cyan, text)
}

func C64Prompt(text string) string {
	return wrap(os.Stdout, bold+green, text)
}

type dimWriter struct {
	w io.Writer
}

func (d dimWriter) Write(p []byte) (int, error) {
	text := string(p)
	styled := Dim(text)
	n, err := io.WriteString(d.w, styled)
	if n > len(styled) {
		n = len(styled)
	}
	if len(styled) != 0 {
		n = len(p)
	}
	return n, err
}

func DimWriter(w io.Writer) io.Writer {
	return dimWriter{w: w}
}

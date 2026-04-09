package chat

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/sttts/claw64/bridge/termstyle"
)

var ErrInterrupted = errors.New("stdin interrupted")

var ansiSeqRE = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

// StdinChannel is a bubbletea-based terminal chat backend.
type StdinChannel struct {
	mu      sync.Mutex
	program *tea.Program
	model   *tuiModel
	early   []string
}

func NewStdin() *StdinChannel { return &StdinChannel{} }

func (s *StdinChannel) Name() string { return "stdin" }

func (s *StdinChannel) LogWriter() io.Writer    { return &tuiLogWriter{ch: s} }
func (s *StdinChannel) StreamWriter() io.Writer { return &tuiStreamWriter{ch: s} }

func (s *StdinChannel) sendLine(line string) {
	line = stripANSI(line)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.program != nil {
		s.program.Send(logLineMsg(line))
	} else {
		s.early = append(s.early, line)
	}
}

func (s *StdinChannel) sendStream(text string) {
	text = stripANSI(text)
	if text == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.program != nil {
		s.program.Send(streamMsg(text))
	}
}

func (s *StdinChannel) commitStream() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.program != nil {
		s.program.Send(streamCommitMsg{})
	}
}

func (s *StdinChannel) Start(ctx context.Context, handler MessageHandler) error {
	s.model = newTuiModel(ctx, handler)
	s.mu.Lock()
	s.program = tea.NewProgram(s.model, tea.WithContext(ctx))
	s.model.program = s.program
	s.model.earlyLines = s.early
	s.early = nil
	s.mu.Unlock()

	_, err := s.program.Run()
	if s.model.interrupted {
		return ErrInterrupted
	}
	return err
}

func (s *StdinChannel) Send(_ context.Context, _, text string) error {
	s.mu.Lock()
	p := s.program
	s.mu.Unlock()
	if p != nil {
		p.Send(c64ReplyMsg(text))
	}
	return nil
}

func (s *StdinChannel) Stop() error {
	s.mu.Lock()
	p := s.program
	s.mu.Unlock()
	if p != nil {
		p.Quit()
	}
	return nil
}

// --- messages ---

type c64ReplyMsg string
type logLineMsg string
type streamMsg string
type streamCommitMsg struct{}
type settledMsg []consoleLine
type flushScrollbackMsg struct{}
type flushEarlyMsg struct{}
type redrawMsg struct{}
type handlerDoneMsg struct {
	err error
}

type consoleLine struct {
	text string
	dim  bool
}

type consoleOutput interface {
	Print(text string) tea.Cmd
	Log(text string) tea.Cmd
	Logln(line string) tea.Cmd
	Println(line string) tea.Cmd
}

// --- TUI model ---
//
// The view shows the prompt line and, when present, one active live-updating
// stream line above it:
//   stream — active live-updating line above the prompt
//   prompt — input line (always)
//
// Settled output is emitted through tea.Println into scrollback. Only the live
// stream stays in the view so the prompt remains anchored on the bottom line.

type tuiModel struct {
	ctx     context.Context
	handler MessageHandler
	program *tea.Program

	input       textinput.Model
	settled     consoleLine
	stream      consoleLine
	earlyLines  []string // buffered before program start
	ready       bool
	scrollback  []string
	width       int
	interrupted bool
	busy        bool

	mu       sync.Mutex
	sigCount int
	lastSig  time.Time
}

func newTuiModel(ctx context.Context, handler MessageHandler) *tuiModel {
	ti := textinput.New()
	ti.Prompt = "\033[1;96myou>\033[0m "
	ti.Focus()
	ti.CharLimit = 0
	return &tuiModel{ctx: ctx, handler: handler, input: ti}
}

// --- helpers ---

type consoleOps struct {
	model *tuiModel
}

func (o consoleOps) Print(text string) tea.Cmd {
	lines := o.model.printStream(text, false)
	if len(lines) == 0 {
		return nil
	}
	return func() tea.Msg { return settledMsg(lines) }
}

func (o consoleOps) Log(text string) tea.Cmd {
	lines := o.model.printStream(text, true)
	if len(lines) == 0 {
		return nil
	}
	return func() tea.Msg { return settledMsg(lines) }
}

func (o consoleOps) Logln(line string) tea.Cmd {
	lines := o.model.printLog(line)
	if len(lines) == 0 {
		return nil
	}
	return func() tea.Msg { return settledMsg(lines) }
}

func (o consoleOps) Println(line string) tea.Cmd {
	if line == "" {
		return nil
	}
	return func() tea.Msg { return settledMsg([]consoleLine{{text: line}}) }
}

func (m *tuiModel) console() consoleOutput {
	return consoleOps{model: m}
}

func stripANSI(s string) string {
	if s == "" {
		return s
	}
	return ansiSeqRE.ReplaceAllString(s, "")
}

// printStream appends text to the live line. Scrollback only happens on
// explicit newline, never on terminal width.
func (m *tuiModel) printStream(s string, dim bool) []consoleLine {
	var lines []consoleLine
	for _, r := range s {
		if r == '\n' {
			if m.stream.text != "" {
				lines = append(lines, m.stream)
				m.stream = consoleLine{}
			}
			continue
		}

		m.stream.text += string(r)
		m.stream.dim = dim
	}
	return lines
}

func (m *tuiModel) printLog(line string) []consoleLine {
	if line != "" {
		return []consoleLine{{text: line, dim: true}}
	}
	return nil
}

func (m *tuiModel) commitStream() []consoleLine {
	if m.stream.text == "" {
		return nil
	}
	lines := []consoleLine{m.stream}
	m.stream = consoleLine{}
	return lines
}

func (m *tuiModel) wrappedLines(lines []consoleLine) []consoleLine {
	out := make([]consoleLine, 0, len(lines))
	for _, line := range lines {
		if line.text == "" {
			continue
		}

		text := line.text
		if m.width > 0 {
			text = xansi.Hardwrap(text, m.width, true)
		}

		for _, part := range strings.Split(text, "\n") {
			if part != "" {
				out = append(out, consoleLine{text: part, dim: line.dim})
			}
		}
	}
	return out
}

func (m *tuiModel) applySettled(lines []consoleLine) tea.Cmd {
	wrapped := m.wrappedLines(lines)
	if len(wrapped) == 0 {
		return nil
	}

	if m.settled.text != "" {
		text := m.settled.text
		if m.settled.dim {
			text = termstyle.Dim(text)
		}
		m.scrollback = append(m.scrollback, text)
		m.settled = consoleLine{}
	}

	for _, line := range wrapped[:max(0, len(wrapped)-1)] {
		text := line.text
		if line.dim {
			text = termstyle.Dim(text)
		}
		m.scrollback = append(m.scrollback, text)
	}
	m.settled = wrapped[len(wrapped)-1]

	return func() tea.Msg { return flushScrollbackMsg{} }
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (m *tuiModel) flushScrollback() tea.Cmd {
	if len(m.scrollback) == 0 {
		return nil
	}

	out := m.scrollback[0]
	m.scrollback = m.scrollback[1:]
	return tea.Sequence(
		tea.Println(out),
		func() tea.Msg {
			if len(m.scrollback) > 0 {
				return flushScrollbackMsg{}
			}
			return redrawMsg{}
		},
	)
}

// --- Init / Update / View ---

func (m *tuiModel) Init() tea.Cmd {
	return m.input.Focus()
}

func (m *tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		if !m.ready {
			m.ready = true
			return m, func() tea.Msg { return flushEarlyMsg{} }
		}
		return m, nil

	case flushScrollbackMsg:
		return m, m.flushScrollback()

	case redrawMsg:
		return m, nil

	case flushEarlyMsg:
		var lines []consoleLine
		for _, line := range m.earlyLines {
			if xansi.StringWidth(line) > 0 {
				lines = append(lines, consoleLine{text: line, dim: true})
			}
		}
		m.earlyLines = nil
		if len(lines) == 0 {
			return m, nil
		}
		return m, func() tea.Msg { return settledMsg(lines) }

	case settledMsg:
		return m, m.applySettled([]consoleLine(msg))

	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c":
			return m.handleCtrlC()
		case "enter":
			return m.handleEnter()
		}
		m.mu.Lock()
		m.sigCount = 0
		m.mu.Unlock()
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd

	case c64ReplyMsg:
		line := fmt.Sprintf("\033[1;92mc64>\033[0m %s", string(msg))
		return m, func() tea.Msg { return settledMsg([]consoleLine{{text: line}}) }

	case logLineMsg:
		return m, m.console().Logln(string(msg))

	case streamMsg:
		return m, m.console().Log(string(msg))

	case streamCommitMsg:
		lines := m.commitStream()
		if len(lines) == 0 {
			return m, nil
		}
		return m, func() tea.Msg { return settledMsg(lines) }

	case handlerDoneMsg:
		m.busy = false
		if msg.err != nil {
			return m, func() tea.Msg {
				return settledMsg([]consoleLine{{text: fmt.Sprintf("\033[91merror: %v\033[0m", msg.err)}})
			}
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *tuiModel) View() tea.View {
	displayLine := m.stream
	if displayLine.text == "" {
		displayLine = m.settled
	}
	if displayLine.text == "" {
		return tea.NewView(m.input.View())
	}

	display := strings.ReplaceAll(strings.ReplaceAll(displayLine.text, "\r", ""), "\n", "")
	if m.width > 0 {
		display = xansi.Truncate(display, m.width, "")
	}
	if displayLine.dim {
		display = termstyle.Dim(display)
	}
	return tea.NewView(display + "\n" + m.input.View())
}

func (m *tuiModel) handleCtrlC() (tea.Model, tea.Cmd) {
	m.mu.Lock()
	if time.Since(m.lastSig) > 1*time.Second {
		m.sigCount = 0
	}
	m.sigCount++
	m.lastSig = time.Now()
	count := m.sigCount
	m.mu.Unlock()

	if count >= 2 {
		m.interrupted = true
		return m, tea.Batch(m.console().Println("bye."), tea.Quit)
	}
	m.input.SetValue("")
	return m, nil
}

func (m *tuiModel) handleEnter() (tea.Model, tea.Cmd) {
	text := m.input.Value()
	m.input.SetValue("")
	if text == "" {
		return m, nil
	}

	echo := fmt.Sprintf("\033[1;96myou>\033[0m %s", text)
	m.busy = true
	return m, tea.Batch(
		func() tea.Msg { return settledMsg([]consoleLine{{text: echo}}) },
		func() tea.Msg {
			err := m.handler(m.ctx, "local", text)
			if err != nil {
				return handlerDoneMsg{err: err}
			}
			return handlerDoneMsg{}
		},
	)
}

// --- writers ---

type tuiLogWriter struct {
	ch  *StdinChannel
	mu  sync.Mutex
	buf []byte
}

func (w *tuiLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	for {
		idx := -1
		for i, b := range w.buf {
			if b == '\n' {
				idx = i
				break
			}
		}
		if idx < 0 {
			break
		}
		line := strings.TrimRight(string(w.buf[:idx]), "\r")
		w.buf = w.buf[idx+1:]
		w.ch.sendLine(line)
	}
	return len(p), nil
}

type tuiStreamWriter struct {
	ch *StdinChannel
	mu sync.Mutex
}

func (w *tuiStreamWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	s := string(p)
	for {
		idx := strings.IndexByte(s, '\n')
		if idx < 0 {
			if s != "" {
				w.ch.sendStream(s)
			}
			break
		}
		if idx > 0 {
			w.ch.sendStream(s[:idx])
		}
		w.ch.commitStream()
		s = s[idx+1:]
	}
	return len(p), nil
}

package serial

import "strings"

var asciiReplacer = strings.NewReplacer(
	"\r\n", "\n",
	"\r", "\n",
	"’", "'",
	"‘", "'",
	"“", "\"",
	"”", "\"",
	"–", "-",
	"—", "-",
	"…", "...",
	"\u00a0", " ",
)

// ToASCII converts text to a conservative 7-bit transport form for the C64 link.
func ToASCII(s string) string {
	s = asciiReplacer.Replace(s)

	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r >= 0x20 && r <= 0x7e:
			b.WriteRune(r)
		default:
			b.WriteByte('?')
		}
	}
	return b.String()
}

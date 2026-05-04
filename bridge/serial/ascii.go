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
	"ä", "ae",
	"ö", "oe",
	"ü", "ue",
	"Ä", "Ae",
	"Ö", "Oe",
	"Ü", "Ue",
	"ß", "ss",
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

// ToC64Text converts model reply text to a stricter transport-safe form
// for the C64 text path. CHROUT on the KERNAL RS232 path does not preserve
// arbitrary ASCII reliably, so keep replies uppercase and punctuation-light.
func ToC64Text(s string) string {
	s = strings.ToUpper(ToASCII(s))

	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case strings.ContainsRune(" .,:;!?()'\"+-=*/", r):
			b.WriteRune(r)
		case r == '`':
			b.WriteByte('\'')
		case r == '[' || r == '{':
			b.WriteByte('(')
		case r == ']' || r == '}':
			b.WriteByte(')')
		case r == '\\':
			b.WriteByte('/')
		default:
			b.WriteByte(' ')
		}
	}
	return b.String()
}

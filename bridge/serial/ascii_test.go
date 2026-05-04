package serial

import "testing"

func TestToASCIITransliteratesGermanUmlauts(t *testing.T) {
	got := ToASCII("Hintergrund ist grün. Straße: ÄÖÜ äöü ß")
	want := "Hintergrund ist gruen. Strasse: AeOeUe aeoeue ss"
	if got != want {
		t.Fatalf("ToASCII = %q, want %q", got, want)
	}
}

func TestToC64TextTransliteratesBeforeUppercase(t *testing.T) {
	got := ToC64Text("Hintergrund ist grün.")
	want := "HINTERGRUND IST GRUEN."
	if got != want {
		t.Fatalf("ToC64Text = %q, want %q", got, want)
	}
}

package filekind

import (
	"strings"
	"testing"
)

func TestExt(t *testing.T) {
	ok := []string{"a.pdf", "A.PDF", "b.docx", "c.xlsx", "d.jpg", "e.png", "f.zip", "g.DjVu"}
	for _, name := range ok {
		if _, err := Ext(name); err != nil {
			t.Errorf("Ext(%q) = %v, want allowed", name, err)
		}
	}
	bad := []string{"x.html", "y.svg", "z.js", "noext", "w.exe", "v.php", "u.HTM", "t.html.pdf.exe"}
	for _, name := range bad {
		if _, err := Ext(name); err == nil {
			t.Errorf("Ext(%q) = allowed, want rejected", name)
		}
	}
}

func TestInlineSafe(t *testing.T) {
	if !InlineSafe(".pdf") || !InlineSafe(".PNG") {
		t.Error("pdf/png must be inline safe")
	}
	for _, ext := range []string{".html", ".svg", ".docx", ".zip", ""} {
		if InlineSafe(ext) {
			t.Errorf("%q must not be inline safe", ext)
		}
	}
}

func TestSniffHeader(t *testing.T) {
	pdf := []byte("%PDF-1.4 test content")
	jpg := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00}
	png := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}
	gif := []byte("GIF89a....")
	webp := append([]byte("RIFF\x00\x00\x00\x00"), []byte("WEBP")...)
	html := []byte("<html><script>alert(1)</script>")

	cases := []struct {
		name string
		head []byte
		ext  string
		ok   bool
	}{
		{"pdf", pdf, ".pdf", true},
		{"pdf-caps", []byte("%PDF-1.7"), ".PDF", true},
		{"html-as-pdf", html, ".pdf", false},
		{"jpg", jpg, ".jpg", true},
		{"html-as-jpg", html, ".jpeg", false},
		{"png", png, ".png", true},
		{"gif", gif, ".gif", true},
		{"webp", webp, ".webp", true},
		{"gif-as-webp", gif, ".webp", false},
		{"docx-not-sniffed", []byte("PK\x03\x04"), ".docx", true},
		{"any-txt", html, ".txt", true},
		{"empty-pdf", nil, ".pdf", false},
	}
	for _, c := range cases {
		err := SniffHeader(c.head, c.ext)
		if c.ok && err != nil {
			t.Errorf("%s: SniffHeader(%q, %s) = %v, want ok", c.name, string(c.head), c.ext, err)
		}
		if !c.ok && err == nil {
			t.Errorf("%s: SniffHeader(%q, %s) = ok, want rejection", c.name, string(c.head), c.ext)
		}
	}
}

func TestStringsNonEmpty(t *testing.T) {
	list := Strings()
	if len(list) < 15 {
		t.Errorf("Strings() = %v, expected the full whitelist", list)
	}
	for _, ext := range list {
		if !strings.HasPrefix(ext, ".") || !Allowed(ext) {
			t.Errorf("unexpected entry %q", ext)
		}
	}
}

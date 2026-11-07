package pdf

import (
	"context"
	"strings"
	"testing"

	"github.com/arham09/html2pdf/internal/font"
	"github.com/arham09/html2pdf/internal/session"
)

func TestEncodeFontKeepsSourceText(t *testing.T) {
	s, err := session.New(context.Background(), session.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	_, face, err := font.NewRegistry(s, nil).Resolve("Noto Sans", 400, false)
	if err != nil {
		t.Fatal(err)
	}
	glyph := func(text string) font.Glyph {
		t.Helper()
		r, err := face.Shape(text, 14)
		if err != nil {
			t.Fatal(err)
		}
		for _, g := range r.Glyphs {
			if g.Unicode == text {
				return g
			}
		}
		t.Fatalf("no single glyph for %q: %+v", text, r.Glyphs)
		return font.Glyph{}
	}
	composed, decomposed := glyph("é"), glyph("é")
	if composed.ID != decomposed.ID {
		t.Skip("font shapes the two forms to different glyphs")
	}
	runs := []font.Run{{Glyphs: []font.Glyph{composed}}, {Glyphs: []font.Glyph{decomposed}}}
	encoded, err := encodeFont(face, runs)
	if err != nil {
		t.Fatal(err)
	}
	a, b := encoded.codes[codeKey{composed.ID, "é"}], encoded.codes[codeKey{decomposed.ID, "é"}]
	if a == 0 || b == 0 || a == b {
		t.Fatalf("source text lost: codes %d and %d", a, b)
	}
	cmap := string(encoded.dictionary.ToUnicode.Content)
	if !strings.Contains(cmap, "<00E9>") || !strings.Contains(cmap, "<00650301>") {
		t.Fatal("ToUnicode map lost a source form")
	}
}

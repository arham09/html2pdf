package font

import (
	"bytes"
	"math"
	"reflect"
	"testing"

	gotext "github.com/go-text/typesetting/font"
)

func TestRealShaping(t *testing.T) {
	f, err := Parse(bundled)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("ligature", func(t *testing.T) {
		r, err := f.Shape("ffi", 14)
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Glyphs) != 1 || r.Glyphs[0].Unicode != "ffi" {
			t.Fatalf("ligature not exercised: %+v", r)
		}
	})
	t.Run("kerning", func(t *testing.T) {
		r, err := f.Shape("AV", 14)
		if err != nil {
			t.Fatal(err)
		}
		var nominal float64
		for _, g := range r.Glyphs {
			nominal += f.Width(g.ID) * 14 / 1000
		}
		if nominal-r.AdvancePt < 0.1 {
			t.Fatalf("expected real kerning: nominal=%f shaped=%f", nominal, r.AdvancePt)
		}
	})
	t.Run("positioned mark", func(t *testing.T) {
		r, err := f.Shape("q\u0301", 14)
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Glyphs) != 2 {
			t.Fatalf("expected 2 glyphs: %+v", r)
		}
		mark := r.Glyphs[1]
		if mark.XOffsetPt == 0 && mark.YOffsetPt == 0 {
			t.Fatal("fixture did not exercise mark offsets")
		}
		if mark.AdvancePt != 0 || mark.Unicode != "\u0301" {
			t.Fatalf("incorrect mark: %+v", mark)
		}
	})
	t.Run("source identity", func(t *testing.T) {
		a, err := f.Shape("é", 14)
		if err != nil {
			t.Fatal(err)
		}
		b, err := f.Shape("e\u0301", 14)
		if err != nil {
			t.Fatal(err)
		}
		if len(a.Glyphs) != 1 || len(b.Glyphs) != 1 || a.Glyphs[0].ID != b.Glyphs[0].ID || a.Glyphs[0].Unicode == b.Glyphs[0].Unicode {
			t.Fatalf("expected same outline with distinct source: %+v %+v", a, b)
		}
	})
}

func TestSubsetPreservesOutlinesAndAdvances(t *testing.T) {
	f, err := Parse(bundled)
	if err != nil {
		t.Fatal(err)
	}
	r, err := f.Shape("Rp 1.250.000,00 café e\u0301 q\u0301 office", 14)
	if err != nil {
		t.Fatal(err)
	}
	var ids []uint16
	for _, g := range r.Glyphs {
		ids = append(ids, g.ID)
	}
	sub, err := f.Subset(ids)
	if err != nil {
		t.Fatal(err)
	}
	if len(sub.Bytes) >= len(f.data) || sub.SubsetGlyphs >= sub.OriginalGlyphs {
		t.Fatal("font was not subset")
	}
	parsed, err := gotext.ParseTTF(bytes.NewReader(sub.Bytes))
	if err != nil {
		t.Fatal(err)
	}
	for original, mapped := range sub.GlyphIDs {
		// The subsetter intentionally empties .notdef. Shape rejects glyph 0;
		// compare outlines for every glyph that can actually be painted.
		if original == 0 {
			continue
		}
		a, aok := f.face.GlyphDataOutline(gotext.GID(original))
		b, bok := parsed.GlyphDataOutline(gotext.GID(mapped))
		if aok != bok || !reflect.DeepEqual(a, b) {
			t.Errorf("outline mismatch original=%d subset=%d", original, mapped)
		}
		if f.face.HorizontalAdvance(gotext.GID(original)) != parsed.HorizontalAdvance(gotext.GID(mapped)) {
			t.Errorf("advance mismatch for %d", original)
		}
	}
	second, err := f.Subset(ids)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(sub.Bytes, second.Bytes) {
		t.Fatal("subset output is nondeterministic")
	}
}

func TestInvalidInputs(t *testing.T) {
	for _, data := range [][]byte{nil, []byte("not a font"), make([]byte, 32)} {
		if _, err := Parse(data); err == nil {
			t.Fatal("accepted invalid font")
		}
	}
	f, err := Parse(bundled)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"\xff", "\n", "中文", "\U0001F680"} {
		if _, err := f.Shape(text, 14); err == nil {
			t.Errorf("accepted unsupported text %q", text)
		}
	}
	for _, size := range []float64{0, -1, math.NaN(), math.Inf(1), 201} {
		if _, err := f.Shape("x", size); err == nil {
			t.Errorf("accepted invalid size %f", size)
		}
	}
	if _, err := f.Subset([]uint16{65535}); err == nil {
		t.Fatal("accepted invalid glyph ID")
	}
}

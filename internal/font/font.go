// Package font provides request-local static TrueType selection and Latin shaping/subsetting.
// It is not a general font registry or an untrusted-font security boundary.
package font

import (
	"bytes"
	_ "embed"
	"fmt"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/go-text/typesetting/di"
	gotext "github.com/go-text/typesetting/font"
	"github.com/go-text/typesetting/language"
	"github.com/go-text/typesetting/shaping"
	sfnt "github.com/tdewolff/font"
	"golang.org/x/image/math/fixed"
)

//go:embed assets/NotoSans-Regular.ttf
var bundled []byte

//go:embed assets/NotoSans-Bold.ttf
var bold []byte

//go:embed assets/NotoSans-Italic.ttf
var italic []byte

//go:embed assets/NotoSans-BoldItalic.ttf
var boldItalic []byte

// Font owns parsed static TrueType data. Create one per render: the face and
// shaper caches are mutable and not locked.
type Font struct {
	face   *gotext.Face
	shaper shaping.HarfbuzzShaper
	sfnt   *sfnt.SFNT
	data   []byte
}

// Glyph preserves a shaped position and its source cluster (rune indices).
type Glyph struct {
	InkLeftPt, InkTopPt, InkWidthPt, InkHeightPt float64
	ID                                           uint16
	AdvancePt, XOffsetPt, YOffsetPt              float64
	TextStart, TextEnd                           int
	Unicode                                      string
}

// Run is a single horizontal, left-to-right Latin run in PDF points.
type Run struct {
	AscentPt, DescentPt float64
	Text                string
	SizePt              float64
	Glyphs              []Glyph
	AdvancePt           float64
}

// Metrics uses PDF's 1000-unit glyph coordinate system.
type Metrics struct {
	BBox                                    [4]float64
	Ascent, Descent, CapHeight, ItalicAngle float64
}

// Parse only accepts static TrueType outlines. No font discovery or I/O occurs.
func Parse(data []byte) (*Font, error) {
	if len(data) < 12 || len(data) > 8<<20 {
		return nil, fmt.Errorf("font: invalid byte length %d", len(data))
	}
	data = bytes.Clone(data)
	parsed, err := sfnt.ParseSFNT(data, 0)
	if err != nil {
		return nil, fmt.Errorf("font: parse TrueType: %w", err)
	}
	if !parsed.IsTrueType || parsed.UnitsPerEm() == 0 || parsed.NumGlyphs() == 0 {
		return nil, fmt.Errorf("font: expected static TrueType outlines")
	}
	if _, variable := parsed.Tables["fvar"]; variable {
		return nil, fmt.Errorf("font: variable fonts are unsupported")
	}
	if parsed.OS2 == nil || parsed.OS2.FsType&(0x0002|0x0100|0x0200) != 0 {
		return nil, fmt.Errorf("font: embedding/subsetting permissions are unsupported")
	}
	face, err := gotext.ParseTTF(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("font: parse shaping face: %w", err)
	}
	return &Font{face: face, sfnt: parsed, data: data}, nil
}

// Name returns a sanitized PostScript identity, not a caller family alias.
func (f *Font) Name() string {
	if f.sfnt.Name != nil {
		for _, record := range f.sfnt.Name.Get(6) {
			if record.Platform != 0 && record.Platform != 3 {
				continue
			}
			raw := record.String()
			out := strings.Builder{}
			for _, r := range raw {
				if r > 32 && r < 127 && !strings.ContainsRune("()<>[]{}/%", r) {
					out.WriteRune(r)
				}
			}
			if out.Len() > 0 {
				return out.String()
			}
		}
	}
	return "EmbeddedFont"
}

func (f *Font) Metrics() Metrics {
	scale := 1000 / float64(f.sfnt.UnitsPerEm())
	h := f.sfnt.Head
	return Metrics{
		BBox:        [4]float64{float64(h.XMin) * scale, float64(h.YMin) * scale, float64(h.XMax) * scale, float64(h.YMax) * scale},
		Ascent:      float64(f.sfnt.Hhea.Ascender) * scale,
		Descent:     float64(f.sfnt.Hhea.Descender) * scale,
		CapHeight:   float64(f.sfnt.OS2.SCapHeight) * scale,
		ItalicAngle: f.sfnt.Post.ItalicAngle,
	}
}

func (f *Font) Width(id uint16) float64 {
	return float64(f.sfnt.Hmtx.Advance(id)) * 1000 / float64(f.sfnt.UnitsPerEm())
}

func (f *Font) Shape(text string, sizePt float64) (Run, error) {
	if !utf8.ValidString(text) || len(text) > 1<<16 {
		return Run{}, fmt.Errorf("font: invalid or oversized UTF-8 run")
	}
	if math.IsNaN(sizePt) || math.IsInf(sizePt, 0) || sizePt < 1 || sizePt > 200 {
		return Run{}, fmt.Errorf("font: size must be between 1 and 200 pt")
	}
	runes := []rune(text)
	for _, r := range runes {
		if unicode.IsControl(r) || !(unicode.In(r, unicode.Latin, unicode.Common, unicode.Inherited)) {
			return Run{}, fmt.Errorf("font: unsupported character U+%04X", r)
		}
		if _, ok := f.face.NominalGlyph(r); !ok {
			return Run{}, fmt.Errorf("font: missing glyph U+%04X", r)
		}
	}
	// Reuse one face and shaper for all runs of this render. A new face per
	// run allocated most of the render's memory.
	out := f.shaper.Shape(shaping.Input{
		Text: runes, RunEnd: len(runes), Face: f.face,
		Direction: di.DirectionLTR, Script: language.Latin, Language: language.NewLanguage("id"),
		Size: fixed.Int26_6(math.Round(sizePt * 64)),
	})
	result := Run{Text: text, SizePt: float64(out.Size) / 64}
	metrics := f.Metrics()
	result.AscentPt = metrics.Ascent * result.SizePt / 1000
	result.DescentPt = -metrics.Descent * result.SizePt / 1000
	for i := 0; i < len(out.Glyphs); {
		g := out.Glyphs[i]
		start, end := g.TextIndex(), g.TextIndex()+g.RunesCount()
		j := i + 1
		for j < len(out.Glyphs) && out.Glyphs[j].TextIndex() == start {
			j++
		}
		if start < 0 || end > len(runes) || start >= end {
			return Run{}, fmt.Errorf("font: invalid shaping cluster")
		}
		// Accept many-to-one ligatures/compositions and one-to-one combining
		// clusters. General many-to-many script mapping is unsupported.
		if j-i != 1 && j-i != end-start {
			return Run{}, fmt.Errorf("font: unsupported many-to-many shaping cluster")
		}
		for k := i; k < j; k++ {
			g = out.Glyphs[k]
			if g.GlyphID == 0 || g.GlyphID > math.MaxUint16 {
				return Run{}, fmt.Errorf("font: missing or invalid shaped glyph")
			}
			mapped := string(runes[start:end])
			if j-i > 1 {
				mapped = string(runes[start+k-i : start+k-i+1])
			}
			glyph := Glyph{ID: uint16(g.GlyphID), AdvancePt: float64(g.Advance) / 64,
				InkLeftPt: float64(g.XBearing+g.XOffset) / 64, InkTopPt: float64(g.YBearing+g.YOffset) / 64, InkWidthPt: float64(g.Width) / 64, InkHeightPt: -float64(g.Height) / 64,
				XOffsetPt: float64(g.XOffset) / 64, YOffsetPt: float64(g.YOffset) / 64,
				TextStart: start, TextEnd: end, Unicode: mapped}
			result.Glyphs = append(result.Glyphs, glyph)
			result.AscentPt = math.Max(result.AscentPt, glyph.InkTopPt)
			result.DescentPt = math.Max(result.DescentPt, glyph.InkHeightPt-glyph.InkTopPt)
			result.AdvancePt += glyph.AdvancePt
		}
		i = j
	}
	return result, nil
}

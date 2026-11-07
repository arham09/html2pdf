package pdf

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"strings"
	"unicode/utf16"

	"github.com/arham09/html2pdf/internal/font"
	"github.com/arham09/html2pdf/internal/session"
	"github.com/benoitkugler/pdf/model"
)

// codeKey keeps the source text of each glyph. One glyph shaped from
// different text, such as "é" and "é", gets one code per source text.
type codeKey struct {
	gid  uint16
	text string
}

// subsetTag returns the six-letter PDF subset prefix for a font program.
func subsetTag(data []byte) string {
	h := sha256.Sum256(data)
	tag := make([]byte, 6)
	for i := range tag {
		tag[i] = 'A' + h[i]%26
	}
	return string(tag)
}

func utf16Hex(text string) string {
	var s strings.Builder
	for _, u := range utf16.Encode([]rune(text)) {
		fmt.Fprintf(&s, "%04X", u)
	}
	return s.String()
}

func toUnicode(entries []codeKey) string {
	var s strings.Builder
	s.WriteString("/CIDInit /ProcSet findresource begin\n12 dict begin\nbegincmap\n/CIDSystemInfo << /Registry (Adobe) /Ordering (UCS) /Supplement 0 >> def\n/CMapName /Adobe-Identity-UCS def\n/CMapType 2 def\n1 begincodespacerange\n<0000> <FFFF>\nendcodespacerange\n")
	for start := 0; start < len(entries); start += 100 {
		end := min(start+100, len(entries))
		fmt.Fprintf(&s, "%d beginbfchar\n", end-start)
		for i := start; i < end; i++ {
			fmt.Fprintf(&s, "<%04X> <%s>\n", i+1, utf16Hex(entries[i].text))
		}
		s.WriteString("endbfchar\n")
	}
	s.WriteString("endcmap\nCMapName currentdict /CMap defineresource pop\nend\nend\n")
	return s.String()
}

type encodedFont struct {
	dictionary *model.FontDict
	codes      map[codeKey]uint16
}

func encodeFont(face *font.Font, runs []font.Run) (encodedFont, error) {
	var ids []uint16
	entries := []codeKey{}
	codes := map[codeKey]uint16{}
	for _, run := range runs {
		for _, g := range run.Glyphs {
			ids = append(ids, g.ID)
			key := codeKey{g.ID, g.Unicode}
			if _, ok := codes[key]; !ok {
				if len(entries) >= 65534 {
					return encodedFont{}, fmt.Errorf("%w: too many font character codes", session.ErrLimitExceeded)
				}
				entries = append(entries, key)
				codes[key] = uint16(len(entries))
			}
		}
	}
	sub, err := face.Subset(ids)
	if err != nil {
		return encodedFont{}, err
	}
	mapping := make([]byte, 2*(len(entries)+1))
	widths := make([]model.Fl, len(entries))
	for i, k := range entries {
		binary.BigEndian.PutUint16(mapping[2*(i+1):], sub.GlyphIDs[k.gid])
		widths[i] = model.Fl(face.Width(k.gid))
	}
	name := subsetTag(sub.Bytes) + "+" + face.Name()
	m := face.Metrics()
	flags := model.Nonsymbolic
	if m.ItalicAngle != 0 {
		flags |= model.Italic
	}
	dictionary := &model.FontDict{Subtype: model.FontType0{
		BaseFont: model.Name(name), Encoding: model.CMapEncodingPredefined("Identity-H"),
		DescendantFonts: model.CIDFontDictionary{Subtype: "CIDFontType2", BaseFont: model.Name(name), CIDSystemInfo: model.CIDSystemInfo{Registry: "Adobe", Ordering: "Identity"},
			FontDescriptor: model.FontDescriptor{FontName: model.Name(name), Flags: flags, FontBBox: model.Rectangle{Llx: model.Fl(m.BBox[0]), Lly: model.Fl(m.BBox[1]), Urx: model.Fl(m.BBox[2]), Ury: model.Fl(m.BBox[3])}, Ascent: model.Fl(m.Ascent), Descent: model.Fl(m.Descent), CapHeight: model.Fl(m.CapHeight), ItalicAngle: model.Fl(m.ItalicAngle), StemV: 80, FontFile: &model.FontFile{Stream: model.NewCompressedStream(sub.Bytes), Length1: len(sub.Bytes)}},
			W:              []model.CIDWidth{model.CIDWidthArray{Start: 1, W: widths}}, CIDToGIDMap: model.CIDToGIDMapStream{Stream: model.NewCompressedStream(mapping)},
		}}, ToUnicode: &model.UnicodeCMap{Stream: model.Stream{Content: []byte(toUnicode(entries))}}}
	return encodedFont{dictionary, codes}, nil
}

// Package pdf serializes paginated display operations into a deterministic
// PDF with subset Type0 TrueType fonts and RGB images.
package pdf

import (
	"fmt"
	"image"
	"io"
	"sort"
	"strings"

	"github.com/arham09/html2pdf/internal/font"
	"github.com/arham09/html2pdf/internal/paint"
	"github.com/arham09/html2pdf/internal/session"
	"github.com/benoitkugler/pdf/model"
)

type contentBuffer struct {
	strings.Builder
	limit int64
	err   error
}

func (b *contentBuffer) Write(p []byte) (int, error) {
	if b.err != nil {
		return 0, b.err
	}
	if int64(len(p)) > b.limit-int64(b.Len()) {
		b.err = &session.LimitError{Resource: "output_bytes", Limit: b.limit, Used: int64(b.Len()), Requested: int64(len(p))}
		return 0, b.err
	}
	return b.Builder.Write(p)
}

// WritePages serializes pre-positioned display operations; it never wraps text.
// It prepares every page with document-wide font/image resources before the
// first caller output write.
func WritePages(s *session.Session, dst io.Writer, pages []paint.Page) error {
	if len(pages) == 0 {
		return fmt.Errorf("%w: PDF needs a page", session.ErrInvalidInput)
	}
	fonts := map[string]*font.Font{}
	images := map[string]image.Image{}
	for _, page := range pages {
		for id, face := range page.Fonts {
			fonts[id] = face
		}
		for id, img := range page.Images {
			images[id] = img
		}
	}
	if err := s.Check(); err != nil {
		return err
	}
	encodedImages := map[string]*model.XObjectImage{}
	runsByFont := map[string][]font.Run{}
	for _, page := range pages {
		for _, op := range page.Operations {
			if err := s.Charge("layout_work", 1); err != nil {
				return err
			}
			if op.Kind == paint.DrawGlyphRun {
				runsByFont[op.FontID] = append(runsByFont[op.FontID], op.Glyphs)
			}
		}
	}
	encoded := map[string]encodedFont{}
	fontNames := map[string]model.Name{}
	keys := []string{}
	for key := range fonts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for i, key := range keys {
		if err := s.Charge("layout_work", 1); err != nil {
			return err
		}
		f, err := encodeFont(fonts[key], runsByFont[key])
		if err != nil {
			return fmt.Errorf("pdf font: %w", err)
		}
		name := model.Name(fmt.Sprintf("F%d", i))

		encoded[key] = f
		fontNames[key] = name
	}
	imageNames := map[string]model.Name{}
	keys = nil
	for key := range images {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for i, key := range keys {
		obj, err := imageObject(s, images[key])
		if err != nil {
			return err
		}
		name := model.Name(fmt.Sprintf("I%d", i))
		encodedImages[key] = obj
		imageNames[key] = name
	}
	contents := make([]string, 0, len(pages))
	// Content is measured before compression. This bounds memory; the final
	// compressed size is checked again when the objects are written.
	remaining := s.Limits.MaxOutputBytes
	for _, page := range pages {
		content := &contentBuffer{limit: remaining}
		// Formatting errors are latched by contentBuffer and checked before commit.
		for _, op := range page.Operations {
			if err := s.Charge("layout_work", 1); err != nil {
				return err
			}
			b := op.Bounds
			switch op.Kind {
			case paint.FillRect:
				if op.Color.A == 0 {
					continue
				}
				fmt.Fprintf(content, "%.6f %.6f %.6f rg %.6f %.6f %.6f %.6f re f\n", float64(op.Color.R)/255, float64(op.Color.G)/255, float64(op.Color.B)/255, b.X, page.HeightPt-b.Y-b.Height, b.Width, b.Height)
			case paint.DrawGlyphRun:
				f, ok := encoded[op.FontID]
				if !ok {
					return fmt.Errorf("%w: missing display font", session.ErrInvalidInput)
				}
				mode := 0
				if op.Color.A == 0 {
					mode = 3
				}
				fmt.Fprintf(content, "BT /%s %.6f Tf %d Tr %.6f %.6f %.6f rg\n", string(fontNames[op.FontID]), op.Glyphs.SizePt, mode, float64(op.Color.R)/255, float64(op.Color.G)/255, float64(op.Color.B)/255)
				pen := b.X
				runes := []rune(op.Glyphs.Text)
				for i := 0; i < len(op.Glyphs.Glyphs); {
					g := op.Glyphs.Glyphs[i]
					j := i + 1
					for j < len(op.Glyphs.Glyphs) && op.Glyphs.Glyphs[j].TextStart == g.TextStart {
						j++
					}
					if j-i > 1 {
						fmt.Fprintf(content, "/Span << /ActualText <FEFF%s> >> BDC\n", utf16Hex(string(runes[g.TextStart:g.TextEnd])))
					}
					for k := i; k < j; k++ {
						if err := s.Charge("layout_work", 1); err != nil {
							return err
						}
						g = op.Glyphs.Glyphs[k]
						code := f.codes[codeKey{g.ID, g.Unicode}]
						fmt.Fprintf(content, "1 0 0 1 %.6f %.6f Tm <%04X> Tj\n", pen+g.XOffsetPt, page.HeightPt-b.Y+g.YOffsetPt, code)
						pen += g.AdvancePt
					}
					if j-i > 1 {
						fmt.Fprintln(content, "EMC")
					}
					i = j
				}
				fmt.Fprintln(content, "ET")
			case paint.DrawImage:
				name, ok := imageNames[op.AssetID]
				if !ok {
					return fmt.Errorf("%w: missing display image", session.ErrMissingResource)
				}
				fmt.Fprintf(content, "q %.6f 0 0 %.6f %.6f %.6f cm /%s Do Q\n", b.Width, b.Height, b.X, page.HeightPt-b.Y-b.Height, string(name))
			default:
				return fmt.Errorf("%w: unsupported display operation", session.ErrUnsupportedFeature)
			}
			if content.err != nil {
				return content.err
			}
		}
		contents = append(contents, content.String())
		remaining -= int64(content.Len())
	}
	return writeDeterministic(s, dst, pages, contents, encoded, fontNames, encodedImages, imageNames)
}

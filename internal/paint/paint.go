// Package paint defines backend-independent drawing operations in PDF points.
package paint

import (
	"github.com/arham09/html2pdf/internal/font"
	"github.com/arham09/html2pdf/internal/geometry"
	"github.com/arham09/html2pdf/internal/style"
	"image"
)

type Kind uint8

const (
	DrawGlyphRun Kind = iota
	FillRect
	StrokeRect
	DrawImage
	PushClip
	PopClip
)

type Operation struct {
	Kind    Kind
	Source  geometry.SourceID
	Bounds  geometry.Rect
	Color   style.Color
	Glyphs  font.Run
	AssetID string
	FontID  string
	Border  bool // resolved border strip; fragmentation may close it inward
}
type Page struct {
	Fonts             map[string]*font.Font
	Images            map[string]image.Image
	WidthPt, HeightPt float64
	Operations        []Operation
}

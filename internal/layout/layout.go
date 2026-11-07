// Package layout measures block, inline and table flow in logical coordinates.
package layout

import (
	"github.com/arham09/html2pdf/internal/geometry"
	"github.com/arham09/html2pdf/internal/style"
)

type SourceID = geometry.SourceID
type Rect = geometry.Rect
type Box struct {
	Source   SourceID
	Style    style.Computed
	Bounds   Rect
	Children []*Box
}
type BreakKind uint8

const (
	LineBoundary BreakKind = iota
	BlockBoundary
	RowBoundary
)

type BreakOpportunity struct {
	// Offset is an exclusive Result.Lines index for LineBoundary, or an exclusive
	// table-row index for RowBoundary. Atomic interiors are marked Avoid.
	Source SourceID
	Kind   BreakKind
	Offset int
	Avoid  bool
}

// Region is an indivisible measured footprint in logical flow coordinates.
type Region struct {
	Source SourceID
	Bounds Rect
}

// PageEvent requests a forced (page) or preferred (avoid) block boundary.
type PageEvent struct {
	Source SourceID
	Y      float64
	Mode   string
}

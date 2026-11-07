// Package style defines typed values shared by later formatting contexts.
package style

type Unit uint8

const (
	Point Unit = iota
	Pixel
	Percent
	Em
	Rem
	Auto
	Fraction // Grid tracks only; resolved by the Grid formatting context.
)

type Length struct {
	Value float64
	Unit  Unit
}
type Display uint8

const (
	Block Display = iota
	Inline
	InlineBlock
	None
	Table
	Flex
	Grid
	TableRowGroup
	TableHeaderGroup
	TableRow
	TableCell
	ListItem
)

type Color struct{ R, G, B, A uint8 }

// GridTracks is immutable after CSS computation. A pointer keeps Computed
// comparable for inline style/run coalescing without a fixed-size track array.
type GridTracks struct{ Columns []Length }
type GridPlacement struct {
	Start int // one-based line; zero means automatic placement
	Span  int
}

const MaxGridColumns = 64
const MaxGridRows = 1000

type Computed struct {
	Display                              Display
	FontFamily                           string
	FontSizePt, LineHeightPt             float64
	FontWeight                           int
	Italic                               bool
	Color, Background                    Color
	Width                                Length
	Height                               Length
	Margin, Padding                      [4]Length // top, right, bottom, left; percentages use containing width
	Border                               [4]Border
	TextAlign, WhiteSpace, OverflowWrap  string
	LineHeightFactor                     float64 // nonzero when inherited as a unitless multiplier
	BorderCollapse, TableLayout          string
	BreakBefore, BreakAfter, BreakInside string
	FlexDirection, FlexWrap              string
	JustifyContent, AlignItems           string
	AlignSelf, AlignContent              string
	FlexGrow, FlexShrink                 float64
	FlexBasis, RowGap, ColumnGap         Length
	MinWidth, MinHeight                  Length // supported for flex/grid items only
	GridColumns                          *GridTracks
	GridColumn, GridRow                  GridPlacement
	JustifyItems, JustifySelf            string
}

type Border struct {
	Width Length
	Style string
	Color Color
}

// Resolve uses the containing width for percentages. Font and root sizes are
// absolute points; font-size percentages are handled separately by the cascade.
func (l Length) Resolve(width, font, root float64) float64 {
	switch l.Unit {
	case Pixel:
		return l.Value * 0.75
	case Percent:
		return width * l.Value / 100
	case Em:
		return font * l.Value
	case Rem:
		return root * l.Value
	default:
		return l.Value
	}
}

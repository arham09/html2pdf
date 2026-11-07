package html2pdf

import (
	"fmt"
	"math"

	"github.com/arham09/html2pdf/internal/session"
)

// PaperSize is a page size in PDF points (1pt = 1/72 inch).
type PaperSize struct{ WidthPt, HeightPt float64 }

// Margins are page margins in PDF points.
type Margins struct{ TopPt, RightPt, BottomPt, LeftPt float64 }

// HeaderFooter is running HTML repeated on every page. HeightPt reserves a
// fixed region inside the page margins. The text {page} and {pages} is
// replaced with the page number and the page count.
type HeaderFooter struct {
	HTML     string
	HeightPt float64
}

// Limits bounds the work and memory of one render. A zero field selects its
// default, shown in brackets; zero never means unlimited. A negative field
// makes New fail. Totals count everything in one render.
type Limits struct {
	MaxHTMLBytes        int64 // total HTML of the document, header and footer [1 MiB]
	MaxCSSBytes         int64 // total CSS [1 MiB]
	MaxStylesheets      int64 // number of stylesheets [128]
	MaxDOMNodes         int64 // total HTML nodes, including header/footer copies [200,000]
	MaxDOMDepth         int64 // HTML nesting depth [128]
	MaxTableRows        int64 // total table rows [100,000]
	MaxTableCells       int64 // total table cells [1,000,000]
	MaxGlyphs           int64 // total shaped glyphs, including trial layout [2,000,000]
	MaxLayoutWork       int64 // total layout work units [10,000,000]
	MaxPages            int64 // output pages [1,000]
	MaxDisplayOps       int64 // total drawing operations [4,000,000]
	MaxAssets           int64 // number of loaded assets [256]
	MaxAssetBytes       int64 // size of one asset [8 MiB]
	MaxTotalAssetBytes  int64 // total size of assets and fonts [32 MiB]
	MaxDecodedBytes     int64 // total decoded image memory [64 MiB]
	MaxResourceURIBytes int64 // length of one resource reference, such as a data: URI [12 MiB]
	MaxImagePixels      int64 // total image pixels [8,000,000]
	MaxFontBytes        int64 // size of one font [8 MiB]
	MaxFonts            int64 // number of font faces [32]
	MaxOutputBytes      int64 // PDF output size [64 MiB]
	MaxWarnings         int64 // number of warnings [100]
}

// DefaultLimits returns the limits used for zero Limits fields.
func DefaultLimits() Limits { return Limits(session.DefaultLimits()) }

// A4 returns the ISO A4 paper size (210 × 297 mm).
func A4() PaperSize { return PaperSize{595.2756, 841.8898} }

// Letter returns the US Letter paper size (8.5 × 11 in).
func Letter() PaperSize { return PaperSize{612, 792} }

// MillimeterMargins converts margins in millimeters to points.
func MillimeterMargins(top, right, bottom, left float64) Margins {
	const pt = 72.0 / 25.4
	return Margins{top * pt, right * pt, bottom * pt, left * pt}
}

// Options configures an Engine. Nil page settings are omitted. Pointers to
// zero margins request zero margins. Explicit API settings precede CSS @page
// settings. Header/footer heights reserve fixed regions inside page margins,
// above and below the paginated body.
type Options struct {
	PageSize       *PaperSize
	Margins        *Margins
	Header, Footer HeaderFooter
	Fonts          []FontFace    // custom faces, in addition to the bundled Noto Sans
	Resolver       AssetResolver // nil: only data: URIs load
	Limits         Limits
	StrictCSS      bool // reject unsupported CSS instead of warning and ignoring it
}

func finiteNonnegative(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 }
func validatePage(page PaperSize, m Margins, h, f HeaderFooter) error {
	for _, v := range []float64{page.WidthPt, page.HeightPt, m.TopPt, m.RightPt, m.BottomPt, m.LeftPt, h.HeightPt, f.HeightPt} {
		if !finiteNonnegative(v) || v > 14400 {
			return fmt.Errorf("%w: invalid page geometry", ErrInvalidOptions)
		}
	}
	if page.WidthPt == 0 || page.HeightPt == 0 || m.LeftPt+m.RightPt >= page.WidthPt || m.TopPt+m.BottomPt+h.HeightPt+f.HeightPt >= page.HeightPt {
		return fmt.Errorf("%w: page has no usable body area", ErrInvalidOptions)
	}
	if h.HTML != "" && h.HeightPt == 0 || f.HTML != "" && f.HeightPt == 0 {
		return fmt.Errorf("%w: decorations require reserved heights", ErrInvalidOptions)
	}
	return nil
}

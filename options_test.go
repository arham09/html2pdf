package html2pdf_test

import (
	"testing"

	pdf "github.com/arham09/html2pdf"
)

// The Limits field comments and the README document these values.
func TestDefaultLimitsMatchDocumentation(t *testing.T) {
	want := pdf.Limits{
		MaxHTMLBytes: 1 << 20, MaxCSSBytes: 1 << 20, MaxStylesheets: 128,
		MaxDOMNodes: 200000, MaxDOMDepth: 128, MaxTableRows: 100000, MaxTableCells: 1000000,
		MaxGlyphs: 2000000, MaxLayoutWork: 10000000, MaxPages: 1000, MaxDisplayOps: 4000000,
		MaxAssets: 256, MaxAssetBytes: 8 << 20, MaxTotalAssetBytes: 32 << 20, MaxDecodedBytes: 64 << 20,
		MaxResourceURIBytes: 12 << 20, MaxImagePixels: 8000000, MaxFontBytes: 8 << 20, MaxFonts: 32,
		MaxOutputBytes: 64 << 20, MaxWarnings: 100,
	}
	if got := pdf.DefaultLimits(); got != want {
		t.Fatalf("DefaultLimits() = %+v, want %+v", got, want)
	}
}

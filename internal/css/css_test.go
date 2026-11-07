package css

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	dom "github.com/arham09/html2pdf/internal/html"
	"github.com/arham09/html2pdf/internal/session"
	"github.com/arham09/html2pdf/internal/style"
)

func compute(t *testing.T, html string, sheets ...string) (*dom.Document, Page) {
	t.Helper()
	s, _ := session.New(context.Background(), session.Limits{})
	doc, err := dom.Parse(s, html)
	if err != nil {
		t.Fatal(err)
	}
	var sheet Sheet
	for _, text := range append(doc.Stylesheets, sheets...) {
		if err := sheet.Add(s, text, true); err != nil {
			t.Fatal(err)
		}
	}
	page, err := sheet.Compute(s, doc, true)
	if err != nil {
		t.Fatal(err)
	}
	return doc, page
}
func byID(t *testing.T, doc *dom.Document, id string) *dom.Node {
	t.Helper()
	for _, n := range doc.Nodes {
		if n.Attributes["id"] == id {
			return n
		}
	}
	t.Fatalf("missing #%s", id)
	return nil
}
func near(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 0.00001 {
		t.Fatalf("got %f want %f", got, want)
	}
}

func TestCascadeImportanceAndSourceOrder(t *testing.T) {
	doc, _ := compute(t, `<style>.amount { color:red } #target {color:green}</style><div class="invoice"><p id="target" class="amount" style="color:blue">Hello</p></div>`,
		`.invoice > p.amount {color:black!important} #target {color:white} .amount {color:yellow}`)
	if byID(t, doc, "target").Style.Color != (style.Color{A: 255}) {
		t.Fatal("author important did not beat inline normal")
	}
	doc, _ = compute(t, `<p id="target" style="color:blue!important">Hi</p>`, `#target {color:red!important}`)
	if byID(t, doc, "target").Style.Color.B != 255 {
		t.Fatal("inline important lost")
	}
	doc, _ = compute(t, `<style>p{color:red}</style><p id="target">Hi</p>`, `p{color:blue} p{color:green}`)
	if byID(t, doc, "target").Style.Color.G != 128 {
		t.Fatal("supplied/source order")
	}
}

func TestSelectorsAndInheritance(t *testing.T) {
	doc, _ := compute(t, `<div class="invoice" style="font-size:10pt;line-height:1.5;color:red"><div><p id="target" class="amount extra"><strong id="bold">Total</strong></p></div></div>`,
		`.invoice p.amount.extra{font-size:2em;padding:1em 5%;border:1px solid currentcolor} .invoice > p{font-size:100pt}`)
	n := byID(t, doc, "target")
	near(t, n.Style.FontSizePt, 20)
	near(t, n.Style.LineHeightPt, 30)
	near(t, n.Style.Padding[0].Value, 20)
	if n.Style.Padding[1].Unit != style.Percent {
		t.Fatal("percentage prematurely resolved")
	}
	near(t, n.Style.Padding[1].Resolve(200, 20, 12), 10)
	near(t, n.Style.Border[0].Width.Resolve(0, 20, 12), 0.75)
	if n.Style.Border[0].Color.R != 255 {
		t.Fatal("currentcolor lost")
	}
	bold := byID(t, doc, "bold")
	near(t, bold.Style.FontSizePt, 20)
	if bold.Style.FontWeight != 700 {
		t.Fatal("UA strong")
	}
	doc, _ = compute(t, `<div style="font-size:20pt;line-height:150%;margin:2em"><p id="target" style="font-size:10pt;line-height:inherit;margin:inherit;color:initial">Hi</p></div>`)
	n = byID(t, doc, "target")
	near(t, n.Style.LineHeightPt, 30)
	near(t, n.Style.Margin[0].Value, 40)
}

func TestUnitsShorthandsAndRootSize(t *testing.T) {
	doc, _ := compute(t, `<div id="target">Text</div>`, `html {font-size:20pt} #target{width:50%;font-size:50%;margin:1mm 2cm 3in 4px;padding:1rem;border-bottom:2px solid #abc}`)
	n := byID(t, doc, "target")
	near(t, n.Style.FontSizePt, 10)
	near(t, n.Style.Padding[0].Value, 20)
	near(t, n.Style.Margin[0].Value, 72/25.4)
	near(t, n.Style.Margin[1].Value, 2*72/2.54)
	near(t, n.Style.Margin[2].Value, 216)
	near(t, n.Style.Margin[3].Resolve(0, 10, 20), 3)
	near(t, n.Style.Width.Resolve(500, 10, 20), 250)
	if n.Style.Border[2].Color != (style.Color{R: 170, G: 187, B: 204, A: 255}) {
		t.Fatal(n.Style.Border)
	}
}

func TestPrintAndPageRules(t *testing.T) {
	doc, page := compute(t, `<style>@media screen {p{display:inline-grid}} @media print {p{color:red}} @page {size:letter landscape;margin:10mm 20mm;margin-left:0!important} @page {margin-left:5mm}</style><p id="target">Hi</p>`)
	if byID(t, doc, "target").Style.Color.R != 255 {
		t.Fatal("print media")
	}
	near(t, *page.WidthPt, 792)
	near(t, *page.HeightPt, 612)
	near(t, *page.Margins[0], 10*72/25.4)
	near(t, *page.Margins[3], 0)
}

func TestDiagnosticsAndRecovery(t *testing.T) {
	for _, tc := range []struct {
		css              string
		strict, critical bool
	}{
		{`p{box-shadow:1px 1px black;color:red}`, false, false},
		{`p{width:calc(100% - 1px)}`, true, false},
		{`p{display:inline-grid}`, false, true}, {`p{position:absolute}`, false, true},
		{`p:hover{color:red}`, false, false}, {`@import "https://example.test/x.css";`, false, false},
		{`p{bad; color:red}`, false, false},
	} {
		s, _ := session.New(context.Background(), session.Limits{})
		var sheet Sheet
		err := sheet.Add(s, tc.css, tc.strict)
		if tc.strict || tc.critical {
			if !errors.Is(err, session.ErrUnsupportedFeature) {
				t.Fatalf("%s: %v", tc.css, err)
			}
		} else if err != nil || len(s.Warnings()) == 0 {
			t.Fatalf("missing diagnostic for %s: %v", tc.css, err)
		}
	}
	doc, _ := compute(t, `<p id="target">Hi</p>`, `p {margin-left:2px; margin:1px; color:red; color:inherit}`)
	near(t, byID(t, doc, "target").Style.Margin[3].Resolve(0, 12, 12), 0.75)
}

func TestCSSBudgetsAndCancellation(t *testing.T) {
	s, _ := session.New(context.Background(), session.Limits{MaxLayoutWork: 5})
	var sheet Sheet
	if err := sheet.Add(s, `p{color:red}`, false); !errors.Is(err, session.ErrLimitExceeded) {
		t.Fatal(err)
	}
	s, _ = session.New(context.Background(), session.Limits{MaxDOMDepth: 3})
	if err := sheet.Add(s, `p{width:calc((((1px))))}`, false); !errors.Is(err, session.ErrLimitExceeded) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s, _ = session.New(ctx, session.Limits{})
	cancel()
	if err := sheet.Add(s, `p{color:red}`, false); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	s, _ = session.New(context.Background(), session.Limits{MaxCSSBytes: 3})
	if err := sheet.Add(s, strings.Repeat("x", 4), false); !errors.Is(err, session.ErrLimitExceeded) {
		t.Fatal(err)
	}
}

func TestComputedInheritanceAndAliases(t *testing.T) {
	doc, _ := compute(t, `<div style="color:transparent;background-color:currentcolor"><p id="target" style="background-color:inherit;word-wrap:break-word;overflow-wrap:normal">Hi</p></div>`)
	n := byID(t, doc, "target")
	if n.Style.Color.A != 0 || n.Style.Background.A != 0 || n.Style.OverflowWrap != "normal" {
		t.Fatal("computed inheritance/alias ordering", n.Style)
	}
	_, page := compute(t, `<style media="print">@page{size:letter}</style>Hi`)
	if page.WidthPt == nil || *page.WidthPt != 612 {
		t.Fatal("nested print page")
	}
	s, _ := session.New(context.Background(), session.Limits{})
	doc, err := dom.Parse(s, `<p style="@page {size:letter} color:red">Hi</p>`)
	if err != nil {
		t.Fatal(err)
	}
	var sheet Sheet
	p, err := sheet.Compute(s, doc, false)
	if err != nil || p.WidthPt != nil || len(s.Warnings()) == 0 {
		t.Fatal("inline at-rule escaped", err)
	}
}

func FuzzParse(f *testing.F) {
	for _, seed := range []string{`p{color:red}`, `@media print{p{margin:1px}}`, `p{bad;`, `a{width:calc((((`, "\xff"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > 4096 {
			return
		}
		s, _ := session.New(context.Background(), session.Limits{MaxCSSBytes: 4096, MaxLayoutWork: 10000, MaxWarnings: 20})
		var sheet Sheet
		_ = sheet.Add(s, input, false)
	})
}

func TestFlexComputedCascadeAndSection(t *testing.T) {
	doc, _ := compute(t, `<style>.item{flex:1 2 30%;gap:2pt 3pt}#target{flex-grow:3!important}</style><section style="display:flex;flex-flow:wrap row;gap:4pt"><span id="target" class="item" style="flex-grow:4;min-width:0;align-self:center">A</span></section>`)
	st := byID(t, doc, "target").Style
	if st.FlexGrow != 3 || st.FlexShrink != 2 || st.FlexBasis != (style.Length{Value: 30, Unit: style.Percent}) || st.AlignSelf != "center" || st.MinWidth.Unit != style.Point {
		t.Fatalf("computed flex: %+v", st)
	}
	if st.RowGap.Value != 2 || st.ColumnGap.Value != 3 {
		t.Fatal(st)
	}
	doc, _ = compute(t, `<section id="target">Block</section>`)
	if byID(t, doc, "target").Style.Display != style.Block {
		t.Fatal("section is not block")
	}
	doc, _ = compute(t, `<div style="flex:2 3 40pt"><div id="target" style="flex:inherit;flex-shrink:initial;gap:initial"></div></div>`)
	st = byID(t, doc, "target").Style
	if st.FlexGrow != 2 || st.FlexShrink != 1 || st.FlexBasis.Value != 40 || st.RowGap.Value != 0 {
		t.Fatal(st)
	}
}

func TestUnsupportedFlexValuesAreCritical(t *testing.T) {
	for _, decl := range []string{"flex-direction:row-reverse", "flex-wrap:wrap-reverse", "flex-grow:-1", "flex-shrink:NaN", "flex-basis:content", "gap:10%", "align-items:baseline", "order:1", "max-width:50pt", "flex:1 2 -3pt"} {
		for _, strict := range []bool{false, true} {
			s, _ := session.New(context.Background(), session.Limits{})
			var sheet Sheet
			if err := sheet.Add(s, "div{"+decl+"}", strict); !errors.Is(err, session.ErrUnsupportedFeature) {
				t.Fatalf("%s strict=%v: %v", decl, strict, err)
			}
		}
	}
}

func TestGridComputedTracksPlacementAndInheritance(t *testing.T) {
	doc, _ := compute(t, `<section style="font-size:10pt;grid-template-columns:2em 25% 2fr"><section id="target" style="font-size:20pt;display:grid;grid-template-columns:inherit;grid-column:2 / span 3;grid-row:3 / 5;justify-items:end;align-items:start"></section></section>`)
	st := byID(t, doc, "target").Style
	if st.Display != style.Grid || st.GridColumns == nil || len(st.GridColumns.Columns) != 3 {
		t.Fatal(st)
	}
	if st.GridColumns.Columns[0] != (style.Length{Value: 20}) || st.GridColumns.Columns[1] != (style.Length{Value: 25, Unit: style.Percent}) || st.GridColumns.Columns[2] != (style.Length{Value: 2, Unit: style.Fraction}) {
		t.Fatal(st.GridColumns)
	}
	if st.GridColumn != (style.GridPlacement{Start: 2, Span: 3}) || st.GridRow != (style.GridPlacement{Start: 3, Span: 2}) || st.JustifyItems != "flex-end" || st.AlignItems != "flex-start" {
		t.Fatal(st)
	}
	_ = st == st // Computed must remain comparable for inline-run merging.
	doc, _ = compute(t, `<style>#target{grid-column:2!important;grid-template-columns:10pt 1fr}</style><div id="target" style="grid-column:3;grid-template-columns:initial"></div>`)
	st = byID(t, doc, "target").Style
	if st.GridColumns != nil || st.GridColumn.Start != 2 {
		t.Fatal(st)
	}
}

func TestGridValuesRejectUnsupportedOrUnboundedLayouts(t *testing.T) {
	for _, decl := range []string{"grid-template-columns:repeat(3,1fr)", "grid-template-columns:minmax(0,1fr)", "grid-template-columns:subgrid", "grid-template-columns:-1fr", "grid-template-columns:NaNfr", "grid-template-columns:auto 1fr", "grid-auto-flow:dense", "grid-auto-flow:column", "grid-column:0", "grid-column:-1", "grid-column:2 / 1", "grid-column:span 65", "grid-row:1000 / span 2", "grid-template-rows:10pt 10pt", "grid-area:1 / 1", "justify-items:baseline", "grid-template-columns:" + strings.Repeat("1fr ", 65)} {
		for _, strict := range []bool{false, true} {
			s, _ := session.New(context.Background(), session.Limits{})
			var sheet Sheet
			if err := sheet.Add(s, "div{"+decl+"}", strict); !errors.Is(err, session.ErrUnsupportedFeature) {
				t.Fatalf("%s strict=%v: %v", decl, strict, err)
			}
		}
	}
}

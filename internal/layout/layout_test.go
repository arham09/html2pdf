package layout

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/arham09/html2pdf/internal/css"
	"github.com/arham09/html2pdf/internal/font"
	dom "github.com/arham09/html2pdf/internal/html"
	"github.com/arham09/html2pdf/internal/paint"
	"github.com/arham09/html2pdf/internal/resource"
	"github.com/arham09/html2pdf/internal/session"
)

func renderLayout(t *testing.T, input string, width float64) (Result, error) {
	t.Helper()
	s, _ := session.New(context.Background(), session.Limits{})
	doc, err := dom.Parse(s, input)
	if err != nil {
		return Result{}, err
	}
	var sheet css.Sheet
	for _, text := range doc.Stylesheets {
		if err := sheet.Add(s, text, true); err != nil {
			return Result{}, err
		}
	}
	if _, err := sheet.Compute(s, doc, true); err != nil {
		return Result{}, err
	}
	// A single fixed page, so content taller than 740pt fails.
	return build(s, doc, font.NewRegistry(s, nil), resource.Loader{Session: s}, 595.2756, 841.8898, Rect{X: 40, Y: 40, Width: width, Height: 740}, false, true)
}
func TestWrappingConservesTextAndMetrics(t *testing.T) {
	result, err := renderLayout(t, `<div style="font-size:12pt">An <strong>important</strong> café invoice with é and ffi text wraps across narrow lines.</div>`, 160)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Lines) < 3 {
		t.Fatal("did not wrap")
	}
	var text []string
	for _, line := range result.Lines {
		text = append(text, line.Text)
		if line.Bounds.Width > 160.0001 {
			t.Fatal("line overflow")
		}
	}
	if strings.Join(text, " ") != "An important café invoice with é and ffi text wraps across narrow lines." {
		t.Fatal(text)
	}
	for _, op := range result.Page.Operations {
		if op.Kind == paint.DrawGlyphRun && math.Abs(op.Bounds.Width-op.Glyphs.AdvancePt) > 0.00001 {
			t.Fatal("measurement/painter mismatch")
		}
	}
	if len(result.Page.Fonts) != 2 {
		t.Fatal("bold face not selected")
	}
}

func TestLongWordAndGraphemeSplitting(t *testing.T) {
	if _, err := renderLayout(t, `<div>extraordinarilylongunbreakableword</div>`, 30); !errors.Is(err, session.ErrUnbreakableContent) {
		t.Fatal(err)
	}
	input := strings.Repeat("q\u0301", 12)
	result, err := renderLayout(t, `<div style="overflow-wrap:break-word">`+input+`</div>`, 30)
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	for _, line := range result.Lines {
		if strings.HasPrefix(line.Text, "\u0301") {
			t.Fatal("split combining cluster")
		}
		text.WriteString(line.Text)
	}
	if text.String() != input || len(result.Lines) < 2 {
		t.Fatal("split lost text", text.String())
	}
}

func TestWhitespaceAlignmentAndBoxes(t *testing.T) {
	result, err := renderLayout(t, `<div style="width:100pt;margin:0 auto;padding:3pt;border:1pt solid blue;background:#eee;text-align:right">  Hello   world </div><div style="white-space:pre">A   B
C</div>`, 300)
	if err != nil {
		t.Fatal(err)
	}
	if result.Lines[0].Text != "Hello world" {
		t.Fatal(result.Lines[0])
	}
	if result.Lines[0].Bounds.X <= 140 {
		t.Fatal("right alignment or centering missing", result.Lines[0])
	}
	var found bool
	for _, box := range result.Boxes {
		if box.Style.Background.A != 0 {
			found = true
			if math.Abs(box.Bounds.Width-108) > 0.0001 {
				t.Fatal(box.Bounds)
			}
			if math.Abs(box.Bounds.X-136) > 0.0001 {
				t.Fatal(box.Bounds)
			}
		}
	}
	if !found {
		t.Fatal("missing decorated box")
	}
	if result.Lines[1].Text != "A   B" || result.Lines[2].Text != "C" {
		t.Fatal("pre whitespace", result.Lines)
	}
}

func TestNoWrapCollapseAndOrderedValues(t *testing.T) {
	result, err := renderLayout(t, `<div style="white-space:nowrap">  A   <strong> B </strong> C  </div><ol start="5"><li>First</li><li value="9"><p>Second</p></li><li>Third</li></ol>`, 300)
	if err != nil {
		t.Fatal(err)
	}
	if result.Lines[0].Text != "A B C" {
		t.Fatal("nowrap collapse", result.Lines[0].Text)
	}
	var text strings.Builder
	for _, op := range result.Page.Operations {
		if op.Kind == paint.DrawGlyphRun {
			text.WriteString(op.Glyphs.Text)
		}
	}
	if !strings.Contains(text.String(), "5.First") || !strings.Contains(text.String(), "9.Second") || !strings.Contains(text.String(), "10.Third") {
		t.Fatal("list source order/values", text.String())
	}
}

func TestInlineBlocksListsAndOverflow(t *testing.T) {
	result, err := renderLayout(t, `<div><span style="display:inline-block;width:100pt;background:#eee;padding:2pt"><strong>Card one</strong></span> <span style="display:inline-block;width:100pt">Card two</span></div><ol><li>First item</li><li>Second item</li></ol>`, 300)
	if err != nil {
		t.Fatal(err)
	}
	var content strings.Builder
	for _, op := range result.Page.Operations {
		if op.Kind == paint.DrawGlyphRun {
			content.WriteString(op.Glyphs.Text)
		}
	}
	if !strings.Contains(content.String(), "Card one") || !strings.Contains(content.String(), "1.") || !strings.Contains(content.String(), "2.") {
		t.Fatal(content.String())
	}
	for _, input := range []string{`<table><tr><td rowspan="2">Later</td></tr></table>`, `<p style="break-before:page">Later</p>`, `<span style="padding:1pt">Unsupported</span>`} {
		if _, err := renderLayout(t, input, 300); !errors.Is(err, session.ErrUnsupportedFeature) {
			t.Fatal(input, err)
		}
	}
	if _, err := renderLayout(t, strings.Repeat("<p>line</p>", 100), 300); !errors.Is(err, session.ErrUnbreakableContent) {
		t.Fatal("vertical overflow", err)
	}
}

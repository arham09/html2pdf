package pagination

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/png"
	"math"
	"strings"
	"testing"

	"github.com/arham09/html2pdf/internal/css"
	"github.com/arham09/html2pdf/internal/font"
	dom "github.com/arham09/html2pdf/internal/html"
	"github.com/arham09/html2pdf/internal/layout"
	"github.com/arham09/html2pdf/internal/paint"
	"github.com/arham09/html2pdf/internal/resource"
	"github.com/arham09/html2pdf/internal/session"
)

func paginate(t *testing.T, input string, height float64, limits session.Limits) ([]Page, *session.Session, error) {
	t.Helper()
	s, err := session.New(context.Background(), limits)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := dom.Parse(s, `<style>body{font-size:10pt;line-height:20pt}p{margin:0}</style>`+input)
	if err != nil {
		t.Fatal(err)
	}
	var sheets css.Sheet
	for _, text := range tree.Stylesheets {
		if err := sheets.Add(s, text, true); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := sheets.Compute(s, tree, true); err != nil {
		t.Fatal(err)
	}
	body := layout.Rect{X: 20, Y: 20, Width: 300, Height: height}
	flow, err := layout.BuildFlow(s, tree, font.NewRegistry(s, nil), resource.Loader{Session: s}, 340, height+40, body)
	if err != nil {
		return nil, s, err
	}
	pages, err := Build(s, flow, body)
	return pages, s, err
}
func text(page Page) string {
	var out strings.Builder
	for _, op := range page.Paint.Operations {
		if op.Kind == paint.DrawGlyphRun {
			out.WriteString(op.Glyphs.Text)
		}
	}
	return out.String()
}
func TestParagraphExactFullAndExplicitBreaks(t *testing.T) {
	cases := []struct {
		input string
		pages []string
	}{
		{`<p>A<br>B<br>C<br>D<br>E</p>`, []string{"ABCDE"}},
		{`<p>A<br>B<br>C<br>D<br>E<br>F</p>`, []string{"ABCDE", "F"}},
		{`<p>A<br>B<br>C<br>D<br>E</p><p style="break-before:page">F</p>`, []string{"ABCDE", "F"}},
		{`<p style="break-before:page;break-after:page">A</p><p style="break-before:page;break-after:page">B</p>`, []string{"A", "B"}},
		{`<p>A</p><div style="break-before:page"></div><p>B</p>`, []string{"A", "B"}},
		{`<table></table>`, []string{""}},
	}
	for _, c := range cases {
		pages, _, err := paginate(t, c.input, 100, session.Limits{})
		if err != nil {
			t.Fatal(c.input, err)
		}
		if len(pages) != len(c.pages) {
			t.Fatalf("%s: %d pages", c.input, len(pages))
		}
		for i, want := range c.pages {
			if text(pages[i]) != want {
				t.Fatalf("page %d: %q, want %q", i, text(pages[i]), want)
			}
		}
	}
}
func TestAvoidAndForcedPrecedence(t *testing.T) {
	prefix := `<p>A<br>B<br>C<br>D</p>`
	for _, suffix := range []string{`<div style="break-inside:avoid"><p>E<br>F</p></div>`, `<p style="break-after:avoid">E</p><p>F</p>`} {
		pages, _, err := paginate(t, prefix+suffix, 100, session.Limits{})
		if err != nil {
			t.Fatal(err)
		}
		if len(pages) != 2 || text(pages[0]) != "ABCD" || text(pages[1]) != "EF" {
			t.Fatal("avoid did not move", pages)
		}
	}
	pages, s, err := paginate(t, `<div style="break-inside:avoid"><p>A<br>B<br>C<br>D<br>E<br>F</p></div>`, 100, session.Limits{})
	if err != nil || len(pages) != 2 || len(s.Warnings()) != 1 || s.Warnings()[0].Code != "avoid-relaxed" {
		t.Fatal("oversized avoid", err, s.Warnings())
	}
	pages, _, err = paginate(t, `<div style="break-inside:avoid"><p>A</p><p style="break-before:page">B</p></div>`, 100, session.Limits{})
	if err != nil || len(pages) != 2 || text(pages[0]) != "A" || text(pages[1]) != "B" {
		t.Fatal("forced break must win", err)
	}
}
func TestTableConservationAndRepeatedHeadings(t *testing.T) {
	input := `<table style="width:300pt;table-layout:fixed;border-collapse:collapse"><thead><tr><th>HEADER</th></tr></thead><tbody>`
	for _, row := range []string{"A", "B", "C", "D", "E", "F"} {
		input += `<tr><td style="border:1pt solid black">` + row + `</td></tr>`
	}
	input += `</tbody></table>`
	pages, _, err := paginate(t, input, 100, session.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 2 || text(pages[0]) != "HEADERABC" || text(pages[1]) != "HEADERDEF" {
		t.Fatalf("headings/count: %v %d", err, len(pages))
	}
	seen := map[layout.SourceID]bool{}
	repeats := 0
	for _, page := range pages {
		for _, f := range page.Fragments {
			if f.RepeatedDecoration {
				repeats++
				continue
			}
			if seen[f.Content.Source] {
				t.Fatal("duplicate body row")
			}
			seen[f.Content.Source] = true
			if f.Bounds.Y < page.Body.Y || f.Bounds.Y+f.Bounds.Height > page.Body.Y+page.Body.Height+.0001 {
				t.Fatal("fragment overflow")
			}
		}
	}
	if len(seen) != 7 || repeats != 1 {
		t.Fatal("source identity", seen, repeats)
	}
	// A table near the bottom moves its heading and first row together.
	pages, _, err = paginate(t, `<p>X<br>Y<br>Z<br>W</p>`+input, 100, session.Limits{})
	if err != nil || text(pages[0]) != "XYZW" || !strings.HasPrefix(text(pages[1]), "HEADERA") {
		t.Fatal("orphan heading", err)
	}
}
func TestOversizedRowsHeadingsAndAtoms(t *testing.T) {
	for _, input := range []string{
		`<p style="line-height:101pt">Huge</p>`,
		`<div style="display:inline-block;width:200pt">A<br>B<br>C<br>D<br>E<br>F</div>`,
		`<table><tr><td>A<br>B<br>C<br>D<br>E<br>F</td></tr></table>`,
		`<table><thead><tr><th>A<br>B<br>C<br>D</th></tr></thead><tbody><tr><td>E<br>F</td></tr></tbody></table>`,
	} {
		if _, _, err := paginate(t, input, 100, session.Limits{}); !errors.Is(err, session.ErrUnbreakableContent) {
			t.Fatal(input, err)
		}
	}
	if _, _, err := paginate(t, `<p>A<br>B<br>C<br>D<br>E<br>F</p>`, 100, session.Limits{MaxPages: 1}); !errors.Is(err, session.ErrLimitExceeded) {
		t.Fatal("page quota", err)
	}
	if _, _, err := paginate(t, `<div style="display:inline-block;width:200pt"><p style="break-before:page">A</p></div>`, 100, session.Limits{}); !errors.Is(err, session.ErrUnsupportedFeature) {
		t.Fatal("atomic forced break", err)
	}
}

func TestContinuationHeadingCapacityAndGroupAvoid(t *testing.T) {
	input := `<table><thead><tr><th>H<br>H<br>H</th></tr></thead><tbody><tr><td>A</td></tr><tr><td>B<br>C<br>D</td></tr></tbody></table>`
	if _, _, err := paginate(t, input, 100, session.Limits{}); !errors.Is(err, session.ErrUnbreakableContent) {
		t.Fatal("continuation row does not fit below heading", err)
	}
	pages, _, err := paginate(t, `<p>A<br>B<br>C</p><table><thead><tr><th>H</th></tr></thead><tbody style="break-inside:avoid"><tr><td>D</td></tr><tr><td>E</td></tr></tbody></table>`, 100, session.Limits{})
	if err != nil || len(pages) != 2 || text(pages[0]) != "ABC" || text(pages[1]) != "HDE" {
		t.Fatal("group avoid/orphan heading", err)
	}
}
func TestPaginationCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s, _ := session.New(ctx, session.Limits{})
	cancel()
	if _, err := Build(s, layout.Result{}, layout.Rect{Width: 100, Height: 100}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestEmptyRowIdentityAndAtomicImage(t *testing.T) {
	pages, _, err := paginate(t, `<table><thead><tr></tr></thead><tbody><tr></tr></tbody></table>`, 100, session.Limits{})
	if err != nil || len(pages) != 1 || len(pages[0].Fragments) != 2 {
		t.Fatal("empty row identity", pages, err)
	}
	// The first page has only 20pt remaining; a 30pt image must move intact.
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewNRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	input := `<p>A<br>B<br>C<br>D</p><img style="width:30pt;height:30pt" src="data:image/png;base64,` + base64.StdEncoding.EncodeToString(encoded.Bytes()) + `">`
	pages, _, err = paginate(t, input, 100, session.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 2 {
		t.Fatal("image fragmented")
	}
	images := 0
	for _, op := range pages[1].Paint.Operations {
		if op.Kind == paint.DrawImage {
			images++
			if op.Bounds.Height != 30 {
				t.Fatal("image resized")
			}
		}
	}
	if images != 1 {
		t.Fatal("image lost")
	}
}

func TestContinuedCollapsedBorderClosure(t *testing.T) {
	input := `<style>td,th{border:2pt solid blue}</style><table style="width:300pt;table-layout:fixed;border-collapse:collapse"><thead><tr><th>H</th></tr></thead><tbody>` + strings.Repeat(`<tr><td>A</td></tr>`, 7) + `</tbody></table>`
	pages, _, err := paginate(t, input, 100, session.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) < 2 {
		t.Fatal("fixture did not paginate")
	}
	first := pages[0]
	closingY := first.Body.Y + first.FlowEnd - first.FlowStart
	found := false
	for _, op := range first.Paint.Operations {
		r := op.Bounds
		if op.Border && r.Width == 300 && r.Height == 2 && math.Abs(r.Y+r.Height-closingY) < .0001 {
			found = true
		}
	}
	if !found {
		t.Fatal("outgoing collapsed border not closed inward")
	}
	join := pages[1].Body.Y + pages[1].HeaderHeight
	count := 0
	for _, op := range pages[1].Paint.Operations {
		r := op.Bounds
		if op.Border && r.Width == 300 && r.Height == 2 && math.Abs(r.Y+r.Height-join) < .0001 {
			count++
		}
	}
	if count != 1 {
		t.Fatal("repeated header join border", count)
	}
}

func TestAvoidAfterContinuedTableUsesFreshPageCapacity(t *testing.T) {
	input := `<table><thead><tr><th>H</th></tr></thead><tbody>` + strings.Repeat(`<tr><td>X</td></tr>`, 6) + `</tbody></table><div style="break-inside:avoid">A<br>B<br>C<br>D<br>E</div>`
	pages, s, err := paginate(t, input, 100, session.Limits{})
	if err != nil || len(pages) != 3 || text(pages[2]) != "ABCDE" || len(s.Warnings()) != 0 {
		t.Fatal("fresh avoid capacity", err, s.Warnings())
	}
}

func TestFlexContainersMoveAsWholeAndTablesContinue(t *testing.T) {
	for _, suffix := range []string{
		`<section style="display:flex"><div>E<br>F</div><div>G<br>H</div></section>`,
		`<section style="display:flex;flex-direction:column"><div>E</div><div>F</div></section>`,
		`<section style="display:flex;flex-wrap:wrap"><div style="flex:0 0 200pt">E</div><div style="flex:0 0 200pt">F</div></section>`,
	} {
		pages, _, err := paginate(t, `<p>A<br>B<br>C<br>D</p>`+suffix, 100, session.Limits{})
		if err != nil || len(pages) != 2 || text(pages[0]) != "ABCD" || !strings.HasPrefix(text(pages[1]), "EF") {
			t.Fatalf("atomic flex: %v, %v", err, pages)
		}
	}
	pages, _, err := paginate(t, `<section style="display:flex"><div>A<br>B<br>C<br>D<br>E</div></section>`, 100, session.Limits{})
	if err != nil || len(pages) != 1 || text(pages[0]) != "ABCDE" {
		t.Fatal("exact fit", err)
	}
	for _, input := range []string{
		`<section style="display:flex;flex-direction:column"><div>A<br>B<br>C<br>D<br>E<br>F</div></section>`,
		`<section style="display:flex;height:101pt"></section>`,
		`<section style="display:flex"><div style="flex:1;min-width:0"><table><tr><td>A<br>B<br>C<br>D<br>E<br>F</td></tr></table></div></section>`,
	} {
		if _, _, err := paginate(t, input, 100, session.Limits{}); !errors.Is(err, session.ErrUnbreakableContent) {
			t.Fatal("oversized atomic flex", input, err)
		}
	}
	input := `<section style="display:flex"><div>FLEX HEADER</div></section><table><thead><tr><th>H</th></tr></thead><tbody>` + strings.Repeat(`<tr><td>X</td></tr>`, 7) + `</tbody></table>`
	pages, _, err = paginate(t, input, 100, session.Limits{})
	if err != nil || len(pages) != 2 || text(pages[0]) != "FLEX HEADERHXXX" || text(pages[1]) != "HXXXX" {
		t.Fatalf("table after flex %v: %v", err, pages)
	}
}

func TestGridRowsPaginateWithoutSplittingItems(t *testing.T) {
	input := `<section style="display:grid;grid-template-columns:1fr 1fr">`
	for _, s := range []string{"A", "B", "C", "D", "E", "F", "G", "H", "I", "J", "K", "L"} {
		input += `<div>` + s + `</div>`
	}
	input += `</section>`
	pages, _, err := paginate(t, input, 100, session.Limits{})
	if err != nil || len(pages) != 2 || text(pages[0]) != "ABCDEFGHIJ" || text(pages[1]) != "KL" {
		t.Fatalf("row cuts: %v, %v", err, pages)
	}
	pages, _, err = paginate(t, `<p>P<br>Q<br>R<br>S</p><section style="display:grid;grid-template-columns:1fr 1fr"><div>A<br>B</div><div>C<br>D</div></section>`, 100, session.Limits{})
	if err != nil || len(pages) != 2 || text(pages[0]) != "PQRS" || text(pages[1]) != "ABCD" {
		t.Fatal("row must move intact", err, pages)
	}
}

func TestGridRowSpansProtectWholeConnectedGroup(t *testing.T) {
	input := `<section style="display:grid;grid-template-columns:1fr 1fr"><div style="grid-column:1;grid-row:1 / span 2;align-self:start">A</div><div style="grid-column:2;grid-row:1">B<br>C</div><div style="grid-column:2;grid-row:2">D<br>E</div></section>`
	pages, _, err := paginate(t, `<p>P<br>Q</p>`+input, 100, session.Limits{})
	if err != nil || len(pages) != 2 || text(pages[0]) != "PQ" || text(pages[1]) != "ABCDE" {
		t.Fatal("span group must move intact", err, pages)
	}
	if _, _, err := paginate(t, input, 60, session.Limits{}); !errors.Is(err, session.ErrUnbreakableContent) {
		t.Fatal("short spanning item cannot let connected rows split", err)
	}
	input = `<section style="display:grid;grid-template-columns:1fr 1fr"><div style="grid-column:1;grid-row:1 / span 2">A</div><div style="grid-column:2;grid-row:2 / span 2">B</div><div style="grid-column:2;grid-row:1">C<br>D</div><div style="grid-column:1;grid-row:3">E<br>F</div></section>`
	if _, _, err := paginate(t, input, 60, session.Limits{}); !errors.Is(err, session.ErrUnbreakableContent) {
		t.Fatal("transitive row span group split", err)
	}
}

func TestGridOversizedRowsFixedContextsAndEmptyAreas(t *testing.T) {
	for _, input := range []string{
		`<section style="display:grid;grid-template-columns:1fr"><div>A<br>B<br>C<br>D<br>E<br>F</div></section>`,
		`<section style="display:grid;grid-template-columns:1fr"><div style="height:101pt"></div></section>`,
		`<div style="display:inline-block;width:300pt"><section style="display:grid;grid-template-columns:1fr">` + strings.Repeat(`<div>A</div>`, 6) + `</section></div>`,
	} {
		if _, _, err := paginate(t, input, 100, session.Limits{}); !errors.Is(err, session.ErrUnbreakableContent) {
			t.Fatal(input, err)
		}
	}
	pages, _, err := paginate(t, `<section style="display:grid;grid-template-columns:1fr"><div>A<br>B<br>C<br>D<br>E</div></section>`, 100, session.Limits{})
	if err != nil || len(pages) != 1 || text(pages[0]) != "ABCDE" {
		t.Fatal("exact row fit", err)
	}
}

func TestGridEmptyRowGapsStillConsumeFlow(t *testing.T) {
	pages, _, err := paginate(t, `<section style="display:grid;grid-template-columns:1fr;row-gap:60pt"><div></div><div></div></section>`, 50, session.Limits{})
	if err != nil || len(pages) != 2 {
		t.Fatal("empty Grid lost its gap extent", len(pages), err)
	}
}

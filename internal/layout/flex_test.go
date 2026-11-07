package layout

import (
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/arham09/html2pdf/internal/session"
)

func flexItemBoxes(r Result) []Rect {
	var boxes []Rect
	for _, box := range r.Boxes {
		if box.Style.Background.R == 255 && box.Style.Background.G == 0 && box.Style.Background.A == 255 {
			boxes = append(boxes, box.Bounds)
		}
	}
	return boxes
}
func closeFlex(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > .0001 {
		t.Fatalf("got %.5f want %.5f", got, want)
	}
}
func TestFlexMainSizingGeometry(t *testing.T) {
	for _, tc := range []struct {
		name, container, a, b string
		widths                []float64
		xs                    []float64
	}{
		{"grow", "gap:20pt", "flex:1 1 50pt", "flex:1 1 50pt", []float64{140, 140}, []float64{40, 200}},
		{"scaled shrink", "width:240pt", "flex:0 1 200pt", "flex:0 1 100pt", []float64{160, 80}, []float64{40, 200}},
		{"freeze minimum", "width:180pt", "flex:0 1 100pt;min-width:100pt", "flex:0 1 100pt", []float64{100, 80}, []float64{40, 140}},
		{"fractional grow", "justify-content:flex-end", "flex:.25 1 50pt", "flex:.25 1 50pt", []float64{100, 100}, []float64{140, 240}},
		{"grow past minimum", "", "flex:1 1 0;min-width:100pt", "flex:1 1 0", []float64{150, 150}, []float64{40, 190}},
		{"percent basis", "gap:20pt", "flex:0 0 40%", "flex:0 0 40%", []float64{120, 120}, []float64{40, 180}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := `<style>.item{background:red;height:20pt;min-width:0}</style><section style="display:flex;` + tc.container + `"><div class="item" style="` + tc.a + `"></div><div class="item" style="` + tc.b + `"></div></section>`
			r, err := renderLayout(t, input, 300)
			if err != nil {
				t.Fatal(err)
			}
			boxes := flexItemBoxes(r)
			if len(boxes) != 2 {
				t.Fatal(boxes)
			}
			for i, box := range boxes {
				closeFlex(t, box.Width, tc.widths[i])
				closeFlex(t, box.X, tc.xs[i])
				closeFlex(t, box.Height, 20)
			}
		})
	}
}
func TestFlexCrossAxesWrappingAndEdges(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		want        []Rect
	}{
		{"column", `<section style="display:flex;flex-direction:column;height:200pt;gap:20pt;align-items:center"><div class="item" style="width:100pt;flex:1 1 40pt"></div><div class="item" style="width:100pt;flex:1 1 40pt"></div></section>`, []Rect{{X: 140, Y: 40, Width: 100, Height: 90}, {X: 140, Y: 150, Width: 100, Height: 90}}},
		{"wrap", `<section style="display:flex;flex-wrap:wrap;gap:5pt 10pt"><div class="item"></div><div class="item"></div><div class="item"></div></section>`, []Rect{{X: 40, Y: 40, Width: 100, Height: 20}, {X: 150, Y: 40, Width: 100, Height: 20}, {X: 40, Y: 65, Width: 100, Height: 20}}},
		{"line packing", `<section style="display:flex;flex-wrap:wrap;gap:10pt;height:120pt;align-content:space-between"><div class="item"></div><div class="item"></div><div class="item"></div></section>`, []Rect{{X: 40, Y: 40, Width: 100, Height: 20}, {X: 150, Y: 40, Width: 100, Height: 20}, {X: 40, Y: 140, Width: 100, Height: 20}}},
		{"center", `<section style="display:flex;height:100pt;align-items:center;justify-content:space-between"><div class="item"></div><div class="item" style="align-self:flex-end"></div></section>`, []Rect{{X: 40, Y: 80, Width: 100, Height: 20}, {X: 240, Y: 120, Width: 100, Height: 20}}},
		{"percentage edges", `<section style="display:flex;gap:10pt"><div class="item" style="width:auto;flex:0 0 30%;padding:5%;border:1pt solid"></div><div class="item" style="width:auto;flex:0 0 30%;padding:5%;border:1pt solid"></div></section>`, []Rect{{X: 40, Y: 40, Width: 122, Height: 52}, {X: 172, Y: 40, Width: 122, Height: 52}}},
		{"stretch", `<section style="display:flex"><div class="item" style="height:auto">A<br>B</div><div class="item" style="height:auto"></div></section>`, []Rect{{X: 40, Y: 40, Width: 100, Height: 40}, {X: 140, Y: 40, Width: 100, Height: 40}}},
		{"minimum height", `<section style="display:flex"><div class="item" style="height:auto;min-height:50pt"></div><div class="item" style="height:auto"></div></section>`, []Rect{{X: 40, Y: 40, Width: 100, Height: 50}, {X: 140, Y: 40, Width: 100, Height: 50}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := `<style>.item{background:red;width:100pt;height:20pt;min-width:0;min-height:0;flex-shrink:0;font-size:10pt;line-height:20pt}</style>` + tc.input
			r, err := renderLayout(t, input, 300)
			if err != nil {
				t.Fatal(err)
			}
			boxes := flexItemBoxes(r)
			if len(boxes) != len(tc.want) {
				t.Fatal(boxes)
			}
			for i, want := range tc.want {
				got := boxes[i]
				closeFlex(t, got.X, want.X)
				closeFlex(t, got.Y, want.Y)
				closeFlex(t, got.Width, want.Width)
				closeFlex(t, got.Height, want.Height)
			}
		})
	}
}
func TestFlexAnonymousItemsIntrinsicMinimaAndTables(t *testing.T) {
	r, err := renderLayout(t, `<section style="display:flex;gap:10pt">Alpha <span>Beta</span><span style="display:none">Hidden</span></section>`, 300)
	if err != nil {
		t.Fatal(err)
	}
	var text []string
	for _, line := range r.Lines {
		text = append(text, line.Text)
	}
	if strings.Join(text, " ") != "Alpha Beta" {
		t.Fatal(text)
	}
	if _, err := renderLayout(t, `<section style="display:flex;width:40pt"><div>UnbreakableMinimum</div><div>Other</div></section>`, 300); !errors.Is(err, session.ErrUnbreakableContent) {
		t.Fatal(err)
	}
	if _, err := renderLayout(t, `<section style="display:flex"><div style="flex:1;min-width:0"><table style="width:100%;table-layout:fixed"><tr><td>Inside</td></tr></table></div><div style="flex:1">Outside</div></section>`, 300); err != nil {
		t.Fatal(err)
	}
	// Content-based auto bases share the exact same shaping widths.
	r, err = renderLayout(t, `<section style="display:flex"><span>Same</span><span>Same</span></section>`, 300)
	if err != nil || len(r.Lines) != 2 {
		t.Fatal(err)
	}
	closeFlex(t, r.Lines[1].Bounds.X-r.Lines[0].Bounds.X, r.Lines[0].Bounds.Width)
}
func TestFlexUnsupportedAndImpossibleGeometry(t *testing.T) {
	for _, input := range []string{
		`<div style="display:flex"><div style="display:flex">Nested</div></div>`,
		`<div style="display:flex"><div style="margin-left:auto">Auto</div></div>`,
		`<div style="display:flex;flex-direction:column;flex-wrap:wrap">Wrap</div>`,
		`<div style="display:flex;flex-direction:column"><div style="flex-basis:50%">Percent</div></div>`,
		`<div style="display:flex;height:50%">Height</div>`,
		`<div style="display:flex"><div style="break-before:page">Break</div></div>`,
		`<div style="display:flex"><div><table><tr><td>Auto table</td></tr></table></div></div>`,
		`<div style="min-width:20pt">Outside flex</div>`,
	} {
		if _, err := renderLayout(t, input, 300); !errors.Is(err, session.ErrUnsupportedFeature) {
			t.Fatalf("%s: %v", input, err)
		}
	}
	for _, input := range []string{
		`<div style="display:flex;width:100pt"><div style="min-width:70pt"></div><div style="min-width:70pt"></div></div>`,
		`<div style="display:flex;height:1pt"><div>Content cannot clip</div></div>`,
		`<div style="display:flex"><div style="height:1pt">Content cannot clip</div></div>`,
	} {
		if _, err := renderLayout(t, input, 300); !errors.Is(err, session.ErrUnbreakableContent) {
			t.Fatalf("%s: %v", input, err)
		}
	}
}

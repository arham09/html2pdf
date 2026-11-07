package layout

import (
	"errors"
	"strings"
	"testing"

	"github.com/arham09/html2pdf/internal/session"
)

func TestGridTracksAndAlignmentNumericalGeometry(t *testing.T) {
	for _, tc := range []struct {
		name, container, a, b, c string
		want                     []Rect
	}{
		{"equal", "grid-template-columns:1fr 1fr;gap:10pt", "", "", "", []Rect{{X: 40, Y: 40, Width: 145, Height: 20}, {X: 195, Y: 40, Width: 145, Height: 20}}},
		{"mixed", "grid-template-columns:80px 20% 2fr;gap:10pt", "", "", "", []Rect{{X: 40, Y: 40, Width: 60, Height: 20}, {X: 110, Y: 40, Width: 60, Height: 20}}},
		{"partial fr", "grid-template-columns:.25fr .25fr;justify-content:end", "", "", "", []Rect{{X: 190, Y: 40, Width: 75, Height: 20}, {X: 265, Y: 40, Width: 75, Height: 20}}},
		{"explicit span", "grid-template-columns:1fr 1fr 1fr;gap:10pt", "grid-column:1 / span 2", "grid-column:3", "", []Rect{{X: 40, Y: 40, Width: 196.6666667, Height: 20}, {X: 246.6666667, Y: 40, Width: 93.3333333, Height: 20}}},
		{"percentage edges", "grid-template-columns:1fr 1fr;gap:20pt", "padding:5%;border:1pt solid", "padding:5%;border:1pt solid", "", []Rect{{X: 40, Y: 40, Width: 140, Height: 36}, {X: 200, Y: 40, Width: 140, Height: 36}}},
		{"item alignment", "grid-template-columns:1fr 1fr;gap:20pt;align-items:end;justify-items:center", "width:40pt;height:10pt", "width:60pt;height:30pt", "", []Rect{{X: 90, Y: 60, Width: 40, Height: 10}, {X: 240, Y: 40, Width: 60, Height: 30}}},
		{"shrink edges", "grid-template-columns:1fr 1fr;gap:20pt;justify-items:center", "width:40pt;padding:5%;border:1pt solid", "height:auto", "", []Rect{{X: 82, Y: 40, Width: 56, Height: 36}, {X: 270, Y: 40, Width: 0, Height: 36}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := `<style>.item{background:red;height:20pt}</style><section style="display:grid;` + tc.container + `"><div class="item" style="` + tc.a + `"></div><div class="item" style="` + tc.b + `"></div></section>`
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

func TestGridSparsePlacementAndSpanningRowHeights(t *testing.T) {
	input := `<style>.item{background:red;font-size:10pt;line-height:20pt}</style><section style="display:grid;grid-template-columns:100pt 100pt;gap:10pt"><div class="item" style="grid-row:1 / span 2;grid-column:1">A<br>B<br>C</div><div class="item" style="grid-column:2">D</div><div class="item" style="grid-column:2">E</div></section>`
	r, err := renderLayout(t, input, 300)
	if err != nil {
		t.Fatal(err)
	}
	boxes := flexItemBoxes(r)
	if len(boxes) != 3 {
		t.Fatal(boxes)
	}
	closeFlex(t, boxes[0].Height, 60)
	closeFlex(t, boxes[1].Height, 25)
	closeFlex(t, boxes[2].Y, 75)
	closeFlex(t, boxes[2].Height, 25)
	var group bool
	for _, a := range r.Atoms {
		if a.Bounds.X == 40 && a.Bounds.Width == 300 && a.Bounds.Height == 60 {
			group = true
		}
	}
	if !group {
		t.Fatal("span-connected group not protected")
	}
	// Later definite and row-locked items reserve cells before automatic items.
	r, err = renderLayout(t, `<section style="display:grid;grid-template-columns:100pt 100pt"><div style="background:red">Auto</div><div style="background:red;grid-column:1;grid-row:1">Locked</div><div style="background:red;grid-row:2">Row locked</div></section>`, 300)
	if err != nil {
		t.Fatal(err)
	}
	boxes = flexItemBoxes(r)
	closeFlex(t, boxes[0].X, 140)
	closeFlex(t, boxes[1].X, 40)
	closeFlex(t, boxes[2].X, 40)
	if boxes[2].Y <= boxes[0].Y {
		t.Fatal(boxes)
	}
}

func TestGridAnonymousTextTablesAndHiddenItems(t *testing.T) {
	r, err := renderLayout(t, `<section style="display:grid;grid-template-columns:1fr 1fr">Alpha <span>Beta</span><span style="display:none">Hidden</span></section>`, 300)
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, l := range r.Lines {
		texts = append(texts, l.Text)
	}
	if strings.Join(texts, " ") != "Alpha Beta" {
		t.Fatal(texts)
	}
	if _, err := renderLayout(t, `<section style="display:grid;grid-template-columns:1fr 1fr"><div><table style="width:100%;table-layout:fixed"><tr><td>Inside</td></tr></table></div><div>Outside</div></section>`, 300); err != nil {
		t.Fatal(err)
	}
	if _, err := renderLayout(t, `<table style="width:300pt;table-layout:fixed"><tr><td><section style="display:grid;grid-template-columns:1fr 1fr"><div>A</div><div>B</div></section></td></tr></table>`, 300); err != nil {
		t.Fatal(err)
	}
}

func TestGridUnsupportedAndOverflow(t *testing.T) {
	for _, input := range []string{
		`<div style="display:grid">Missing tracks</div>`,
		`<div style="display:grid;grid-template-columns:1fr"><div style="grid-column:2">Implicit</div></div>`,
		`<div style="display:grid;grid-template-columns:1fr"><div style="grid-row:1;grid-column:1">A</div><div style="grid-row:1;grid-column:1">B</div></div>`,
		`<div style="display:grid;grid-template-columns:1fr"><div style="height:50%">Cyclic height</div></div>`,
		`<div style="display:grid;grid-template-columns:1fr"><div style="margin-left:auto">Auto margin</div></div>`,
		`<div style="display:grid;grid-template-columns:1fr"><div style="display:flex">Nested</div></div>`,
		`<div style="display:flex"><div style="display:grid;grid-template-columns:1fr">Nested</div></div>`,
		`<div style="display:grid;grid-template-columns:1fr"><div><div style="display:grid;grid-template-columns:1fr">Nested</div></div></div>`,
		`<table><tr><td><div style="display:grid;grid-template-columns:1fr"><div>Intrinsic</div></div></td></tr></table>`,
	} {
		if _, err := renderLayout(t, input, 300); !errors.Is(err, session.ErrUnsupportedFeature) {
			t.Fatalf("%s: %v", input, err)
		}
	}
	for _, input := range []string{
		`<div style="display:grid;grid-template-columns:50% 50%;gap:1pt"><div>A</div></div>`,
		`<div style="display:grid;grid-template-columns:1fr"><div style="min-width:400pt">Too wide</div></div>`,
		`<div style="display:grid;grid-template-columns:1fr"><div style="height:1pt">Cannot clip</div></div>`,
	} {
		if _, err := renderLayout(t, input, 300); !errors.Is(err, session.ErrUnbreakableContent) {
			t.Fatalf("%s: %v", input, err)
		}
	}
}

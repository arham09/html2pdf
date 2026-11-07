package layout

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	"image/png"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/arham09/html2pdf/internal/paint"
	"github.com/arham09/html2pdf/internal/session"
	"github.com/arham09/html2pdf/internal/style"
)

func closeTable(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 0.0001 {
		t.Fatalf("got %.6f, want %.6f", got, want)
	}
}
func tableText(result Result) string {
	var out strings.Builder
	for _, op := range result.Page.Operations {
		if op.Kind == paint.DrawGlyphRun {
			out.WriteString(op.Glyphs.Text)
		}
	}
	return out.String()
}

func TestFixedTableColumnsRowsAndSpans(t *testing.T) {
	result, err := renderLayout(t, `<style>table{width:300pt;table-layout:fixed}th,td{padding:4pt;border:1pt solid #888}th{width:90pt} .number{text-align:right}</style><table><thead><tr><th>Description</th><th>Qty</th><th>Amount</th></tr></thead><tbody><tr><td>A long description with <strong>bold</strong> words wraps inside this column.</td><td class="number">2</td><td class="number">1.234,56</td></tr><tr><td colspan="2">Total</td><td class="number">1.234,56</td></tr></tbody></table>`, 400)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Tables) != 1 {
		t.Fatal("missing table")
	}
	table := result.Tables[0]
	for _, w := range table.Columns {
		closeTable(t, w, 100)
	}
	if len(table.Rows) != 3 || !table.Rows[0].Header || table.Rows[1].Header {
		t.Fatal(table.Rows)
	}
	ids := map[SourceID]bool{}
	for i, r := range table.Rows {
		if ids[r.Source] {
			t.Fatal("duplicated row")
		}
		ids[r.Source] = true
		closeTable(t, r.Bounds.Width, 300)
		if i > 0 {
			closeTable(t, r.Bounds.Y, table.Rows[i-1].Bounds.Y+table.Rows[i-1].Bounds.Height)
		}
		for _, c := range r.Cells {
			closeTable(t, c.Bounds.Height, r.Bounds.Height)
		}
	}
	if table.Rows[1].Bounds.Height <= table.Rows[0].Bounds.Height*2 {
		t.Fatal("description not wrapped")
	}
	closeTable(t, table.Rows[2].Cells[0].Bounds.Width, 200)
	number := table.Rows[1].Cells[2]
	found := false
	for _, line := range result.Lines {
		if line.Text == "1.234,56" && line.Bounds.Y < number.Bounds.Y+number.Bounds.Height {
			closeTable(t, line.Bounds.X+line.Bounds.Width, number.Bounds.X+number.Bounds.Width-5)
			found = true
		}
	}
	if !found {
		t.Fatal("amount line missing")
	}
	if !strings.Contains(strings.ReplaceAll(tableText(result), " ", ""), "Alongdescriptionwithboldwordswrapsinsidethiscolumn.") || len(result.Page.Fonts) != 2 {
		t.Fatal("mixed text lost")
	}
	rowBreaks := 0
	for _, br := range result.Breaks {
		if br.Kind == RowBoundary {
			rowBreaks++
			if br.Offset != rowBreaks || br.Source != table.Rows[rowBreaks-1].Source {
				t.Fatal(br)
			}
		} else if br.Kind == LineBoundary && !br.Avoid {
			t.Fatal("cell interior is not atomic")
		}
	}
	if rowBreaks != 3 {
		t.Fatal("row opportunities", rowBreaks)
	}
}

func TestAutoTableIntrinsicWidthsAndEmptyCells(t *testing.T) {
	result, err := renderLayout(t, `<style>table{width:300pt}td{padding:3pt;border:1pt solid black}.n{text-align:right}</style><table><tr><td><p>Several descriptive words here</p></td><td class="n">42,50</td></tr><tr><td colspan="2">A much longer explanatory note across both columns</td></tr><tr><td></td><td class="n">0</td></tr></table>`, 400)
	if err != nil {
		t.Fatal(err)
	}
	table := result.Tables[0]
	closeTable(t, table.Columns[0]+table.Columns[1], 300)
	if table.Columns[0] <= table.Columns[1] {
		t.Fatal("intrinsic text widths ignored", table.Columns)
	}
	closeTable(t, table.Rows[1].Cells[0].Bounds.Width, 300)
	if len(table.Rows[2].Cells) != 2 || table.Rows[2].Cells[0].ContentBounds.Height != 0 {
		t.Fatal("empty cell lost")
	}
	if !strings.Contains(tableText(result), "42,50") || !strings.Contains(strings.ReplaceAll(tableText(result), " ", ""), "noteacrossbothcolumns") {
		t.Fatal(tableText(result))
	}
}

func TestTableBorderModelsAndCollapseConflicts(t *testing.T) {
	for _, mode := range []string{"separate", "collapse"} {
		t.Run(mode, func(t *testing.T) {
			result, err := renderLayout(t, `<style>table{width:200pt;table-layout:fixed;border-collapse:`+mode+`}td{border:1pt solid red}.left{border-right:3pt solid blue}</style><table><tr><td class="left">A</td><td>B</td></tr><tr><td colspan="2">C</td></tr></table>`, 300)
			if err != nil {
				t.Fatal(err)
			}
			rects := 0
			shared := 0
			for _, op := range result.Page.Operations {
				if op.Kind != paint.FillRect {
					continue
				}
				rects++
				if mode == "collapse" && math.Abs(op.Bounds.X-138.5) < 0.001 && op.Bounds.Width == 3 {
					shared++
					if op.Color != (style.Color{B: 255, A: 255}) {
						t.Fatal("wrong border winner")
					}
				}
			}
			want := 12
			if mode == "collapse" {
				want = 11
				if shared != 1 {
					t.Fatal("shared border paint count", shared)
				}
			}
			if rects != want {
				t.Fatal("border segment count", rects, want)
			}
		})
	}
	result, err := renderLayout(t, `<table style="width:200pt;border-collapse:collapse;border:4pt solid blue"><tr><td style="border:1pt solid red">A</td></tr></table>`, 300)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range result.Page.Operations {
		if op.Kind == paint.FillRect && op.Color != (style.Color{B: 255, A: 255}) {
			t.Fatal("table width priority failed")
		}
	}
	cell := result.Tables[0].Rows[0].Cells[0]
	closeTable(t, cell.ContentBounds.X, cell.Bounds.X+4)
	// Equal widths: cells beat the table, and the later cell wins a shared edge.
	result, err = renderLayout(t, `<table style="width:200pt;border-collapse:collapse;border:1pt solid blue"><tr><td style="border:1pt solid red">A</td><td style="border:1pt solid green">B</td></tr></table>`, 300)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range result.Page.Operations {
		if op.Kind == paint.FillRect && op.Color == (style.Color{B: 255, A: 255}) {
			t.Fatal("cell tie priority failed")
		}
	}
}

func TestTableHeaderOrderingBackgroundsAndTranslation(t *testing.T) {
	result, err := renderLayout(t, `<div style="display:inline-block;width:300pt;padding:5pt"><table style="width:100%;table-layout:fixed"><tbody style="background:#eee"><tr style="background:#ddd"><td>Body</td><td>2</td></tr></tbody><thead style="background:#abc"><tr><th>Header</th><th>Count</th></tr></thead><tbody><tr><td>Tail</td></tr></tbody></table></div>`, 400)
	if err != nil {
		t.Fatal(err)
	}
	table := result.Tables[0]
	closeTable(t, table.Bounds.X, 45)
	closeTable(t, table.Bounds.Y, 45)
	if !table.Rows[0].Header || table.Rows[1].Header || len(table.Rows[2].Cells) != 1 {
		t.Fatal(table.Rows)
	}
	if tableText(result) != "HeaderCountBody2Tail" {
		t.Fatal("source order", tableText(result))
	}
	closeTable(t, table.Rows[0].Cells[1].Bounds.X, 195)
	if len(result.Tables) != 1 {
		t.Fatal("duplicated local table")
	}
	fills := 0
	for _, op := range result.Page.Operations {
		if op.Kind == paint.FillRect {
			fills++
			if op.Bounds.X < 45 {
				t.Fatal("untranslated background")
			}
		}
	}
	if fills != 3 {
		t.Fatal("group/row backgrounds", fills)
	}
}

func TestTableWidthsAndUnsupportedCases(t *testing.T) {
	cases := []struct {
		html string
		want error
	}{
		{`<table style="table-layout:fixed"><tr><td>A</td></tr></table>`, session.ErrUnsupportedFeature},
		{`<table><tr><td rowspan="0">A</td></tr></table>`, session.ErrUnsupportedFeature},
		{`<table><tr><td colspan="0">A</td></tr></table>`, session.ErrInvalidInput},
		{`<table><tr><td colspan="1001">A</td></tr></table>`, session.ErrInvalidInput},
		{`<table><tr><td><table><tr><td>A</td></tr></table></td></tr></table>`, session.ErrUnsupportedFeature},
		{`<table><tfoot><tr><td>Total</td></tr></tfoot></table>`, session.ErrUnsupportedFeature},
		{`<table><colgroup><col></colgroup><tr><td>A</td></tr></table>`, session.ErrUnsupportedFeature},
		{`<table><caption>Title</caption><tr><td>A</td></tr></table>`, session.ErrUnsupportedFeature},
		{`<table><tr style="border:1pt solid black"><td>A</td></tr></table>`, session.ErrUnsupportedFeature},
		{`<table><tr><td style="height:40pt">A</td></tr></table>`, session.ErrUnsupportedFeature},
		{`<table><tr><td><span><img style="width:50%" src="unused"></span></td></tr></table>`, session.ErrUnsupportedFeature},
		{`<table style="width:100pt;table-layout:fixed"><tr><td style="width:80pt">A</td><td style="width:80pt">B</td></tr></table>`, session.ErrUnbreakableContent},
		{`<table style="width:50pt"><tr><td>extraordinarilylongunbreakableword</td></tr></table>`, session.ErrUnbreakableContent},
		{`<table style="width:30pt;table-layout:fixed"><tr><td style="padding:20pt">A</td></tr></table>`, session.ErrUnbreakableContent},
	}
	for _, c := range cases {
		if _, err := renderLayout(t, c.html, 400); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", c.html, err, c.want)
		}
	}
	result, err := renderLayout(t, `<table style="width:200pt;table-layout:fixed"><tr><td style="width:25%">A</td><td>B</td></tr><tr><td style="width:999pt">C</td><td>D</td></tr></table>`, 400)
	if err != nil {
		t.Fatal(err)
	}
	closeTable(t, result.Tables[0].Columns[0], 50)
	closeTable(t, result.Tables[0].Columns[1], 150)
	if _, err := renderLayout(t, `<table style="width:200pt;table-layout:fixed">`+strings.Repeat(`<tr><td>Row</td></tr>`, 100)+`</table>`, 400); !errors.Is(err, session.ErrUnbreakableContent) {
		t.Fatal("row overflow", err)
	}
}

func TestEmptyAndHiddenTableRows(t *testing.T) {
	result, err := renderLayout(t, `<table><thead><tr></tr></thead><tbody><tr></tr><tr style="display:none"><td>Hidden</td></tr></tbody></table><table></table>`, 300)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Tables) != 2 || len(result.Tables[0].Rows) != 2 || len(result.Tables[1].Rows) != 0 {
		t.Fatal(result.Tables)
	}
}

func TestFixedSpanHintsAndStableGeometry(t *testing.T) {
	input := `<table style="width:300pt;table-layout:fixed"><thead><tr><th colspan="2" style="width:200pt">Group</th><th>Amount</th></tr></thead><tbody><tr><td>A</td><td>B</td><td style="text-align:right">42</td></tr></tbody></table>`
	first, err := renderLayout(t, input, 400)
	if err != nil {
		t.Fatal(err)
	}
	for _, width := range first.Tables[0].Columns {
		closeTable(t, width, 100)
	}
	second, err := renderLayout(t, input, 400)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first.Tables, second.Tables) || !reflect.DeepEqual(first.Lines, second.Lines) || !reflect.DeepEqual(first.Breaks, second.Breaks) {
		t.Fatal("table geometry or source identity is unstable")
	}
}

func TestFixedTableCellBlocksAndImages(t *testing.T) {
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewNRGBA(image.Rect(0, 0, 2, 1))); err != nil {
		t.Fatal(err)
	}
	input := `<table style="width:200pt;table-layout:fixed"><tr><td><div style="width:50%;padding:2pt;background:#eee"><img width="20" src="data:image/png;base64,` + base64.StdEncoding.EncodeToString(data.Bytes()) + `"></div><p>Below image</p></td><td>Other</td></tr></table>`
	result, err := renderLayout(t, input, 300)
	if err != nil {
		t.Fatal(err)
	}
	images := 0
	for _, op := range result.Page.Operations {
		if op.Kind == paint.DrawImage {
			images++
			closeTable(t, op.Bounds.Width, 15)
			closeTable(t, op.Bounds.Height, 7.5)
			cell := result.Tables[0].Rows[0].Cells[0]
			if op.Bounds.X < cell.ContentBounds.X || op.Bounds.Y < cell.ContentBounds.Y {
				t.Fatal("cell image translation")
			}
		}
	}
	if images != 1 || !strings.Contains(tableText(result), "Below image") {
		t.Fatal("cell content missing")
	}
}

func TestCollapsedRaggedEmptyRowAndBorderBounds(t *testing.T) {
	result, err := renderLayout(t, `<table style="width:200pt;border:4pt solid blue;border-collapse:collapse"><tr></tr><tr><td style="border-top:3pt solid red">A</td><td>B</td></tr><tr><td>C</td></tr></table>`, 300)
	if err != nil {
		t.Fatal(err)
	}
	table := result.Tables[0]
	if len(table.Rows) != 3 || len(table.Rows[0].Cells) != 0 || table.Rows[0].Bounds.Height < 4 {
		t.Fatal(table.Rows)
	}
	for _, op := range result.Page.Operations {
		if op.Kind == paint.FillRect {
			r := op.Bounds
			b := table.Bounds
			if r.X < b.X-.0001 || r.Y < b.Y-.0001 || r.X+r.Width > b.X+b.Width+.0001 || r.Y+r.Height > b.Y+b.Height+.0001 {
				t.Fatal("border outside grid", r, b)
			}
		}
	}
	if _, err := renderLayout(t, `<table style="width:30pt;table-layout:fixed;border-collapse:collapse;border:100pt solid black"><tr><td></td></tr></table>`, 300); !errors.Is(err, session.ErrUnbreakableContent) {
		t.Fatal("oversized table border", err)
	}
	if _, err := renderLayout(t, `<table style="border-collapse:collapse;border:1pt solid black"></table>`, 300); !errors.Is(err, session.ErrUnsupportedFeature) {
		t.Fatal("empty collapsed border", err)
	}
}

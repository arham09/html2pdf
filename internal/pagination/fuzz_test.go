package pagination

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/arham09/html2pdf/internal/css"
	"github.com/arham09/html2pdf/internal/font"
	dom "github.com/arham09/html2pdf/internal/html"
	"github.com/arham09/html2pdf/internal/layout"
	"github.com/arham09/html2pdf/internal/resource"
	"github.com/arham09/html2pdf/internal/session"
)

func FuzzTableProgress(f *testing.F) {
	f.Add(uint8(20), uint8(2), uint8(1), uint8(100), true)
	f.Add(uint8(1), uint8(50), uint8(3), uint8(20), false)
	f.Fuzz(func(t *testing.T, rows, lines, span, height uint8, avoid bool) {
		s, _ := session.New(context.Background(), session.Limits{MaxPages: 12, MaxLayoutWork: 60000, MaxGlyphs: 10000, MaxDisplayOps: 10000, MaxDOMNodes: 5000})
		var html strings.Builder
		fmt.Fprintf(&html, `<style>body{font-size:9pt;line-height:14pt}table{width:200pt;table-layout:fixed;border-collapse:collapse;%s}td,th{padding:2pt;border:1pt solid black}</style><table><thead><tr><th>H</th></tr></thead><tbody>`, map[bool]string{true: "break-inside:avoid", false: ""}[avoid])
		for i := 0; i < int(rows%60); i++ {
			fmt.Fprintf(&html, `<tr><td colspan="%d">%sR-%d</td></tr>`, 1+span%8, strings.Repeat("A<br>", int(lines%15)), i)
		}
		html.WriteString(`</tbody></table>`)
		tree, err := dom.Parse(s, html.String())
		if err != nil {
			return
		}
		var sheets css.Sheet
		for _, text := range tree.Stylesheets {
			if err := sheets.Add(s, text, true); err != nil {
				return
			}
		}
		if _, err := sheets.Compute(s, tree, true); err != nil {
			return
		}
		body := layout.Rect{X: 20, Y: 20, Width: 200, Height: float64(height) + 1}
		flow, err := layout.BuildFlow(s, tree, font.NewRegistry(s, nil), resource.Loader{Session: s}, 240, body.Height+40, body)
		if err != nil {
			return
		}
		pages, err := Build(s, flow, body)
		if err != nil {
			return
		}
		seen := map[layout.SourceID]bool{}
		for _, page := range pages {
			if page.FlowEnd < page.FlowStart {
				t.Fatal("negative progress")
			}
			for _, fragment := range page.Fragments {
				if fragment.RepeatedDecoration {
					continue
				}
				if seen[fragment.Content.Source] {
					t.Fatal("duplicate source row")
				}
				seen[fragment.Content.Source] = true
			}
		}
		if len(pages) > 12 || len(pages) == 0 {
			t.Fatal("page bound")
		}
		if len(seen) != int(rows%60)+1 {
			t.Fatal("lost table rows", len(seen), int(rows%60)+1)
		}
	})
}

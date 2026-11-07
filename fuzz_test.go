package html2pdf_test

import (
	"bytes"
	"context"
	pdf "github.com/arham09/html2pdf"
	"testing"
)

func FuzzRender(f *testing.F) {
	for _, seed := range []string{`<section style="display:grid;grid-template-columns:80px 1fr;gap:2pt"><div style="grid-row:span 2">Grid</div><div>Alpha</div><div>Beta</div></section>`, `<section style="display:grid;grid-template-columns:1fr 1fr"><div style="grid-column:1;grid-row:1">Collision</div><div style="grid-column:1;grid-row:1">Denied</div></section>`, `<section style="display:flex;flex-wrap:wrap;gap:2pt"><div style="flex:1 1 0;min-width:0">Alpha</div><div style="flex:2 1 0;min-width:0">Beta</div></section>`, `<section style="display:flex;flex-direction:column;height:100pt"><div style="flex:1">Column</div></section>`, `<p>café é</p>`, `<table><thead><tr><th>H</th></tr></thead><tr><td>A</td></tr></table>`, `<p style="break-inside:avoid">A<br>B</p><p style="break-before:page">C</p>`, `<img src="data:;base64,AAAA">`, `<style>p{bad:1}</style>Hi`, `<script>throw 1</script>Static`, "\xff"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > 4096 {
			return
		}
		e, err := pdf.New(pdf.Options{Limits: pdf.Limits{MaxHTMLBytes: 4096, MaxCSSBytes: 4096, MaxDOMNodes: 3000, MaxDOMDepth: 48, MaxLayoutWork: 40000, MaxGlyphs: 10000, MaxDisplayOps: 10000, MaxPages: 8, MaxOutputBytes: 1 << 20, MaxWarnings: 20, MaxImagePixels: 4096, MaxDecodedBytes: 1 << 20, MaxAssetBytes: 8192}})
		if err != nil {
			t.Fatal(err)
		}
		var first bytes.Buffer
		r, err := e.Render(context.Background(), pdf.Document{HTML: input}, &first)
		if err != nil {
			if first.Len() != 0 {
				t.Fatal("failed bounded render wrote bytes", err)
			}
			return
		}
		if r.Pages < 1 || r.Pages > 8 || !bytes.HasPrefix(first.Bytes(), []byte("%PDF-1.7")) || !bytes.Contains(first.Bytes(), []byte("%%EOF")) {
			t.Fatal("incomplete PDF")
		}
		var second bytes.Buffer
		r2, err := e.Render(context.Background(), pdf.Document{HTML: input}, &second)
		if err != nil || r2.Pages != r.Pages || !bytes.Equal(first.Bytes(), second.Bytes()) {
			t.Fatal("render not reproducible", err)
		}
	})
}

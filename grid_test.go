package html2pdf

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestGridPublicErrorsAndQuotasBeforeOutput(t *testing.T) {
	for _, tc := range []struct {
		html string
		want error
	}{
		{`<section style="display:grid;grid-template-columns:1fr"><div style="grid-column:1;grid-row:1">A</div><div style="grid-column:1;grid-row:1">B</div></section>`, ErrUnsupportedFeature},
		{`<section style="display:grid;grid-template-columns:1fr"><div style="grid-column:2">Implicit column</div></section>`, ErrUnsupportedFeature},
		{`<section style="display:grid;grid-template-columns:50% 50%;gap:10pt"><div>A</div></section>`, ErrUnbreakableContent},
		{`<section style="display:grid;grid-template-columns:1fr"><div>` + strings.Repeat(`Oversized<br>`, 100) + `</div></section>`, ErrUnbreakableContent},
		{`<section style="display:grid;grid-template-columns:1fr"><div style="break-before:page">Break inside item</div></section>`, ErrUnsupportedFeature},
	} {
		e, _ := New(Options{})
		var out bytes.Buffer
		if _, err := e.Render(context.Background(), Document{HTML: tc.html}, &out); !errors.Is(err, tc.want) || out.Len() != 0 {
			t.Fatal(tc.html, err, out.Len())
		}
	}
	input := `<section style="display:grid;grid-template-columns:1fr 1fr;gap:4pt">` + strings.Repeat(`<div>Quota card</div>`, 30) + `</section>`
	for _, limits := range []Limits{{MaxLayoutWork: 500}, {MaxGlyphs: 5}, {MaxDisplayOps: 5}, {MaxOutputBytes: 20}} {
		e, _ := New(Options{Limits: limits})
		var out bytes.Buffer
		if _, err := e.Render(context.Background(), Document{HTML: input}, &out); !errors.Is(err, ErrLimitExceeded) || out.Len() != 0 {
			t.Fatal(limits, err, out.Len())
		}
	}
	e, _ := New(Options{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out bytes.Buffer
	if _, err := e.Render(ctx, Document{HTML: input}, &out); !errors.Is(err, context.Canceled) || out.Len() != 0 {
		t.Fatal(err)
	}
	// A sparse explicit placement cannot allocate a huge occupancy plane for free.
	e, _ = New(Options{Limits: Limits{MaxLayoutWork: 500}})
	out.Reset()
	if _, err := e.Render(context.Background(), Document{HTML: `<section style="display:grid;grid-template-columns:` + strings.Repeat(`1fr `, 64) + `"><div style="grid-row:1 / span 1000;grid-column:1 / span 64"></div></section>`}, &out); !errors.Is(err, ErrLimitExceeded) || out.Len() != 0 {
		t.Fatal("occupancy work quota", err, out.Len())
	}
}

func TestSharedEngineGridDeterministicAndPaginated(t *testing.T) {
	e, err := New(Options{StrictCSS: true, PageSize: &PaperSize{340, 180}, Margins: &Margins{20, 20, 20, 20}})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 24 {
		var cards strings.Builder
		for j := range 20 {
			fmt.Fprintf(&cards, `<div>REQ-%02d-CARD-%02d<br>Rp 1.234,56</div>`, i, j)
		}
		input := `<section style="display:grid;grid-template-columns:1fr 1fr;gap:4pt;font-size:9pt;line-height:14pt">` + cards.String() + `</section>`
		var expected bytes.Buffer
		r, err := e.Render(context.Background(), Document{HTML: input}, &expected)
		if err != nil || r.Pages < 2 {
			t.Fatal(r, err)
		}
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			t.Parallel()
			var actual bytes.Buffer
			r2, err := e.Render(context.Background(), Document{HTML: input}, &actual)
			if err != nil || r2.Pages != r.Pages || !bytes.Equal(expected.Bytes(), actual.Bytes()) {
				t.Fatal("Grid shared-engine output differs", r2, err)
			}
		})
	}
}

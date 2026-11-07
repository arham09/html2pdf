package html2pdf

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestPublicFlexFailuresAreAtomic(t *testing.T) {
	for _, tc := range []struct {
		html string
		want error
	}{
		{`<section style="display:flex"><div style="display:flex">Nested</div></section>`, ErrUnsupportedFeature},
		{`<section style="display:flex;flex-direction:column">` + strings.Repeat(`<div>Line</div>`, 100) + `</section>`, ErrUnbreakableContent},
		{`<section style="display:flex"><div style="height:1pt">Cannot clip</div></section>`, ErrUnbreakableContent},
		{`<section style="display:flex;align-items:baseline">Unsupported</section>`, ErrUnsupportedFeature},
	} {
		e, _ := New(Options{})
		var out bytes.Buffer
		if _, err := e.Render(context.Background(), Document{HTML: tc.html}, &out); !errors.Is(err, tc.want) || out.Len() != 0 {
			t.Fatalf("%s: %v, bytes=%d", tc.html, err, out.Len())
		}
	}
	input := `<section style="display:flex;flex-wrap:wrap;gap:4pt">` + strings.Repeat(`<div style="flex:1 1 50pt">Content</div>`, 30) + `</section>`
	for _, limits := range []Limits{{MaxLayoutWork: 500}, {MaxGlyphs: 10}, {MaxDisplayOps: 5}, {MaxOutputBytes: 20}} {
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
}

func TestSharedEngineFlexDeterminism(t *testing.T) {
	e, err := New(Options{StrictCSS: true})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 24 {
		input := fmt.Sprintf(`<section style="display:flex;gap:12pt;align-items:center"><div style="flex:1 1 0;min-width:0">ISSUER %03d</div><div style="flex:2 1 0;min-width:0;text-align:right">CUSTOMER %03d</div></section><table style="width:100%%;table-layout:fixed"><tr><td>Invoice</td><td>Rp 1.234,56</td></tr></table>`, i, i)
		var expected bytes.Buffer
		if _, err := e.Render(context.Background(), Document{HTML: input}, &expected); err != nil {
			t.Fatal(err)
		}
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			t.Parallel()
			var actual bytes.Buffer
			r, err := e.Render(context.Background(), Document{HTML: input}, &actual)
			if err != nil || r.Pages != 1 || !bytes.Equal(expected.Bytes(), actual.Bytes()) {
				t.Fatal("concurrent flex differs", r, err)
			}
		})
	}
}

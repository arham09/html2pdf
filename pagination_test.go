package html2pdf

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"testing"
)

func TestPublicMultipageAndReservationFailures(t *testing.T) {
	options := Options{PageSize: &PaperSize{340, 160}, Margins: &Margins{20, 20, 20, 20}, Header: HeaderFooter{HTML: `<div style="font-size:8pt;line-height:12pt">Page {page} of {pages}</div>`, HeightPt: 20}, Footer: HeaderFooter{HTML: `<div style="font-size:8pt;line-height:12pt">Footer</div>`, HeightPt: 20}}
	engine, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	input := Document{HTML: `<style>body{font-size:10pt;line-height:20pt}p{margin:0}</style><p>A<br>B<br>C<br>D<br>E<br>F</p>`}
	var out bytes.Buffer
	result, err := engine.Render(context.Background(), input, &out)
	if err != nil || result.Pages != 2 || len(regexp.MustCompile(`/Type\s*/Page\b`).FindAll(out.Bytes(), -1)) != 2 {
		t.Fatal("multipage public PDF", result, err)
	}
	for _, limits := range []Limits{{MaxPages: 1}, {MaxDisplayOps: 1}, {MaxOutputBytes: 20}} {
		options.Limits = limits
		e, err := New(options)
		if err != nil {
			t.Fatal(err)
		}
		out.Reset()
		if _, err := e.Render(context.Background(), input, &out); !errors.Is(err, ErrLimitExceeded) {
			t.Fatal("multipage quota", limits, err)
		}
		if limits.MaxPages == 1 && out.Len() != 0 {
			t.Fatal("page quota wrote output")
		}
	}
	options.Limits = Limits{}
	options.Footer.HTML = `<div style="font-family:Missing">Missing</div>`
	e, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if _, err := e.Render(context.Background(), input, &out); !errors.Is(err, ErrMissingResource) || out.Len() != 0 {
		t.Fatal("late decoration preflight", err)
	}
	// A row larger than the usable body remains indivisible after reservation.
	options.Footer.HTML = ""
	e, err = New(options)
	if err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if _, err := e.Render(context.Background(), Document{HTML: `<table><tr><td>` + strings.Repeat("Line<br>", 20) + `</td></tr></table>`}, &out); !errors.Is(err, ErrUnbreakableContent) || out.Len() != 0 {
		t.Fatal("oversized row", err)
	}
}
func TestConcurrentMultipageDecorations(t *testing.T) {
	engine, err := New(Options{Footer: HeaderFooter{HTML: `<div style="font-size:8pt;line-height:12pt">Page {page} of {pages}</div>`, HeightPt: 20}})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 20 {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			t.Parallel()
			doc := Document{HTML: `<p>First</p><p style="break-before:page">Second</p>`}
			r, err := engine.Render(context.Background(), doc, io.Discard)
			if err != nil || r.Pages != 2 {
				t.Fatal(r, err)
			}
		})
	}
}

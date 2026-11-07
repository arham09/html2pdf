package html2pdf

import (
	"bytes"
	"context"
	"errors"
	"testing"

	dom "github.com/arham09/html2pdf/internal/html"
)

func TestPreparePagePrecedenceAndDiagnostics(t *testing.T) {
	e, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	s, _, _ := e.start(context.Background())
	p, err := e.prepare(s, Document{HTML: `<style>@page{size:letter;margin:0}</style><p>Hello</p>`})
	if err != nil {
		t.Fatal(err)
	}
	if p.Page.WidthPt != 612 || p.Margins.LeftPt != 0 {
		t.Fatal(p)
	}
	page, margin := A4(), Margins{LeftPt: 10}
	e, _ = New(Options{PageSize: &page, Margins: &margin})
	s, _, _ = e.start(context.Background())
	p, err = e.prepare(s, Document{HTML: `<style>@page{size:letter;margin:0}</style>Hi`})
	if err != nil {
		t.Fatal(err)
	}
	if p.Page != page || p.Margins != margin {
		t.Fatal("API values lost precedence")
	}
	for _, strict := range []bool{false, true} {
		e, _ = New(Options{StrictCSS: strict})
		var dst bytes.Buffer
		result, err := e.Render(context.Background(), Document{HTML: `<p style="box-shadow:1px 1px black">Hi</p>`}, &dst)
		if strict {
			if !errors.Is(err, ErrUnsupportedFeature) || dst.Len() != 0 {
				t.Fatal(err)
			}
		} else if err != nil || result.Pages != 1 || dst.Len() == 0 {
			t.Fatal(err)
		}
		if !strict && len(result.Warnings) != 1 {
			t.Fatal("parse warnings lost on unsupported layout", result)
		}
	}
}

func TestPreparationPreservesFontAndDecorationIsolation(t *testing.T) {
	e, err := New(Options{Header: HeaderFooter{HTML: `<p style="font-size:9pt">Page {page}</p>`, HeightPt: 20}})
	if err != nil {
		t.Fatal(err)
	}
	s, _, _ := e.start(context.Background())
	p, err := e.prepare(s, Document{HTML: `<style>p{color:red}</style><p>Body</p>`})
	if err != nil {
		t.Fatal(err)
	}
	if p.Header == nil || p.Footer != nil {
		t.Fatal("decoration missing")
	}
	ids := map[uint64]bool{}
	for _, tree := range []*dom.Document{p.Body, p.Header} {
		for _, n := range tree.Nodes {
			if ids[n.ID] {
				t.Fatal("duplicate source ID across body/decorations")
			}
			ids[n.ID] = true
		}
	}
	e, _ = New(Options{Limits: Limits{MaxCSSBytes: 3}})
	_, err = e.Render(context.Background(), Document{HTML: `<style>p{color:red}</style>`}, &bytes.Buffer{})
	if !errors.Is(err, ErrLimitExceeded) {
		t.Fatal("embedded styles bypass CSS quota", err)
	}
}

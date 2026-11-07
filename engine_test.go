package html2pdf

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"os"
	"testing"

	"github.com/arham09/html2pdf/internal/resource"
	"github.com/arham09/html2pdf/internal/session"
)

func TestOptionsAndOwnership(t *testing.T) {
	page, margins := Letter(), Margins{}
	data, err := os.ReadFile("internal/font/assets/NotoSans-Regular.ttf")
	if err != nil {
		t.Fatal(err)
	}
	faces := []FontFace{{Family: "Noto", Data: data}}
	e, err := New(Options{PageSize: &page, Margins: &margins, Fonts: faces})
	if err != nil {
		t.Fatal(err)
	}
	data[0] = 99
	faces[0].Family = "changed"
	page.WidthPt = 1
	margins.LeftPt = 100
	if e.options.PageSize.WidthPt != 612 || e.options.Margins.LeftPt != 0 || e.options.Fonts[0].Data[0] != 0 || e.options.Fonts[0].Family != "Noto" {
		t.Fatal("caller mutation escaped into Engine")
	}
	if e.options.Fonts[0].Weight != 400 {
		t.Fatal("missing weight default")
	}
	d, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	_, m := resolvedPage(d.options)
	if m.LeftPt <= 0 || d.options.Margins != nil {
		t.Fatal("omitted margins must retain CSS/default precedence")
	}
	for _, opts := range []Options{
		{PageSize: &PaperSize{WidthPt: math.NaN(), HeightPt: 100}},
		{PageSize: &PaperSize{}}, {Margins: &Margins{LeftPt: 1000}},
		{Header: HeaderFooter{HTML: "header"}}, {Limits: Limits{MaxPages: -1}},
		{Fonts: []FontFace{{Family: "x", Data: []byte("invalid")}}},
	} {
		if _, err := New(opts); err == nil {
			t.Fatalf("accepted invalid options %+v", opts)
		}
	}
}

func TestRenderPolicyAndSinglePageOutput(t *testing.T) {
	e, err := New(Options{Limits: Limits{MaxHTMLBytes: 4, MaxCSSBytes: 3, MaxStylesheets: 2}})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		doc      Document
		category error
	}{
		{Document{HTML: "abcd"}, nil},
		{Document{HTML: "abcde"}, ErrLimitExceeded},
		{Document{HTML: "\xff"}, ErrInvalidInput},
		{Document{Stylesheets: []string{"ab", "cd"}}, ErrLimitExceeded},
		{Document{Stylesheets: []string{"a", "b", "c"}}, ErrLimitExceeded},
		{Document{Stylesheets: []string{"\xff"}}, ErrInvalidInput},
	}
	for _, tc := range tests {
		var output bytes.Buffer
		r, err := e.Render(context.Background(), tc.doc, &output)
		if tc.category == nil {
			if err != nil || r.Pages != 1 || !bytes.HasPrefix(output.Bytes(), []byte("%PDF-")) {
				t.Fatalf("render failed: %v %+v", err, r)
			}
			continue
		}
		if !errors.Is(err, tc.category) || output.Len() != 0 || r.Pages != 0 {
			t.Fatalf("doc=%+v result=%+v err=%v output=%d", tc.doc, r, err, output.Len())
		}
		if errors.Is(err, ErrLimitExceeded) {
			var quota *LimitError
			if !errors.As(err, &quota) {
				t.Fatal("missing typed limit details")
			}
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.Render(ctx, Document{}, io.Discard); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	//lint:ignore SA1012 a nil context is the input under test
	if _, err := e.Render(nil, Document{}, io.Discard); !errors.Is(err, ErrInvalidInput) {
		t.Fatal(err)
	}
	if _, err := e.Render(context.Background(), Document{}, nil); !errors.Is(err, ErrInvalidInput) {
		t.Fatal(err)
	}
	var zero Engine
	if _, err := zero.Render(context.Background(), Document{}, io.Discard); !errors.Is(err, ErrInvalidOptions) {
		t.Fatal(err)
	}
	if b, err := Render(context.Background(), "hello"); err != nil || !bytes.HasPrefix(b, []byte("%PDF-")) {
		t.Fatalf("convenience render failed: %v", err)
	}
}

func TestConcurrent100Requests(t *testing.T) {
	e, err := New(Options{Limits: Limits{MaxHTMLBytes: 1}})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		t.Run("request", func(t *testing.T) {
			t.Parallel()
			if result, err := e.Render(context.Background(), Document{HTML: "x"}, io.Discard); err != nil || result.Pages != 1 {
				t.Fatal(err)
			}
			s, _, err := e.start(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Warn(session.Warning{Code: "request"}); err != nil {
				t.Fatal(err)
			}
			fresh, _, err := e.start(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(fresh.Warnings()) != 0 {
				t.Fatal("warning state leaked")
			}
		})
	}
}

func TestRegisteredFontsShareResourceBudget(t *testing.T) {
	data, err := os.ReadFile("internal/font/assets/NotoSans-Regular.ttf")
	if err != nil {
		t.Fatal(err)
	}
	e, err := New(Options{Fonts: []FontFace{{Family: "Custom", Data: data}}, Limits: Limits{MaxTotalAssetBytes: int64(len(data)) + 2}})
	if err != nil {
		t.Fatal(err)
	}
	_, loader, err := e.start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := loader.Load(resource.Image, "data:,abc"); !errors.Is(err, ErrLimitExceeded) {
		t.Fatal("registered font bytes absent from asset accounting", err)
	}
}

package html2pdf_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"strings"
	"testing"

	pdf "github.com/arham09/html2pdf"
)

type resolverFunc func(context.Context, string) (io.ReadCloser, error)

func (f resolverFunc) Resolve(ctx context.Context, uri string) (io.ReadCloser, error) {
	return f(ctx, uri)
}

type tracked struct {
	io.Reader
	closed   bool
	closeErr error
}

func (r *tracked) Close() error { r.closed = true; return r.closeErr }

type outputFunc func([]byte) (int, error)

func (f outputFunc) Write(p []byte) (int, error) { return f(p) }

func TestDeterministicDistinctConcurrent100(t *testing.T) {
	e, err := pdf.New(pdf.Options{Header: pdf.HeaderFooter{HTML: `<div style="font-size:8pt">Page {page} / {pages}</div>`, HeightPt: 20}})
	if err != nil {
		t.Fatal(err)
	}
	expected := make([][]byte, 100)
	input := func(i int) pdf.Document {
		return pdf.Document{HTML: fmt.Sprintf(`<p><strong>REQUEST-%03d</strong> <em>café</em> é</p><table style="width:300pt;table-layout:fixed;border-collapse:collapse"><thead><tr><th>ID</th><th>Amount</th></tr></thead><tbody><tr><td>OWN-%03d</td><td style="text-align:right">1.000,00</td></tr></tbody></table><p style="break-before:page">END-%03d</p>`, i, i, i)}
	}
	for i := range expected {
		var b bytes.Buffer
		r, err := e.Render(context.Background(), input(i), &b)
		if err != nil || r.Pages != 2 {
			t.Fatal(r, err)
		}
		expected[i] = bytes.Clone(b.Bytes())
	}
	for i := range expected {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			t.Parallel()
			var b bytes.Buffer
			r, err := e.Render(context.Background(), input(i), &b)
			if err != nil || r.Pages != 2 || !bytes.Equal(b.Bytes(), expected[i]) {
				t.Fatal("request identity, deterministic output or state isolation failed", r, err)
			}
		})
	}
}
func TestResourceFailuresCancelAndCloseBeforeOutput(t *testing.T) {
	boom := errors.New("asset failure")
	for _, mode := range []string{"resolve", "read", "close", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			r := &tracked{Reader: strings.NewReader("invalid image")}
			resolver := resolverFunc(func(context.Context, string) (io.ReadCloser, error) {
				switch mode {
				case "resolve":
					return r, boom
				case "read":
					r.Reader = errorReader{boom}
				case "close":
					r.closeErr = boom
				case "cancel":
					cancel()
				}
				return r, nil
			})
			e, err := pdf.New(pdf.Options{Resolver: resolver})
			if err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			_, err = e.Render(ctx, pdf.Document{HTML: `<img src="asset">`}, &out)
			want := boom
			if mode == "cancel" {
				want = context.Canceled
			}
			if !errors.Is(err, want) || !r.closed || out.Len() != 0 {
				t.Fatal(mode, err, r.closed, out.Len())
			}
		})
	}
	var pngBytes bytes.Buffer
	if err := png.Encode(&pngBytes, image.NewNRGBA(image.Rect(0, 0, 2, 1))); err != nil {
		t.Fatal(err)
	}
	e, err := pdf.New(pdf.Options{Resolver: resolverFunc(func(context.Context, string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(pngBytes.Bytes())), nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	writes := 0
	_, err = e.Render(ctx, pdf.Document{HTML: `<img src="asset">`}, outputFunc(func(b []byte) (int, error) { writes++; cancel(); return len(b), nil }))
	if !errors.Is(err, context.Canceled) || writes != 1 {
		t.Fatal("writer cancellation", err, writes)
	}
}

type errorReader struct{ err error }

func (r errorReader) Read([]byte) (int, error) { return 0, r.err }

func TestMaliciousAndOutputBoundaries(t *testing.T) {
	cases := []struct {
		html   string
		limits pdf.Limits
	}{
		{strings.Repeat("<div>", 200), pdf.Limits{MaxDOMDepth: 32}},
		{`<table>` + strings.Repeat(`<tr><td colspan="1000">A</td></tr>`, 20) + `</table>`, pdf.Limits{MaxLayoutWork: 20000}},
		{`<p>` + strings.Repeat("A", 65537) + `</p>`, pdf.Limits{MaxGlyphs: 1000}},
		{`<p>A</p><p style="break-before:page">B</p>`, pdf.Limits{MaxPages: 1}},
		{`<img src="data:;base64,` + strings.Repeat("A", 4096) + `">`, pdf.Limits{MaxResourceURIBytes: 100}},
		{`<style>` + strings.Repeat("@media print{", 80) + `p{color:red}</style>Hi`, pdf.Limits{MaxDOMDepth: 32}},
	}
	for _, c := range cases {
		e, err := pdf.New(pdf.Options{Limits: c.limits})
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		_, err = e.Render(context.Background(), pdf.Document{HTML: c.html}, &out)
		if err == nil || out.Len() != 0 {
			t.Fatal("malicious input was not bounded before output", err)
		}
	}
	good, err := pdf.Render(context.Background(), "Hello output quota")
	if err != nil {
		t.Fatal(err)
	}
	for _, limit := range []int64{1, int64(len(good) - 1)} {
		e, err := pdf.New(pdf.Options{Limits: pdf.Limits{MaxOutputBytes: limit}})
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		_, err = e.Render(context.Background(), pdf.Document{HTML: "Hello output quota"}, &out)
		if !errors.Is(err, pdf.ErrLimitExceeded) || out.Len() != 0 {
			t.Fatal("serialized output preflight", err, out.Len())
		}
	}
	e, err := pdf.New(pdf.Options{Limits: pdf.Limits{MaxOutputBytes: int64(len(good))}})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if _, err = e.Render(context.Background(), pdf.Document{HTML: "Hello output quota"}, &out); err != nil || !bytes.Equal(good, out.Bytes()) {
		t.Fatal("exact output quota", err)
	}
}

package html2pdf_test

import (
	"bytes"
	"compress/zlib"
	"context"
	"errors"
	"io"
	"regexp"
	"strconv"
	"testing"

	pdf "github.com/arham09/html2pdf"
)

var streamHeader = regexp.MustCompile(`<< /Length (\d+)[^\n]*>>\nstream\n`)

// expand returns the PDF followed by every Flate stream decompressed, so a
// search also sees compressed page content.
func expand(t *testing.T, data []byte) []byte {
	t.Helper()
	out := bytes.Clone(data)
	for _, m := range streamHeader.FindAllSubmatchIndex(data, -1) {
		n, err := strconv.Atoi(string(data[m[2]:m[3]]))
		if err != nil || m[1]+n > len(data) {
			t.Fatal("invalid stream length")
		}
		r, err := zlib.NewReader(bytes.NewReader(data[m[1] : m[1]+n]))
		if err != nil {
			continue
		}
		plain, err := io.ReadAll(r)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, plain...)
	}
	return out
}

func TestAcceptance(t *testing.T) {
	for _, name := range fixtureNames {
		t.Run(name, func(t *testing.T) {
			doc, options, err := fixture(name)
			if err != nil {
				t.Fatal(err)
			}
			e, err := pdf.New(options)
			if err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			r, err := e.Render(context.Background(), doc, &out)
			if name == "overflow_row" {
				if !errors.Is(err, pdf.ErrUnbreakableContent) || out.Len() != 0 {
					t.Fatal("overflow acceptance", err)
				}
				return
			}
			want := 1
			switch name {
			case "invoice_many_rows":
				want = 5
			case "financial_report_20_pages":
				want = 20
			case "statement_transactions":
				want = 26
			}
			if err != nil || r.Pages != want || !bytes.HasPrefix(out.Bytes(), []byte("%PDF-1.7")) {
				t.Fatal("acceptance output", r, err)
			}
			if (name == "malformed_input" || name == "unsupported_css") && len(r.Warnings) == 0 {
				t.Fatal("missing diagnostics")
			}
			if name == "scripts_ignored" {
				all := expand(t, out.Bytes())
				if !bytes.Contains(all, []byte(" Tj")) {
					t.Fatal("page content was not decompressed")
				}
				if bytes.Contains(all, []byte("/JavaScript")) || bytes.Contains(all, []byte("https://example.invalid")) {
					t.Fatal("active script content in PDF")
				}
			}
			if name == "unsupported_css" {
				options.StrictCSS = true
				strict, err := pdf.New(options)
				if err != nil {
					t.Fatal(err)
				}
				out.Reset()
				if _, err := strict.Render(context.Background(), doc, &out); !errors.Is(err, pdf.ErrUnsupportedFeature) || out.Len() != 0 {
					t.Fatal("strict diagnostics", err)
				}
			}
		})
	}
}

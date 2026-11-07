package html2pdf_test

import (
	"bytes"
	"context"
	pdf "github.com/arham09/html2pdf"
	"testing"
)

func BenchmarkRender(b *testing.B) {
	for _, name := range []string{"invoice_one_page", "invoice_many_rows", "financial_report_20_pages", "statement_transactions"} {
		b.Run(name, func(b *testing.B) {
			doc, options, err := fixture(name)
			if err != nil {
				b.Fatal(err)
			}
			e, err := pdf.New(options)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			size, pages := 0, 0
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				var out bytes.Buffer
				r, err := e.Render(context.Background(), doc, &out)
				if err != nil {
					b.Fatal(err)
				}
				size = out.Len()
				pages = r.Pages
			}
			b.ReportMetric(float64(size), "pdf-bytes/op")
			b.ReportMetric(float64(pages), "pages/op")
		})
	}
}

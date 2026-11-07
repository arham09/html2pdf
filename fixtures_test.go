package html2pdf_test

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"strings"

	pdf "github.com/arham09/html2pdf"
)

// fixtureNames lists the representative documents for acceptance tests and benchmarks.
var fixtureNames = []string{"invoice_one_page", "invoice_many_rows", "financial_report_20_pages", "statement_transactions", "indonesian_fonts", "overflow_row", "malformed_input", "unsupported_css", "scripts_ignored"}

func fixture(name string) (pdf.Document, pdf.Options, error) {
	var html strings.Builder
	html.WriteString(`<html><head><style>@page{size:A4;margin:15mm}body{font-family:"Noto Sans";font-size:9pt;line-height:14pt;color:#25364a}h1{font-size:20pt;line-height:26pt;margin:0 0 8pt}p{margin:0 0 6pt}table{width:100%;table-layout:fixed;border-collapse:collapse}td,th{padding:1pt;border:0.5pt solid #aabbcc}thead{background:#e9f0f6}.number{text-align:right;white-space:nowrap}.id{width:55pt;white-space:nowrap}.amount{width:90pt}.total{font-weight:bold;background:#edf4fa}.chapter{break-before:page}</style></head><body>`)
	options := pdf.Options{StrictCSS: true}
	if name == "invoice_many_rows" || name == "financial_report_20_pages" || name == "statement_transactions" {
		options.Header = pdf.HeaderFooter{HTML: `<div style="font-size:8pt;line-height:12pt">PT Contoh Indonesia</div>`, HeightPt: 24}
		options.Footer = pdf.HeaderFooter{HTML: `<div style="font-size:8pt;line-height:12pt;text-align:right">Page {page} of {pages}</div>`, HeightPt: 20}
	}
	switch name {
	case "invoice_one_page":
		logo := image.NewNRGBA(image.Rect(0, 0, 24, 12))
		for y := 0; y < 12; y++ {
			for x := 0; x < 24; x++ {
				logo.SetNRGBA(x, y, color.NRGBA{R: 30, G: 100, B: 180, A: 200})
			}
		}
		var data bytes.Buffer
		if err := png.Encode(&data, logo); err != nil {
			return pdf.Document{}, options, err
		}
		fmt.Fprintf(&html, `<img width="64" src="data:image/png;base64,%s"><h1>INVOICE INV-0001</h1><p><strong>PT Contoh Indonesia</strong><br>Customer: Acme · Jakarta</p><table><thead><tr><th>Description</th><th class="id number">Qty</th><th class="amount number">Amount (IDR)</th></tr></thead><tbody><tr><td>Consulting preparation and review</td><td class="number">2</td><td class="number">2.000.000,00</td></tr><tr><td><em>Design review</em></td><td class="number">1</td><td class="number">500.000,00</td></tr><tr><td colspan="2">Subtotal</td><td class="number">2.500.000,00</td></tr><tr><td colspan="2">Tax 11%%</td><td class="number">275.000,00</td></tr><tr class="total"><td colspan="2">GRAND TOTAL</td><td class="number">2.775.000,00</td></tr></tbody></table><p>Reference INV-0001 · Terima kasih.</p>`, base64.StdEncoding.EncodeToString(data.Bytes()))
	case "invoice_many_rows":
		html.WriteString(`<h1>INVOICE · 201 ITEMS</h1><table><thead><tr><th class="id">Item ID</th><th>Description</th><th class="amount number">Amount</th></tr></thead><tbody>`)
		for i := 1; i <= 201; i++ {
			fmt.Fprintf(&html, `<tr><td class="id">ITEM-%04d</td><td>Service item %04d</td><td class="number">1.000,00</td></tr>`, i, i)
		}
		html.WriteString(`<tr class="total"><td colspan="2">GRAND TOTAL</td><td class="number">201.000,00</td></tr></tbody></table>`)
	case "financial_report_20_pages":
		for i := 1; i <= 20; i++ {
			class := ""
			if i > 1 {
				class = ` class="chapter"`
			}
			fmt.Fprintf(&html, `<div%s><h1>FINANCIAL SECTION %02d</h1><p>Illustrative comparative report. All numbers are caller-provided sample values.</p><p>Preparation and review preserve source text, including café and é. Narrative paragraphs and comparative tables use the same measured layout.</p><table><thead><tr><th>Metric</th><th class="number">2025</th><th class="number">2026</th></tr></thead><tbody><tr><td>Revenue</td><td class="number">100.000,00</td><td class="number">120.000,00</td></tr><tr><td>Costs</td><td class="number">60.000,00</td><td class="number">70.000,00</td></tr><tr><td>Operating result</td><td class="number">40.000,00</td><td class="number">50.000,00</td></tr></tbody></table><p>SECTION-END-%02d</p></div>`, class, i, i)
		}
	case "statement_transactions":
		html.WriteString(`<h1>TRANSACTION STATEMENT</h1><table style="table-layout:auto;border-collapse:separate"><thead><tr><th class="id">Row ID</th><th>Description</th><th class="number">Debit</th><th class="number">Credit</th><th class="number">Balance</th></tr></thead><tbody>`)
		for i := 1; i <= 1000; i++ {
			debit, credit, balance := "1.000,00", "0,00", "1.000,00"
			if i%2 == 0 {
				debit, credit, balance = "0,00", "1.000,00", "0,00"
			}
			description := fmt.Sprintf("Record %04d", i)
			if i%25 == 0 {
				description += " includes a longer description with document preparation and detailed review"
			}
			fmt.Fprintf(&html, `<tr><td class="id">TX-%04d</td><td>%s</td><td class="number">%s</td><td class="number">%s</td><td class="number">%s</td></tr>`, i, description, debit, credit, balance)
		}
		html.WriteString(`<tr class="total"><td colspan="4">CLOSING BALANCE</td><td class="number">0,00</td></tr></tbody></table>`)
	case "indonesian_fonts":
		html.WriteString(`<h1>Ringkasan Indonesia</h1><p>Rp 1.234.567,89 · Jakarta — pembayaran lunas.</p><p>café é q́ office ffi</p><p><strong>Tebal</strong> <em>Miring</em> <strong><em>Tebal miring</em></strong></p>`)
	case "overflow_row":
		html.WriteString(`<table><tr><td>` + strings.Repeat("Oversized row<br>", 100) + `</td></tr></table>`)
	case "malformed_input":
		options.StrictCSS = false
		html.WriteString(`<div><p>Recovered <strong>text</div><p style="unknown-property:1">After</p>`)
	case "unsupported_css":
		options.StrictCSS = false
		html.WriteString(`<p style="subgrid:yes">Unsupported CSS remains diagnosed.</p>`)
	case "scripts_ignored":
		html.WriteString(`<script>fetch("https://example.invalid/");document.write("INJECTED");</script><p>STATIC CONTENT ONLY</p>`)
	default:
		return pdf.Document{}, options, fmt.Errorf("unknown fixture %q", name)
	}
	html.WriteString(`</body></html>`)
	return pdf.Document{HTML: html.String()}, options, nil
}

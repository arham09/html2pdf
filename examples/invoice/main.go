// Command invoice renders a multipage invoice: a Flexbox masthead, summary
// cards, a 201-row table with a repeated header, and a page-number footer.
package main

import (
	"bytes"
	"context"
	_ "embed"
	"flag"
	"fmt"
	"html/template"
	"log"
	"os"
	"time"

	"github.com/arham09/html2pdf"
)

//go:embed invoice.html
var source string

type item struct {
	Reference, Description, Amount string
}

func main() {
	out := flag.String("out", "invoice.pdf", "output PDF file")
	flag.Parse()
	if err := run(*out); err != nil {
		log.Fatal(err)
	}
}

func run(out string) error {
	// The caller owns templates, escaping and number formatting.
	data := struct {
		Issuer, City, Customer, Number, Issued, Total string
		Items                                         []item
	}{
		Issuer: "PT Contoh Indonesia", City: "Jakarta · café é",
		Customer: "PT Pelanggan", Number: "INV-0001", Issued: "7 November 2026",
		Total: "Rp 248.146,56",
	}
	for i := 1; i <= 201; i++ {
		data.Items = append(data.Items, item{fmt.Sprintf("ITEM-%04d", i), "Monthly service", "1.234,56"})
	}
	tpl, err := template.New("invoice").Parse(source)
	if err != nil {
		return err
	}
	var html bytes.Buffer
	if err := tpl.Execute(&html, data); err != nil {
		return err
	}

	engine, err := html2pdf.New(html2pdf.Options{
		StrictCSS: true,
		Footer: html2pdf.HeaderFooter{
			HTML:     `<div style="font-size:8pt;line-height:12pt;text-align:right">Page {page} of {pages}</div>`,
			HeightPt: 18,
		},
	})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var output bytes.Buffer
	result, err := engine.Render(ctx, html2pdf.Document{HTML: html.String()}, &output)
	for _, w := range result.Warnings {
		log.Printf("warning: %s: %s", w.Code, w.Message)
	}
	if err != nil {
		return err
	}
	if err := os.WriteFile(out, output.Bytes(), 0o644); err != nil {
		return err
	}
	fmt.Printf("Wrote %s (%d pages)\n", out, result.Pages)
	return nil
}

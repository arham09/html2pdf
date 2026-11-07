// Command report renders a CSS Grid summary of 60 cards that paginates
// between grid rows.
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

//go:embed report.html
var source string

type branch struct {
	Name, Revenue string
}

func main() {
	out := flag.String("out", "report.pdf", "output PDF file")
	flag.Parse()
	if err := run(*out); err != nil {
		log.Fatal(err)
	}
}

func run(out string) error {
	data := struct {
		Company, Period string
		Branches        []branch
	}{Company: "PT Contoh Indonesia", Period: "November 2026"}
	for i := 1; i <= 60; i++ {
		data.Branches = append(data.Branches, branch{fmt.Sprintf("Branch %02d", i), "Rp 1.234,56"})
	}
	tpl, err := template.New("report").Parse(source)
	if err != nil {
		return err
	}
	var html bytes.Buffer
	if err := tpl.Execute(&html, data); err != nil {
		return err
	}

	engine, err := html2pdf.New(html2pdf.Options{StrictCSS: true})
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

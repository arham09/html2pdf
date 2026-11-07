// Command basic renders HTML with default options and saves the PDF.
package main

import (
	"context"
	"log"
	"os"

	"github.com/arham09/html2pdf"
)

func main() {
	data, err := html2pdf.Render(context.Background(), `<h1>Hello Indonesia</h1><p>Rp 1.234,56 · café é</p>`)
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile("document.pdf", data, 0o644); err != nil {
		log.Fatal(err)
	}
}

package html2pdf_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	pdf "github.com/arham09/html2pdf"
)

func TestRenderReturnsPublicLimitError(t *testing.T) {
	e, err := pdf.New(pdf.Options{Limits: pdf.Limits{MaxPages: 1}})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	_, err = e.Render(context.Background(), pdf.Document{HTML: `<p>A</p><p style="break-before:page">B</p>`}, &out)
	var limit *pdf.LimitError
	if !errors.As(err, &limit) || !errors.Is(err, pdf.ErrLimitExceeded) {
		t.Fatalf("not a public limit error: %v", err)
	}
	if limit.Resource != "pages" || limit.Limit != 1 || limit.Requested != 1 || out.Len() != 0 {
		t.Fatalf("limit details %+v, output %d bytes", limit, out.Len())
	}
	if !strings.HasPrefix(err.Error(), "html2pdf: resource limit exceeded: pages (limit 1") {
		t.Fatalf("message changed: %v", err)
	}
}

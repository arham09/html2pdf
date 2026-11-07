package html2pdf

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
)

type failOutput struct {
	err   error
	short bool
}

func (w failOutput) Write(p []byte) (int, error) {
	if w.short {
		return len(p) - 1, nil
	}
	return 0, w.err
}

func TestSinglePageErrorsAndImagePolicy(t *testing.T) {
	e, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, html := range []string{`<table><tr><td rowspan="2">later</td></tr></table>`, `<div style="width:9999pt">wide</div>`, `<p style="font-family:MissingFont">missing</p>`} {
		var out bytes.Buffer
		if _, err := e.Render(context.Background(), Document{HTML: html}, &out); err == nil || out.Len() != 0 {
			t.Fatalf("unexpected output/error for %s: %v", html, err)
		}
	}
	var out bytes.Buffer
	if _, err := e.Render(context.Background(), Document{HTML: `<img src="https://example.test/logo.png">`}, &out); !errors.Is(err, ErrMissingResource) || out.Len() != 0 {
		t.Fatal(err)
	}
	var imageData bytes.Buffer
	if err := png.Encode(&imageData, image.NewNRGBA(image.Rect(0, 0, 10, 5))); err != nil {
		t.Fatal(err)
	}
	html := `<img width="40" src="data:image/png;base64,` + base64.StdEncoding.EncodeToString(imageData.Bytes()) + `">`
	if result, err := e.Render(context.Background(), Document{HTML: html}, &out); err != nil || result.Pages != 1 {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`/Subtype\s*/Image`).Match(out.Bytes()) {
		t.Fatal("image not embedded")
	}
	tiny, _ := New(Options{Limits: Limits{MaxImagePixels: 49}})
	out.Reset()
	if _, err := tiny.Render(context.Background(), Document{HTML: html}, &out); !errors.Is(err, ErrLimitExceeded) || out.Len() != 0 {
		t.Fatal("image limit", err)
	}
}

func TestOutputErrorsAndBudgets(t *testing.T) {
	e, _ := New(Options{})
	boom := errors.New("caller write failed")
	if _, err := e.Render(context.Background(), Document{HTML: "hello"}, failOutput{err: boom}); !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if _, err := e.Render(context.Background(), Document{HTML: "hello"}, failOutput{short: true}); !errors.Is(err, io.ErrShortWrite) {
		t.Fatal(err)
	}
	for _, limits := range []Limits{{MaxGlyphs: 1}, {MaxDisplayOps: 1}, {MaxOutputBytes: 20}, {MaxFontBytes: 100}, {MaxTotalAssetBytes: 100}} {
		e, _ := New(Options{Limits: limits})
		var output bytes.Buffer
		if _, err := e.Render(context.Background(), Document{HTML: "hello world"}, &output); !errors.Is(err, ErrLimitExceeded) {
			t.Fatalf("limits=%+v err=%v", limits, err)
		}
	}
	e, _ = New(Options{Header: HeaderFooter{HTML: "Page {page}", HeightPt: 1}})
	var out bytes.Buffer
	if _, err := e.Render(context.Background(), Document{HTML: "hello"}, &out); !errors.Is(err, ErrUnbreakableContent) || out.Len() != 0 {
		t.Fatal("overflowing decoration produced output", err)
	}
}

func TestCustomFontIdentityAndJPEG(t *testing.T) {
	data, err := os.ReadFile("internal/font/assets/NotoSans-Regular.ttf")
	if err != nil {
		t.Fatal(err)
	}
	e, err := New(Options{Fonts: []FontFace{{Family: "CallerAlias", Data: data}}})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if _, err := e.Render(context.Background(), Document{HTML: `<p style="font-family:CallerAlias">Custom font</p>`}, &output); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(output.Bytes(), []byte("+NotoSans-Regular")) || bytes.Contains(output.Bytes(), []byte("+CallerAlias")) {
		t.Fatal("font descriptor used alias instead of embedded identity")
	}
	output.Reset()
	if _, err := e.Render(context.Background(), Document{HTML: `<strong style="font-family:CallerAlias">Missing bold</strong>`}, &output); !errors.Is(err, ErrMissingResource) || output.Len() != 0 {
		t.Fatal("synthetic unregistered style", err)
	}
	var jpegData bytes.Buffer
	if err := jpeg.Encode(&jpegData, image.NewGray(image.Rect(0, 0, 10, 5)), nil); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if _, err := e.Render(context.Background(), Document{HTML: `<img style="width:40pt;height:20pt" src="data:image/jpeg;base64,` + base64.StdEncoding.EncodeToString(jpegData.Bytes()) + `">`}, &output); err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`/Subtype\s*/Image`).Match(output.Bytes()) {
		t.Fatal("JPEG absent")
	}
	if data, err := Render(context.Background(), `<table><tr><td rowspan="2">Later</td></tr></table>`); data != nil || !errors.Is(err, ErrUnsupportedFeature) {
		t.Fatal("byte API exposed failed render")
	}
}

func TestTableQuotaFailuresAreAtomic(t *testing.T) {
	input := `<table style="width:100pt;table-layout:fixed">` + strings.Repeat(`<tr><td colspan="1000">A</td></tr>`, 10) + `</table>`
	for _, limits := range []Limits{{MaxLayoutWork: 15000}, {MaxTableRows: 2}, {MaxTableCells: 2}, {MaxDisplayOps: 2}, {MaxGlyphs: 2}} {
		e, err := New(Options{Limits: limits})
		if err != nil {
			t.Fatal(err)
		}
		var output bytes.Buffer
		_, err = e.Render(context.Background(), Document{HTML: input}, &output)
		if !errors.Is(err, ErrLimitExceeded) || output.Len() != 0 {
			t.Fatal("table limits must fail before output", limits, err, output.Len())
		}
	}
	for _, input := range []string{`<table style="width:100pt;table-layout:fixed"><tr><td style="width:80pt">A</td><td style="width:80pt">B</td></tr></table>`, `<table><tr><td>` + strings.Repeat(`Row<br>`, 100) + `</td></tr></table>`} {
		if data, err := Render(context.Background(), input); data != nil || !errors.Is(err, ErrUnbreakableContent) {
			t.Fatal("table failure exposed bytes", err)
		}
	}
}

func TestSharedEngineConcurrentTables(t *testing.T) {
	engine, err := New(Options{StrictCSS: true})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() {
			input := fmt.Sprintf(`<table style="width:300pt;table-layout:fixed;border-collapse:collapse"><thead><tr><th>ID</th><th>Amount</th></tr></thead><tbody><tr><td>ROW-%03d</td><td style="text-align:right">1.234,56</td></tr></tbody></table>`, i)
			var output bytes.Buffer
			result, err := engine.Render(context.Background(), Document{HTML: input}, &output)
			if err != nil || result.Pages != 1 || !bytes.HasPrefix(output.Bytes(), []byte("%PDF-")) {
				t.Errorf("table %d: %v", i, err)
			}
		})
	}
	wg.Wait()
}

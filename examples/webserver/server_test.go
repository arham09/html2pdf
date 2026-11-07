package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	pdf "github.com/arham09/html2pdf"
)

type renderFunc func(context.Context, pdf.Document, io.Writer) (pdf.Result, error)

func (f renderFunc) Render(ctx context.Context, doc pdf.Document, w io.Writer) (pdf.Result, error) {
	return f(ctx, doc, w)
}
func request(h http.Handler, method, path, body, kind string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if kind != "" {
		r.Header.Set("Content-Type", kind)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func actualHandler(t *testing.T) http.Handler {
	t.Helper()
	e, err := exampleEngine()
	if err != nil {
		t.Fatal(err)
	}
	return newHandler(e, 1, time.Second*30)
}
func TestHealthAndEmbeddedUI(t *testing.T) {
	h := actualHandler(t)
	w := request(h, "GET", "/healthz", "", "")
	var health map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &health); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || health["status"] != "ok" || health["os"] != runtime.GOOS || health["arch"] != runtime.GOARCH {
		t.Fatal(health)
	}
	for _, path := range []string{"/", "/app.js", "/style.css"} {
		w := request(h, "GET", path, "", "")
		if w.Code != 200 || w.Body.Len() == 0 || w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Content-Security-Policy") == "" {
			t.Fatal(path, w.Code)
		}
	}
	for _, c := range []struct {
		method, path string
		status       int
	}{{"POST", "/healthz", 405}, {"GET", "/render", 405}, {"DELETE", "/", 405}, {"GET", "/assets/../go.mod", 404}, {"GET", "/nope", 404}} {
		w := request(h, c.method, c.path, "", "")
		if w.Code != c.status || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
			t.Fatal(c, w.Code)
		}
	}
}
func TestPublicPDFEndpoint(t *testing.T) {
	h := actualHandler(t)
	input := `{"html":"<p><strong>café</strong> é Rp 1.234,56</p><p style=\"break-before:page\">Second</p>","stylesheets":["p{margin:0}"]}`
	w := request(h, "POST", "/render", input, "application/json; charset=UTF-8")
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/pdf" || w.Header().Get("X-PDF-Pages") != "2" || w.Header().Get("X-PDF-Warnings") != "0" || !bytes.HasPrefix(w.Body.Bytes(), []byte("%PDF-1.7")) {
		t.Fatal(w.Code, w.Header(), w.Body.String())
	}
	if w.Header().Get("Content-Disposition") != `inline; filename="document.pdf"` {
		t.Fatal("PDF disposition")
	}
	w = request(h, "POST", "/render", `{"html":"<p style=\"unknown-style:yes\">Warn</p>"}`, "application/json")
	if w.Code != 200 || w.Header().Get("X-PDF-Warnings") != "1" {
		t.Fatal("warnings", w.Code, w.Header())
	}
}
func TestJSONAndRenderFailures(t *testing.T) {
	h := actualHandler(t)
	cases := []struct {
		body, kind, code string
		status           int
	}{
		{`{"html":"Hi"}`, "text/html", "unsupported_media_type", 415},
		{`{`, "application/json", "invalid_json", 400},
		{`{"html":null}`, "application/json", "invalid_json", 400},
		{`{"stylesheets":[]}`, "application/json", "invalid_json", 400},
		{`{"html":"Hi","unknown":true}`, "application/json", "invalid_json", 400},
		{`{"html":"Hi"} {}`, "application/json", "invalid_json", 400},
		{"{\"html\":\"\xff\"}", "application/json", "invalid_json", 400},
		{`{"html":"<img src=\"https://example.invalid/private\">"}`, "application/json", "missing_resource", 422},
		{`{"html":"<div style=\"display:inline-grid\">No</div>"}`, "application/json", "unsupported_feature", 422},
		{`{"html":"<div style=\"width:1pt\">unbreakableword</div>"}`, "application/json", "unbreakable_content", 422},
		{`{"html":"` + strings.Repeat("A", (256<<10)+1) + `"}`, "application/json", "render_limit", 413},
		{strings.Repeat(" ", maxRequestBytes+1), "application/json", "request_too_large", 413},
	}
	for _, c := range cases {
		w := request(h, "POST", "/render", c.body, c.kind)
		var body errorResponse
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if w.Code != c.status || body.Error.Code != c.code || bytes.Contains(w.Body.Bytes(), []byte("%PDF")) {
			t.Fatal(c.code, w.Code, w.Body.String())
		}
	}
}
func TestAdmissionAndDeadline(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	fake := renderFunc(func(ctx context.Context, _ pdf.Document, w io.Writer) (pdf.Result, error) {
		close(started)
		select {
		case <-release:
			_, err := w.Write([]byte("PDF"))
			return pdf.Result{Pages: 1}, err
		case <-ctx.Done():
			return pdf.Result{}, ctx.Err()
		}
	})
	h := newHandler(fake, 1, 5*time.Second)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- request(h, "POST", "/render", `{"html":"A"}`, "application/json") }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("renderer did not start")
	}
	busy := request(h, "POST", "/render", `{"html":"B"}`, "application/json")
	if busy.Code != 503 || busy.Header().Get("Retry-After") != "1" {
		t.Fatal("admission", busy.Code)
	}
	if health := request(h, "GET", "/healthz", "", ""); health.Code != 200 {
		t.Fatal("health blocked")
	}
	close(release)
	select {
	case w := <-done:
		if w.Code != 200 {
			t.Fatal(w.Code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("request did not finish")
	}
	timeout := newHandler(renderFunc(func(ctx context.Context, _ pdf.Document, _ io.Writer) (pdf.Result, error) {
		<-ctx.Done()
		return pdf.Result{}, ctx.Err()
	}), 1, 10*time.Millisecond)
	if w := request(timeout, "POST", "/render", `{"html":"A"}`, "application/json"); w.Code != 504 {
		t.Fatal("deadline", w.Code)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest("POST", "/render", strings.NewReader(`{"html":"A"}`)).WithContext(ctx)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 408 {
		t.Fatal("cancellation", w.Code)
	}
}
func TestInternalErrorsAndPartialRenderNeverLeakPDF(t *testing.T) {
	for _, err := range []error{errors.New("private internal cause"), pdf.ErrInvalidInput, pdf.ErrLimitExceeded} {
		h := newHandler(renderFunc(func(context.Context, pdf.Document, io.Writer) (pdf.Result, error) { return pdf.Result{}, err }), 1, time.Second)
		w := request(h, "POST", "/render", `{"html":"A"}`, "application/json")
		if w.Code == 200 || strings.Contains(w.Body.String(), "private internal cause") {
			t.Fatal("internal details", w.Code, w.Body.String())
		}
	}
	h := newHandler(renderFunc(func(_ context.Context, _ pdf.Document, w io.Writer) (pdf.Result, error) {
		_, err := w.Write([]byte("%PDF-private-partial"))
		if err != nil {
			return pdf.Result{}, err
		}
		return pdf.Result{}, pdf.ErrInvalidInput
	}), 1, time.Second)
	if w := request(h, "POST", "/render", `{"html":"A"}`, "application/json"); w.Code != 422 || bytes.Contains(w.Body.Bytes(), []byte("%PDF")) {
		t.Fatal("partial render leaked")
	}
}

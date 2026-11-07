package main

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	pdf "github.com/arham09/html2pdf"
)

const maxRequestBytes = 2 << 20

//go:embed assets/*
var assets embed.FS

type renderer interface {
	Render(context.Context, pdf.Document, io.Writer) (pdf.Result, error)
}
type application struct {
	renderer renderer
	slots    chan struct{}
	timeout  time.Duration
}
type renderRequest struct {
	HTML        *string  `json:"html"`
	Stylesheets []string `json:"stylesheets,omitempty"`
}
type problem struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type errorResponse struct {
	Error problem `json:"error"`
}

func exampleEngine() (*pdf.Engine, error) {
	return pdf.New(pdf.Options{Limits: pdf.Limits{MaxHTMLBytes: 256 << 10, MaxCSSBytes: 128 << 10, MaxDOMNodes: 20000, MaxDOMDepth: 64, MaxTableRows: 5000, MaxTableCells: 25000, MaxGlyphs: 200000, MaxLayoutWork: 2000000, MaxPages: 100, MaxDisplayOps: 200000, MaxAssets: 32, MaxAssetBytes: 2 << 20, MaxTotalAssetBytes: 8 << 20, MaxImagePixels: 1000000, MaxDecodedBytes: 16 << 20, MaxOutputBytes: 16 << 20}})
}
func newHandler(render renderer, concurrency int, timeout time.Duration) http.Handler {
	return &application{renderer: render, slots: make(chan struct{}, concurrency), timeout: timeout}
}
func (a *application) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; frame-src blob:; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
	switch r.URL.Path {
	case "/healthz":
		if !method(w, r, http.MethodGet) {
			return
		}
		writeJSON(w, http.StatusOK, struct {
			Status string `json:"status"`
			OS     string `json:"os"`
			Arch   string `json:"arch"`
			Go     string `json:"go"`
		}{"ok", runtime.GOOS, runtime.GOARCH, runtime.Version()})
	case "/render":
		if !method(w, r, http.MethodPost) {
			return
		}
		a.render(w, r)
	case "/", "/app.js", "/style.css":
		if !method(w, r, http.MethodGet) {
			return
		}
		name, kind := "index.html", "text/html; charset=utf-8"
		if r.URL.Path == "/app.js" {
			name, kind = "app.js", "text/javascript; charset=utf-8"
		}
		if r.URL.Path == "/style.css" {
			name, kind = "style.css", "text/css; charset=utf-8"
		}
		data, err := assets.ReadFile("assets/" + name)
		if err != nil {
			writeError(w, 500, "internal_error", "Example asset unavailable.")
			return
		}
		w.Header().Set("Content-Type", kind)
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		_, _ = w.Write(data) // A disconnected client cannot receive another error response.
	default:
		writeError(w, 404, "not_found", "Route not found.")
	}
}
func method(w http.ResponseWriter, r *http.Request, want string) bool {
	if r.Method == want {
		return true
	}
	w.Header().Set("Allow", want)
	writeError(w, 405, "method_not_allowed", "Method not allowed.")
	return false
}
func (a *application) render(w http.ResponseWriter, r *http.Request) {
	kind, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || kind != "application/json" || (params["charset"] != "" && !strings.EqualFold(params["charset"], "utf-8")) || (r.Header.Get("Content-Encoding") != "" && r.Header.Get("Content-Encoding") != "identity") {
		writeError(w, 415, "unsupported_media_type", "Send UTF-8 application/json without content encoding.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), a.timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		renderError(w, err)
		return
	}
	select {
	case a.slots <- struct{}{}:
		defer func() { <-a.slots }()
	default:
		w.Header().Set("Retry-After", "1")
		writeError(w, 503, "busy", "A render is in progress. Retry later.")
		return
	}
	body := http.MaxBytesReader(w, r.Body, maxRequestBytes)
	data, err := io.ReadAll(body)
	if err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			writeError(w, 413, "request_too_large", "JSON body exceeds 2 MiB.")
		} else {
			writeError(w, 400, "invalid_json", "Unable to read request body.")
		}
		return
	}
	if !utf8.Valid(data) {
		writeError(w, 400, "invalid_json", "Request must be valid UTF-8 JSON.")
		return
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var input renderRequest
	if err := decoder.Decode(&input); err != nil {
		writeError(w, 400, "invalid_json", "Expected html and optional stylesheets fields.")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, 400, "invalid_json", "Send exactly one JSON object.")
		return
	}
	if input.HTML == nil {
		writeError(w, 400, "invalid_json", "The html string field is required.")
		return
	}
	var output bytes.Buffer
	result, err := a.renderer.Render(ctx, pdf.Document{HTML: *input.HTML, Stylesheets: input.Stylesheets}, &output)
	if err != nil {
		renderError(w, err)
		return
	}
	if err := ctx.Err(); err != nil {
		renderError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `inline; filename="document.pdf"`)
	w.Header().Set("Content-Length", strconv.Itoa(output.Len()))
	w.Header().Set("X-PDF-Pages", strconv.Itoa(result.Pages))
	w.Header().Set("X-PDF-Warnings", strconv.Itoa(len(result.Warnings)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(output.Bytes()) // Client write failures end this response; PDF was fully rendered first.
}
func renderError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		writeError(w, 504, "render_timeout", "Rendering exceeded its deadline.")
	case errors.Is(err, context.Canceled):
		writeError(w, 408, "request_cancelled", "Request was cancelled.")
	case errors.Is(err, pdf.ErrLimitExceeded):
		writeError(w, 413, "render_limit", "Document exceeds the example render limits.")
	case errors.Is(err, pdf.ErrUnsupportedFeature):
		writeError(w, 422, "unsupported_feature", "Document uses an unsupported HTML/CSS feature.")
	case errors.Is(err, pdf.ErrMissingResource):
		writeError(w, 422, "missing_resource", "A font or asset is unavailable. External assets are disabled.")
	case errors.Is(err, pdf.ErrUnbreakableContent):
		writeError(w, 422, "unbreakable_content", "An indivisible item does not fit its page region.")
	case errors.Is(err, pdf.ErrInvalidInput):
		writeError(w, 422, "invalid_document", "Document input is invalid.")
	default:
		writeError(w, 500, "internal_error", "Rendering failed.")
	}
}
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorResponse{Error: problem{code, message}})
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
} // No second response can repair client I/O failure.

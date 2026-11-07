// Package html2pdf renders the supported static business-document
// HTML/CSS profile with embedded fonts and explicit resource access.
package html2pdf

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/arham09/html2pdf/internal/decoration"
	"github.com/arham09/html2pdf/internal/font"
	"github.com/arham09/html2pdf/internal/layout"
	"github.com/arham09/html2pdf/internal/pagination"
	"github.com/arham09/html2pdf/internal/paint"
	"github.com/arham09/html2pdf/internal/pdf"
	"github.com/arham09/html2pdf/internal/resource"
	"github.com/arham09/html2pdf/internal/session"
)

// Engine owns immutable configuration; each Render creates independent state.
// A caller-supplied resolver must itself be safe for concurrent use.
type Engine struct {
	options Options
	ready   bool
}

// New validates and copies options. It parses custom fonts once, so a bad
// font fails here and not during Render.
func New(options Options) (*Engine, error) {
	limits, err := session.Normalize(session.Limits(options.Limits))
	if err != nil {
		return nil, err
	}
	options.Limits = Limits(limits)
	if options.PageSize != nil {
		v := *options.PageSize
		options.PageSize = &v
	}
	if options.Margins != nil {
		v := *options.Margins
		options.Margins = &v
	}
	page, m := resolvedPage(options)
	if err := validatePage(page, m, options.Header, options.Footer); err != nil {
		return nil, err
	}
	if int64(len(options.Header.HTML)) > limits.MaxHTMLBytes || int64(len(options.Footer.HTML)) > limits.MaxHTMLBytes || !utf8.ValidString(options.Header.HTML) || !utf8.ValidString(options.Footer.HTML) {
		return nil, fmt.Errorf("%w: invalid decoration input", ErrInvalidOptions)
	}
	if int64(len(options.Fonts)) > limits.MaxFonts {
		return nil, &LimitError{Resource: "fonts", Limit: limits.MaxFonts, Requested: int64(len(options.Fonts))}
	}
	faces := make([]FontFace, len(options.Fonts))
	seen := make(map[string]bool)
	var total int64
	for i, face := range options.Fonts {
		if strings.TrimSpace(face.Family) == "" || len(face.Family) > 256 || !utf8.ValidString(face.Family) {
			return nil, fmt.Errorf("%w: invalid font family", ErrInvalidOptions)
		}
		if face.Weight == 0 {
			face.Weight = 400
		}
		if face.Weight != 400 && face.Weight != 700 {
			return nil, fmt.Errorf("%w: font weight", ErrUnsupportedFeature)
		}
		key := fmt.Sprintf("%s/%d/%t", strings.ToLower(strings.TrimSpace(face.Family)), face.Weight, face.Italic)
		if seen[key] {
			return nil, fmt.Errorf("%w: duplicate font face", ErrInvalidOptions)
		}
		seen[key] = true
		n := int64(len(face.Data))
		if n > limits.MaxFontBytes {
			return nil, &LimitError{Resource: "font_bytes", Limit: limits.MaxFontBytes, Requested: n}
		}
		if n > limits.MaxTotalAssetBytes-total {
			return nil, &LimitError{Resource: "total_asset_bytes", Limit: limits.MaxTotalAssetBytes, Used: total, Requested: n}
		}
		total += n
		if _, err := font.Parse(face.Data); err != nil {
			return nil, fmt.Errorf("%w: font %d: %w", ErrInvalidOptions, i, err)
		}
		face.Data = bytes.Clone(face.Data)
		faces[i] = face
	}
	options.Fonts = faces
	return &Engine{options: options, ready: true}, nil
}

func resolvedPage(o Options) (PaperSize, Margins) {
	p, m := A4(), MillimeterMargins(15, 15, 15, 15)
	if o.PageSize != nil {
		p = *o.PageSize
	}
	if o.Margins != nil {
		m = *o.Margins
	}
	return p, m
}

// Render writes a supported static document. Layout/resource errors
// occur before output. Caller I/O errors can leave partial PDF bytes.
func (e *Engine) Render(ctx context.Context, doc Document, dst io.Writer) (Result, error) {
	if e == nil || !e.ready {
		return Result{}, fmt.Errorf("%w: use New to construct Engine", ErrInvalidOptions)
	}
	s, loader, err := e.start(ctx)
	if err != nil {
		return Result{}, publicError(err)
	}
	if dst == nil {
		return Result{}, fmt.Errorf("%w: nil writer", ErrInvalidInput)
	}
	pages, err := e.render(s, loader, doc, dst)
	result := Result{Warnings: publicWarnings(s.Warnings())}
	if err != nil {
		return result, publicError(err)
	}
	result.Pages = pages
	return result, nil
}

// render runs every stage with one session and returns the page count.
func (e *Engine) render(s *session.Session, loader resource.Loader, doc Document, dst io.Writer) (int, error) {
	prepared, err := e.prepare(s, doc)
	if err != nil {
		return 0, err
	}

	faces := make([]font.FaceSpec, len(e.options.Fonts))
	for i, f := range e.options.Fonts {
		faces[i] = font.FaceSpec{Family: f.Family, Weight: f.Weight, Italic: f.Italic, Data: f.Data}
	}
	fonts := font.NewRegistry(s, faces)
	body := layout.Rect{X: prepared.Margins.LeftPt, Y: prepared.Margins.TopPt + e.options.Header.HeightPt, Width: prepared.Page.WidthPt - prepared.Margins.LeftPt - prepared.Margins.RightPt, Height: prepared.Page.HeightPt - prepared.Margins.TopPt - prepared.Margins.BottomPt - e.options.Header.HeightPt - e.options.Footer.HeightPt}
	flow, err := layout.BuildFlow(s, prepared.Body, fonts, loader, prepared.Page.WidthPt, prepared.Page.HeightPt, body)
	if err != nil {
		return 0, err
	}
	pages, err := pagination.Build(s, flow, body)
	if err != nil {
		return 0, err
	}
	header := layout.Rect{X: body.X, Y: prepared.Margins.TopPt, Width: body.Width, Height: e.options.Header.HeightPt}
	footer := layout.Rect{X: body.X, Y: prepared.Page.HeightPt - prepared.Margins.BottomPt - e.options.Footer.HeightPt, Width: body.Width, Height: e.options.Footer.HeightPt}
	if err := decoration.Apply(s, pages, fonts, loader, decoration.Part{Document: prepared.Header, Region: header, Before: true}, decoration.Part{Document: prepared.Footer, Region: footer}); err != nil {
		return 0, err
	}
	painted := make([]paint.Page, len(pages))
	for i := range pages {
		painted[i] = pages[i].Paint
	}
	if err := pdf.WritePages(s, dst, painted); err != nil {
		return 0, err
	}
	return len(pages), nil
}

// start binds the same budget to input, fonts and all resource consumers.
func (e *Engine) start(ctx context.Context) (*session.Session, resource.Loader, error) {
	s, err := session.New(ctx, session.Limits(e.options.Limits))
	if err != nil {
		return nil, resource.Loader{}, err
	}
	for _, face := range e.options.Fonts {
		if err := s.Charge("fonts", 1); err != nil {
			return nil, resource.Loader{}, err
		}
		if err := s.Charge("total_asset_bytes", int64(len(face.Data))); err != nil {
			return nil, resource.Loader{}, err
		}
	}
	return s, resource.Loader{Session: s, Resolver: e.options.Resolver}, nil
}

// Render uses safe defaults and returns bytes only on success.
func Render(ctx context.Context, html string) ([]byte, error) {
	e, err := New(Options{})
	if err != nil {
		return nil, err
	}
	s, err := session.New(ctx, session.Limits(e.options.Limits))
	if err != nil {
		return nil, err
	}
	output := session.NewBuffer(s)
	if _, err := e.Render(ctx, Document{HTML: html}, output); err != nil {
		return nil, err
	}
	data, err := output.Bytes()
	if err != nil {
		return nil, publicError(err)
	}
	return data, nil
}

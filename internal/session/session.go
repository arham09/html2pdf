// Package session owns request-local policy, accounting and cancellation.
package session

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
)

var (
	ErrInvalidOptions     = errors.New("html2pdf: invalid options")
	ErrInvalidInput       = errors.New("html2pdf: invalid input")
	ErrLimitExceeded      = errors.New("html2pdf: resource limit exceeded")
	ErrUnsupportedFeature = errors.New("html2pdf: unsupported feature")
	ErrMissingResource    = errors.New("html2pdf: missing resource")
	ErrUnbreakableContent = errors.New("html2pdf: unbreakable content")
)

// Limits are positive ceilings; zero fields select safe defaults.
type Limits struct {
	MaxHTMLBytes, MaxCSSBytes, MaxStylesheets                     int64
	MaxDOMNodes, MaxDOMDepth, MaxTableRows, MaxTableCells         int64
	MaxGlyphs, MaxLayoutWork, MaxPages, MaxDisplayOps             int64
	MaxAssets, MaxAssetBytes, MaxTotalAssetBytes, MaxDecodedBytes int64
	MaxResourceURIBytes, MaxImagePixels, MaxFontBytes, MaxFonts   int64
	MaxOutputBytes, MaxWarnings                                   int64
}

func DefaultLimits() Limits {
	return Limits{
		MaxHTMLBytes: 1 << 20, MaxCSSBytes: 1 << 20, MaxStylesheets: 128,
		MaxDOMNodes: 200000, MaxDOMDepth: 128, MaxTableRows: 100000, MaxTableCells: 1000000,
		MaxGlyphs: 2000000, MaxLayoutWork: 10000000, MaxPages: 1000, MaxDisplayOps: 4000000,
		MaxAssets: 256, MaxAssetBytes: 8 << 20, MaxTotalAssetBytes: 32 << 20, MaxDecodedBytes: 64 << 20,
		MaxResourceURIBytes: 12 << 20, MaxImagePixels: 8000000, MaxFontBytes: 8 << 20, MaxFonts: 32,
		MaxOutputBytes: 64 << 20, MaxWarnings: 100,
	}
}

func (l *Limits) fields() []struct {
	name  string
	value *int64
} {
	return []struct {
		name  string
		value *int64
	}{
		{"html_bytes", &l.MaxHTMLBytes}, {"css_bytes", &l.MaxCSSBytes}, {"stylesheets", &l.MaxStylesheets},
		{"dom_nodes", &l.MaxDOMNodes}, {"dom_depth", &l.MaxDOMDepth}, {"table_rows", &l.MaxTableRows}, {"table_cells", &l.MaxTableCells},
		{"glyphs", &l.MaxGlyphs}, {"layout_work", &l.MaxLayoutWork}, {"pages", &l.MaxPages}, {"display_ops", &l.MaxDisplayOps},
		{"assets", &l.MaxAssets}, {"asset_bytes", &l.MaxAssetBytes}, {"total_asset_bytes", &l.MaxTotalAssetBytes}, {"decoded_bytes", &l.MaxDecodedBytes},
		{"resource_uri_bytes", &l.MaxResourceURIBytes}, {"image_pixels", &l.MaxImagePixels}, {"font_bytes", &l.MaxFontBytes}, {"fonts", &l.MaxFonts},
		{"output_bytes", &l.MaxOutputBytes}, {"warnings", &l.MaxWarnings},
	}
}

func Normalize(l Limits) (Limits, error) {
	defaults := DefaultLimits()
	df := defaults.fields()
	for i, f := range l.fields() {
		if *f.value < 0 || *f.value > math.MaxInt64/2 {
			return Limits{}, fmt.Errorf("%w: %s outside safe range", ErrInvalidOptions, f.name)
		}
		if *f.value == 0 {
			*f.value = *df[i].value
		}
	}
	return l, nil
}

type LimitError struct {
	Resource               string
	Limit, Used, Requested int64
}

func (e *LimitError) Error() string {
	return fmt.Sprintf("%s: %s (limit %d, used %d, requested %d)", ErrLimitExceeded, e.Resource, e.Limit, e.Used, e.Requested)
}
func (e *LimitError) Unwrap() error { return ErrLimitExceeded }

type Warning struct {
	Code, Message string
	SourceID      uint64
}

// Session is single-owner mutable state. Never share it between renders.
type Session struct {
	Context    context.Context
	Limits     Limits
	caps, used map[string]int64
	warnings   []Warning
	sourceID   uint64
}

func New(ctx context.Context, l Limits) (*Session, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: nil context", ErrInvalidInput)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	normalized, err := Normalize(l)
	if err != nil {
		return nil, err
	}
	caps := make(map[string]int64)
	for _, f := range normalized.fields() {
		caps[f.name] = *f.value
	}
	return &Session{Context: ctx, Limits: normalized, caps: caps, used: make(map[string]int64)}, nil
}
func (s *Session) Check() error { return s.Context.Err() }

// NextSourceID is request-local; DOM allocation is bounded before assignment.
func (s *Session) NextSourceID() uint64 { s.sourceID++; return s.sourceID }
func (s *Session) CheckSize(name string, n int64) error {
	if err := s.Check(); err != nil {
		return err
	}
	cap, ok := s.caps[name]
	if !ok || n < 0 {
		return fmt.Errorf("%w: invalid budget %s", ErrInvalidInput, name)
	}
	if n > cap {
		return &LimitError{Resource: name, Limit: cap, Requested: n}
	}
	return nil
}
func (s *Session) Charge(name string, n int64) error {
	if err := s.CheckSize(name, n); err != nil {
		return err
	}
	cap, used := s.caps[name], s.used[name]
	if n > cap-used {
		return &LimitError{Resource: name, Limit: cap, Used: used, Requested: n}
	}
	s.used[name] = used + n
	return nil
}
func (s *Session) Warn(w Warning) error {
	if err := s.Charge("warnings", 1); err != nil {
		return err
	}
	s.warnings = append(s.warnings, w)
	return nil
}
func (s *Session) Warnings() []Warning { return append([]Warning(nil), s.warnings...) }

// Writer bounds output cumulatively and checks cancellation around caller I/O.
// It cannot forcibly interrupt a blocking caller writer.
type Writer struct {
	Session     *Session
	Destination io.Writer
}

func (w Writer) Write(p []byte) (int, error) {
	if w.Destination == nil {
		return 0, fmt.Errorf("%w: nil writer", ErrInvalidInput)
	}
	if err := w.Session.Charge("output_bytes", int64(len(p))); err != nil {
		return 0, err
	}
	n, err := w.Destination.Write(p)
	if err != nil {
		return n, err
	}
	if n != len(p) {
		return n, io.ErrShortWrite
	}
	return n, w.Session.Check()
}

// Buffer retains bounded private output. Bytes returns a copy only on success.
type Buffer struct {
	session *Session
	data    bytes.Buffer
	err     error
}

func NewBuffer(s *Session) *Buffer { return &Buffer{session: s} }
func (b *Buffer) Write(p []byte) (int, error) {
	if b.err != nil {
		return 0, b.err
	}
	n, err := (Writer{b.session, &b.data}).Write(p)
	b.err = err
	return n, err
}
func (b *Buffer) Bytes() ([]byte, error) {
	if b.err != nil {
		return nil, b.err
	}
	if err := b.session.Check(); err != nil {
		return nil, err
	}
	return bytes.Clone(b.data.Bytes()), nil
}

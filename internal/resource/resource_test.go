package resource

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/png"
	"io"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/arham09/html2pdf/internal/session"
)

type resolverFunc func(context.Context, string) (io.ReadCloser, error)

func (f resolverFunc) Resolve(ctx context.Context, r string) (io.ReadCloser, error) { return f(ctx, r) }

type trackedReader struct {
	io.Reader
	closed   bool
	closeErr error
}

func (r *trackedReader) Close() error { r.closed = true; return r.closeErr }
func loader(t *testing.T, l session.Limits) Loader {
	t.Helper()
	s, e := session.New(context.Background(), l)
	if e != nil {
		t.Fatal(e)
	}
	return Loader{Session: s}
}

func TestDenyExternalAndBoundDataURLs(t *testing.T) {
	for _, ref := range []string{"https://example.test/logo", "file:///etc/passwd", "logo.png"} {
		if _, err := loader(t, session.Limits{}).Load(Image, ref); !errors.Is(err, session.ErrMissingResource) {
			t.Fatal(err)
		}
	}
	l := loader(t, session.Limits{MaxAssetBytes: 3})
	data, err := l.Load(Image, "data:;base64,YWJj")
	if err != nil || string(data) != "abc" {
		t.Fatal(string(data), err)
	}
	if _, err := l.Load(Image, "data:,abcd"); !errors.Is(err, session.ErrLimitExceeded) {
		t.Fatal(err)
	}
	for _, ref := range []string{"data:bad", "data:,%XX", "data:;base64,@@@@"} {
		if _, err := loader(t, session.Limits{}).Load(Image, ref); err == nil {
			t.Fatalf("accepted %q", ref)
		}
	}
	if _, err := loader(t, session.Limits{MaxResourceURIBytes: 5}).Load(Image, "data:,a"); !errors.Is(err, session.ErrLimitExceeded) {
		t.Fatal(err)
	}
}

func TestReadCloseErrorsAndTotals(t *testing.T) {
	boom := errors.New("I/O failed")
	for _, tc := range []struct {
		reader   io.Reader
		closeErr error
		want     error
	}{
		{strings.NewReader("abcd"), nil, session.ErrLimitExceeded},
		{iotest.ErrReader(boom), nil, boom},
		{strings.NewReader("abc"), boom, boom},
	} {
		r := &trackedReader{Reader: tc.reader, closeErr: tc.closeErr}
		l := loader(t, session.Limits{MaxAssetBytes: 3})
		l.Resolver = resolverFunc(func(context.Context, string) (io.ReadCloser, error) { return r, nil })
		data, err := l.Load(Image, "asset")
		if !errors.Is(err, tc.want) || !r.closed || data != nil {
			t.Fatalf("err=%v closed=%t data=%q", err, r.closed, data)
		}
	}
	l := loader(t, session.Limits{MaxTotalAssetBytes: 5})
	l.Resolver = resolverFunc(func(context.Context, string) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader("abc")), nil
	})
	if _, err := l.Load(Image, "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Load(Image, "b"); !errors.Is(err, session.ErrLimitExceeded) {
		t.Fatal(err)
	}
}

func TestResolverErrorsAndCancellation(t *testing.T) {
	boom := errors.New("resolver failed")
	r := &trackedReader{Reader: strings.NewReader("x")}
	l := loader(t, session.Limits{})
	l.Resolver = resolverFunc(func(context.Context, string) (io.ReadCloser, error) { return r, boom })
	if _, err := l.Load(Image, "x"); !errors.Is(err, boom) || !errors.Is(err, session.ErrMissingResource) || !r.closed {
		t.Fatal(err, r.closed)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s, _ := session.New(ctx, session.Limits{})
	calls := 0
	r = &trackedReader{Reader: strings.NewReader("x")}
	l = Loader{Session: s, Resolver: resolverFunc(func(got context.Context, _ string) (io.ReadCloser, error) {
		if got != ctx {
			t.Fatal("wrong context")
		}
		calls++
		cancel()
		return r, nil
	})}
	if _, err := l.Load(Image, "x"); !errors.Is(err, context.Canceled) || !r.closed {
		t.Fatal(err)
	}
	if _, err := l.Load(Image, "x"); !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatal("called resolver after cancellation")
	}
}

func TestImageBudgetBeforeDecode(t *testing.T) {
	var pngData bytes.Buffer
	if err := png.Encode(&pngData, image.NewNRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	l := loader(t, session.Limits{MaxImagePixels: 4, MaxDecodedBytes: 32})
	if _, err := l.DecodeImage(pngData.Bytes()); err != nil {
		t.Fatal(err)
	}
	if _, err := l.DecodeImage(pngData.Bytes()); !errors.Is(err, session.ErrLimitExceeded) {
		t.Fatal("cumulative pixel limit", err)
	}
	l = loader(t, session.Limits{MaxDecodedBytes: 31})
	if _, err := l.DecodeImage(pngData.Bytes()); !errors.Is(err, session.ErrLimitExceeded) {
		t.Fatal("decoded limit", err)
	}
	l = loader(t, session.Limits{MaxImagePixels: 3})
	uri := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngData.Bytes())
	data, err := l.Load(Image, uri)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.DecodeImage(data); !errors.Is(err, session.ErrLimitExceeded) {
		t.Fatal("pixel limit", err)
	}
	if _, err := loader(t, session.Limits{}).DecodeImage([]byte("broken")); !errors.Is(err, session.ErrInvalidInput) {
		t.Fatal(err)
	}
}

type idleReader struct{}

func (idleReader) Read([]byte) (int, error) { return 0, nil }

func TestAttemptAndCSSFontLimits(t *testing.T) {
	l := loader(t, session.Limits{MaxAssets: 1})
	if _, err := l.Load(Image, "denied"); !errors.Is(err, session.ErrMissingResource) {
		t.Fatal(err)
	}
	if _, err := l.Load(Image, "data:,a"); !errors.Is(err, session.ErrLimitExceeded) {
		t.Fatal("failed attempt bypassed quota", err)
	}
	l = loader(t, session.Limits{})
	r := &trackedReader{Reader: idleReader{}}
	l.Resolver = resolverFunc(func(context.Context, string) (io.ReadCloser, error) { return r, nil })
	if _, err := l.Load(Image, "idle"); !errors.Is(err, io.ErrNoProgress) || !r.closed {
		t.Fatal(err)
	}
	l = loader(t, session.Limits{MaxCSSBytes: 3})
	if _, err := l.Load(Stylesheet, "data:,ab"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Load(Stylesheet, "data:,cd"); !errors.Is(err, session.ErrLimitExceeded) {
		t.Fatal("aggregate CSS bypass", err)
	}
	l = loader(t, session.Limits{MaxFontBytes: 2})
	if _, err := l.Load(Font, "data:,abc"); !errors.Is(err, session.ErrLimitExceeded) {
		t.Fatal("font bound", err)
	}
	l = loader(t, session.Limits{MaxFonts: 1})
	if _, err := l.Load(Font, "data:,x"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Load(Font, "data:,y"); !errors.Is(err, session.ErrLimitExceeded) {
		t.Fatal("font count", err)
	}
}

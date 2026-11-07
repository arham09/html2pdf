package session

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"testing"
)

func TestQuotaBoundariesAndOverflow(t *testing.T) {
	s, err := New(context.Background(), Limits{MaxLayoutWork: math.MaxInt64 / 2})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Charge("layout_work", math.MaxInt64/2); err != nil {
		t.Fatal(err)
	}
	for _, n := range []int64{1, math.MaxInt64 / 2, math.MaxInt64} {
		if err := s.Charge("layout_work", n); !errors.Is(err, ErrLimitExceeded) {
			t.Fatalf("n=%d: %v", n, err)
		}
	}
	if err := s.Charge("layout_work", -1); !errors.Is(err, ErrInvalidInput) {
		t.Fatal(err)
	}
	if err := s.Charge("unknown", 1); !errors.Is(err, ErrInvalidInput) {
		t.Fatal(err)
	}
	if err := s.CheckSize("dom_depth", 128); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckSize("dom_depth", 129); !errors.Is(err, ErrLimitExceeded) {
		t.Fatal(err)
	}
	if _, err := Normalize(Limits{MaxAssetBytes: math.MaxInt64}); !errors.Is(err, ErrInvalidOptions) {
		t.Fatal(err)
	}
}

func TestBoundedDiagnosticsAndOutput(t *testing.T) {
	s, err := New(context.Background(), Limits{MaxWarnings: 1, MaxOutputBytes: 3})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Warn(Warning{Code: "first"}); err != nil {
		t.Fatal(err)
	}
	copy := s.Warnings()
	copy[0].Code = "changed"
	if err := s.Warn(Warning{Code: "second"}); !errors.Is(err, ErrLimitExceeded) || s.Warnings()[0].Code != "first" {
		t.Fatal("warning cap/ownership broken")
	}
	b := NewBuffer(s)
	if _, err := b.Write([]byte("abc")); err != nil {
		t.Fatal(err)
	}
	data, err := b.Bytes()
	if err != nil || string(data) != "abc" {
		t.Fatal(err, string(data))
	}
	data[0] = 'x'
	again, _ := b.Bytes()
	if string(again) != "abc" {
		t.Fatal("buffer alias")
	}
	if _, err := b.Write([]byte("d")); !errors.Is(err, ErrLimitExceeded) {
		t.Fatal(err)
	}
	if data, err := b.Bytes(); data != nil || !errors.Is(err, ErrLimitExceeded) {
		t.Fatal("buffer exposed partial bytes after failure")
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

func TestWriterCancellationAndFailures(t *testing.T) {
	boom := errors.New("write failed")
	for _, tc := range []struct {
		writer io.Writer
		want   error
	}{
		{writerFunc(func([]byte) (int, error) { return 0, boom }), boom},
		{writerFunc(func([]byte) (int, error) { return 0, nil }), io.ErrShortWrite},
	} {
		s, _ := New(context.Background(), Limits{})
		if _, err := (Writer{s, tc.writer}).Write([]byte("a")); !errors.Is(err, tc.want) {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	s, _ := New(ctx, Limits{})
	w := writerFunc(func(p []byte) (int, error) { cancel(); return len(p), nil })
	if _, err := (Writer{s, w}).Write([]byte("a")); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	var dst bytes.Buffer
	if _, err := (Writer{s, &dst}).Write([]byte("a")); !errors.Is(err, context.Canceled) || dst.Len() != 0 {
		t.Fatal("canceled write touched destination")
	}
	if _, err := NewBuffer(s).Bytes(); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

package html

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/arham09/html2pdf/internal/session"
)

func TestStaticNormalization(t *testing.T) {
	s, _ := session.New(context.Background(), session.Limits{})
	doc, err := Parse(s, `<head><title>secret</title><style>p{color:red}</style></head><p onclick="fetch('x')">Hello <strong>world<p>Next<script>fetch('https://example.test')</script><template><style>p{color:blue}</style>hidden</template><custom>kept</custom>`)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Stylesheets) != 1 {
		t.Fatalf("styles lost: %v", doc.Stylesheets)
	}
	var text strings.Builder
	ids := map[uint64]bool{}
	for _, n := range doc.Nodes {
		if ids[n.ID] {
			t.Fatal("duplicate source ID")
		}
		ids[n.ID] = true
		text.WriteString(n.Text)
		if n.Attributes["onclick"] != "" {
			t.Fatal("active attribute retained")
		}
	}
	if !strings.Contains(text.String(), "Hello worldNextkept") || strings.Contains(text.String(), "secret") || strings.Contains(text.String(), "fetch") || strings.Contains(text.String(), "hidden") {
		t.Fatal(text.String())
	}
	if len(s.Warnings()) != 1 {
		t.Fatal("wrapper diagnostic", s.Warnings())
	}
}

func TestHTMLBounds(t *testing.T) {
	for _, tc := range []struct {
		limits session.Limits
		input  string
	}{
		{session.Limits{MaxDOMDepth: 3}, strings.Repeat("<div>", 4)},
		{session.Limits{MaxDOMNodes: 5}, `<p>Hello</p>`},
		{session.Limits{MaxHTMLBytes: 2}, "abc"},
		{session.Limits{MaxTableRows: 1}, `<table><tr><td>1<tr><td>2</table>`},
	} {
		s, _ := session.New(context.Background(), tc.limits)
		if _, err := Parse(s, tc.input); !errors.Is(err, session.ErrLimitExceeded) {
			t.Fatal(tc, err)
		}
	}
	s, _ := session.New(context.Background(), session.Limits{})
	if _, err := Parse(s, `<link rel=stylesheet href="https://example.test">`); !errors.Is(err, session.ErrUnsupportedFeature) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s, _ = session.New(ctx, session.Limits{})
	cancel()
	if _, err := Parse(s, "a"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func FuzzParse(f *testing.F) {
	for _, seed := range []string{"<p>Hello", `<table><b><tr><td>x`, strings.Repeat("<b>", 10), `<script><style>x</style></script>`, "\xff"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > 4096 {
			return
		}
		s, _ := session.New(context.Background(), session.Limits{MaxHTMLBytes: 4096, MaxLayoutWork: 10000, MaxDOMNodes: 5000, MaxDOMDepth: 64, MaxWarnings: 20})
		_, _ = Parse(s, input)
	})
}

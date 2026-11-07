package font

import (
	"context"
	"github.com/arham09/html2pdf/internal/session"
	"strings"
	"testing"
)

func TestRealFourFaceRegistry(t *testing.T) {
	s, _ := session.New(context.Background(), session.Limits{})
	r := NewRegistry(s, nil)
	names := map[string]bool{}
	for _, style := range []struct {
		weight int
		italic bool
		name   string
	}{{400, false, "Regular"}, {700, false, "Bold"}, {400, true, "Italic"}, {700, true, "BoldItalic"}} {
		_, f, err := r.Resolve("Noto Sans", style.weight, style.italic)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(f.Name(), style.name) {
			t.Fatal(f.Name())
		}
		names[f.Name()] = true
	}
	if len(names) != 4 {
		t.Fatal("synthetic or duplicate font styles")
	}
}

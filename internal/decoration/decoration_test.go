package decoration

import (
	"context"
	dom "github.com/arham09/html2pdf/internal/html"
	"github.com/arham09/html2pdf/internal/session"
	"testing"
)

func TestCountersTextOnlyAndIndependentPages(t *testing.T) {
	s, _ := session.New(context.Background(), session.Limits{})
	original, err := dom.Parse(s, `<div title="{page}" style="font-family:'{pages}'">Page {page} of {pages}</div>`)
	if err != nil {
		t.Fatal(err)
	}
	for _, page := range []int{9, 10} {
		copy, err := counters(s, original, page, 10)
		if err != nil {
			t.Fatal(err)
		}
		for i, n := range original.Nodes {
			if copy.Nodes[i].ID == n.ID {
				t.Fatal("source identity reused")
			}
			if n.Tag == "div" && (copy.Nodes[i].Attributes["title"] != "{page}" || copy.Nodes[i].Attributes["style"] != n.Attributes["style"]) {
				t.Fatal("attribute substitution")
			}
			if n.Text != "" {
				want := "Page 9 of 10"
				if page == 10 {
					want = "Page 10 of 10"
				}
				if copy.Nodes[i].Text != want || n.Text != "Page {page} of {pages}" {
					t.Fatal("text substitution mutated source")
				}
			}
		}
	}
}

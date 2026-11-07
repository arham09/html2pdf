// Package decoration lays out running content after the body page count is final.
package decoration

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/arham09/html2pdf/internal/font"
	dom "github.com/arham09/html2pdf/internal/html"
	"github.com/arham09/html2pdf/internal/layout"
	"github.com/arham09/html2pdf/internal/pagination"
	"github.com/arham09/html2pdf/internal/paint"
	"github.com/arham09/html2pdf/internal/resource"
	"github.com/arham09/html2pdf/internal/session"
)

type Part struct {
	Document *dom.Document
	Region   layout.Rect
	Before   bool
}

func Apply(s *session.Session, pages []pagination.Page, fonts *font.Registry, loader resource.Loader, parts ...Part) error {
	for i := range pages {
		for _, part := range parts {
			if part.Document == nil {
				continue
			}
			tree, err := counters(s, part.Document, i+1, len(pages))
			if err != nil {
				return err
			}
			page := &pages[i].Paint
			result, err := layout.BuildRegion(s, tree, fonts, loader, page.WidthPt, page.HeightPt, part.Region)
			if err != nil {
				return fmt.Errorf("decoration on page %d: %w", i+1, err)
			}
			for id, img := range result.Page.Images {
				page.Images[id] = img
			}
			first := len(page.Operations)
			for _, op := range result.Page.Operations {
				if err := pagination.Append(s, page, op); err != nil {
					return err
				}
			}
			if part.Before {
				prefix := append([]paint.Operation(nil), page.Operations[first:]...)
				copy(page.Operations[len(prefix):], page.Operations[:first])
				copy(page.Operations, prefix)
			}
		}
	}
	return nil
}

// Only text nodes are substituted. Prepared styles, URIs and attributes stay
// untouched, and each page gets independent source IDs/resources.
func counters(s *session.Session, input *dom.Document, page, total int) (*dom.Document, error) {
	output := &dom.Document{}
	var clone func(*dom.Node, *dom.Node) (*dom.Node, error)
	clone = func(n, parent *dom.Node) (*dom.Node, error) {
		if err := s.Charge("dom_nodes", 1); err != nil {
			return nil, err
		}
		if err := s.Charge("layout_work", int64(len(n.Text))+1); err != nil {
			return nil, err
		}
		c := *n
		c.ID = s.NextSourceID()
		c.Parent = parent
		c.Children = nil
		c.Text = strings.ReplaceAll(strings.ReplaceAll(n.Text, "{pages}", strconv.Itoa(total)), "{page}", strconv.Itoa(page))
		output.Nodes = append(output.Nodes, &c)
		for _, child := range n.Children {
			copy, err := clone(child, &c)
			if err != nil {
				return nil, err
			}
			c.Children = append(c.Children, copy)
		}
		return &c, nil
	}
	root, err := clone(input.Root, nil)
	if err != nil {
		return nil, err
	}
	output.Root = root
	return output, nil
}

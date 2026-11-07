package html2pdf

import (
	"fmt"

	"github.com/arham09/html2pdf/internal/css"
	dom "github.com/arham09/html2pdf/internal/html"
	"github.com/arham09/html2pdf/internal/session"
)

type prepared struct {
	Body, Header, Footer *dom.Document
	Page                 PaperSize
	Margins              Margins
}

func (e *Engine) prepare(s *session.Session, doc Document) (*prepared, error) {
	body, err := dom.Parse(s, doc.HTML)
	if err != nil {
		return nil, err
	}
	var sheets css.Sheet
	for _, text := range body.Stylesheets {
		if err := sheets.Add(s, text, e.options.StrictCSS); err != nil {
			return nil, err
		}
	}
	// API-supplied sheets are ordered after embedded author sheets. Inline
	// declarations keep their own higher specificity within each importance level.
	for _, text := range doc.Stylesheets {
		if err := sheets.Add(s, text, e.options.StrictCSS); err != nil {
			return nil, err
		}
	}
	pageCSS, err := sheets.Compute(s, body, e.options.StrictCSS)
	if err != nil {
		return nil, err
	}
	out := &prepared{Body: body}
	out.Page, out.Margins = resolvedPageCSS(e.options, pageCSS)
	if err := validatePage(out.Page, out.Margins, e.options.Header, e.options.Footer); err != nil {
		return nil, fmt.Errorf("%w: resolved page: %w", ErrInvalidInput, err)
	}
	for _, part := range []struct {
		html   string
		target **dom.Document
	}{{e.options.Header.HTML, &out.Header}, {e.options.Footer.HTML, &out.Footer}} {
		if part.html == "" {
			continue
		}
		tree, err := dom.Parse(s, part.html)
		if err != nil {
			return nil, err
		}
		// Decoration-local styles do not mutate the body's stylesheet/order state.
		local := sheets.Clone()
		for _, text := range tree.Stylesheets {
			if err := local.Add(s, text, e.options.StrictCSS); err != nil {
				return nil, err
			}
		}
		if _, err := local.Compute(s, tree, e.options.StrictCSS); err != nil {
			return nil, err
		}
		*part.target = tree
	}
	return out, nil
}

func resolvedPageCSS(options Options, cssPage css.Page) (PaperSize, Margins) {
	page, m := resolvedPage(Options{})
	if cssPage.WidthPt != nil && cssPage.HeightPt != nil {
		page = PaperSize{*cssPage.WidthPt, *cssPage.HeightPt}
	}
	fields := []*float64{&m.TopPt, &m.RightPt, &m.BottomPt, &m.LeftPt}
	for i, v := range cssPage.Margins {
		if v != nil {
			*fields[i] = *v
		}
	}
	if options.PageSize != nil {
		page = *options.PageSize
	}
	if options.Margins != nil {
		m = *options.Margins
	}
	return page, m
}

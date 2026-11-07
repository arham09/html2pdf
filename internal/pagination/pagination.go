// Package pagination places measured logical flow into bounded physical pages.
package pagination

import (
	"fmt"
	"math"
	"sort"

	"github.com/arham09/html2pdf/internal/layout"
	"github.com/arham09/html2pdf/internal/paint"
	"github.com/arham09/html2pdf/internal/session"
)

const epsilon = 0.0001

// Continuation identifies a source row interval, never an index into copies.
type Continuation struct {
	Source     layout.SourceID
	Start, End int
}
type Fragment struct {
	Content            Continuation
	Bounds             layout.Rect
	RepeatedDecoration bool
}
type Page struct {
	Number             int
	Body               layout.Rect
	FlowStart, FlowEnd float64
	HeaderHeight       float64
	Fragments          []Fragment
	Paint              paint.Page
}
type interval struct {
	source     layout.SourceID
	start, end float64
}
type heading struct {
	table      layout.Table
	start, end float64
	count      int
}

func Build(s *session.Session, flow layout.Result, body layout.Rect) ([]Page, error) {
	if body.Width <= 0 || body.Height <= 0 {
		return nil, fmt.Errorf("%w: empty pagination region", session.ErrInvalidInput)
	}
	var hard, keep []interval
	end := body.Y
	add := func(id layout.SourceID, r layout.Rect) {
		if r.Height > epsilon {
			hard = append(hard, interval{id, r.Y, r.Y + r.Height})
		}
	}
	for _, line := range flow.Lines {
		add(line.Source, line.Bounds)
		end = math.Max(end, line.Bounds.Y+line.Bounds.Height)
	}
	for _, atom := range flow.Atoms {
		add(atom.Source, atom.Bounds)
		end = math.Max(end, atom.Bounds.Y+atom.Bounds.Height)
	}
	for _, box := range flow.Boxes {
		if box.Style.BreakInside == "avoid" {
			keep = append(keep, interval{box.Source, box.Bounds.Y, box.Bounds.Y + box.Bounds.Height})
		}
	}
	var headings []heading
	for _, table := range flow.Tables {
		h := heading{table: table, start: table.Bounds.Y, end: table.Bounds.Y}
		for i, row := range table.Rows {
			add(row.Source, row.Bounds)
			if row.Header {
				h.count = i + 1
				h.end = row.Bounds.Y + row.Bounds.Height
			}
		}
		if h.count > 0 {
			protected := h.end
			if h.count < len(table.Rows) {
				row := table.Rows[h.count]
				protected = row.Bounds.Y + row.Bounds.Height
			}
			hard = append(hard, interval{table.Source, h.start, protected})
			headings = append(headings, h)
		}
	}
	for _, op := range flow.Page.Operations {
		if op.Kind != paint.DrawGlyphRun {
			end = math.Max(end, op.Bounds.Y+op.Bounds.Height)
		}
		if op.Kind == paint.DrawImage {
			add(op.Source, op.Bounds)
		}
	}
	if err := s.Charge("layout_work", int64(len(hard)+len(keep)+len(flow.Events))); err != nil {
		return nil, err
	}
	sort.SliceStable(hard, func(i, j int) bool { return hard[i].start < hard[j].start })
	// Merge overlapping footprints, preserving legal boundaries where items touch.
	merged := make([]interval, 0, len(hard))
	for _, r := range hard {
		if len(merged) > 0 && r.start < merged[len(merged)-1].end-epsilon {
			last := &merged[len(merged)-1]
			last.end = math.Max(last.end, r.end)
		} else {
			merged = append(merged, r)
		}
	}
	hard = merged
	var forced []float64
	for _, event := range flow.Events {
		if event.Mode == "page" {
			forced = append(forced, event.Y)
			continue
		}
		// Avoid before/after binds the closest measured neighbors when feasible.
		var before, after *interval
		for i := range hard {
			if err := s.Charge("layout_work", 1); err != nil {
				return nil, err
			}
			r := &hard[i]
			if r.end <= event.Y+epsilon && (before == nil || r.end > before.end) {
				before = r
			}
			if r.start >= event.Y-epsilon && (after == nil || r.start < after.start) {
				after = r
			}
		}
		if before != nil && after != nil {
			keep = append(keep, interval{event.Source, before.start, after.end})
		}
	}
	sort.Float64s(forced)
	retreat := func(target float64) (float64, error) {
		for _, r := range hard {
			if err := s.Charge("layout_work", 1); err != nil {
				return 0, err
			}
			if r.start < target-epsilon && r.end > target+epsilon {
				return r.start, nil
			}
		}
		return target, nil
	}
	warned := map[layout.SourceID]bool{}
	var pages []Page
	for start := body.Y; start < end-epsilon || len(pages) == 0; {
		if err := s.Charge("pages", 1); err != nil {
			return nil, err
		}
		var repeat *heading
		for i := range headings {
			h := &headings[i]
			if h.count < len(h.table.Rows) && h.end > h.start+epsilon && start >= h.end-epsilon && start < h.table.Bounds.Y+h.table.Bounds.Height-epsilon {
				repeat = h
				break
			}
		}
		headerHeight := 0.0
		if repeat != nil {
			headerHeight = repeat.end - repeat.start
		}
		capacity := body.Height - headerHeight
		if capacity <= epsilon {
			return nil, fmt.Errorf("%w: repeated heading leaves no body space", session.ErrUnbreakableContent)
		}
		target := math.Min(end, start+capacity)
		forcedAt := math.Inf(1)
		for _, y := range forced {
			if y > start+epsilon {
				forcedAt = y
				break
			}
		}
		target = math.Min(target, forcedAt)
		var err error
		target, err = retreat(target)
		if err != nil {
			return nil, err
		}
		for {
			previous := target
			for _, r := range keep {
				if err := s.Charge("layout_work", 1); err != nil {
					return nil, err
				}
				if r.start >= target-epsilon || r.end <= target+epsilon {
					continue
				}
				hasForced := false
				for _, y := range forced {
					if y > r.start+epsilon && y < r.end-epsilon {
						hasForced = true
						break
					}
				}
				if hasForced {
					continue
				}
				// Feasibility uses a fresh page at this source, not the header
				// space already consumed by an earlier table on this page.
				fitCapacity := body.Height
				for _, h := range headings {
					if err := s.Charge("layout_work", 1); err != nil {
						return nil, err
					}
					if r.start >= h.end-epsilon && r.start < h.table.Bounds.Y+h.table.Bounds.Height-epsilon {
						fitCapacity -= h.end - h.start
						break
					}
				}
				if r.end-r.start > fitCapacity+epsilon {
					if !warned[r.source] {
						if err := s.Warn(session.Warning{Code: "avoid-relaxed", Message: "Oversized avoid region fragmented at legal boundaries", SourceID: uint64(r.source)}); err != nil {
							return nil, err
						}
						warned[r.source] = true
					}
					continue
				}
				if r.start > start+epsilon {
					target = r.start
				}
			}
			target, err = retreat(target)
			if err != nil {
				return nil, err
			}
			if math.Abs(previous-target) < epsilon {
				break
			}
		}
		if target <= start+epsilon && end > start+epsilon {
			return nil, fmt.Errorf("%w: an indivisible line, row, heading or atomic item exceeds an empty page", session.ErrUnbreakableContent)
		}
		page := Page{Number: len(pages) + 1, Body: body, FlowStart: start, FlowEnd: target, HeaderHeight: headerHeight, Paint: paint.Page{WidthPt: flow.Page.WidthPt, HeightPt: flow.Page.HeightPt, Fonts: flow.Page.Fonts, Images: flow.Page.Images}}
		if repeat != nil {
			if err := slice(s, &page, flow, repeat.start, repeat.end, body.Y, true, false); err != nil {
				return nil, err
			}
			for i := 0; i < repeat.count; i++ {
				row := repeat.table.Rows[i]
				r := row.Bounds
				r.Y += body.Y - repeat.start
				page.Fragments = append(page.Fragments, Fragment{Content: Continuation{Source: row.Source, Start: i, End: i + 1}, Bounds: r, RepeatedDecoration: true})
			}
		}
		closeEnd, closeStart := false, false
		for _, table := range flow.Tables {
			if target > table.Bounds.Y+epsilon && target < table.Bounds.Y+table.Bounds.Height-epsilon {
				closeEnd = true
			}
			if start > table.Bounds.Y+epsilon && start < table.Bounds.Y+table.Bounds.Height-epsilon {
				closeStart = true
			}
		}
		if err := slice(s, &page, flow, start, target, body.Y+headerHeight, closeEnd, closeStart); err != nil {
			return nil, err
		}
		for _, table := range flow.Tables {
			for i, row := range table.Rows {
				if row.Bounds.Y >= start-epsilon && row.Bounds.Y+row.Bounds.Height <= target+epsilon && (row.Bounds.Height > epsilon || row.Bounds.Y < target-epsilon || target >= end-epsilon) {
					r := row.Bounds
					r.Y += body.Y + headerHeight - start
					page.Fragments = append(page.Fragments, Fragment{Content: Continuation{Source: row.Source, Start: i, End: i + 1}, Bounds: r})
				}
			}
		}
		pages = append(pages, page)
		if target >= end-epsilon {
			break
		}
		start = target
	}
	return pages, nil
}

func slice(s *session.Session, page *Page, flow layout.Result, lo, hi, destination float64, closeEnd, closeStart bool) error {
	for _, op := range flow.Page.Operations {
		if err := s.Charge("layout_work", 1); err != nil {
			return err
		}
		r := op.Bounds
		switch op.Kind {
		case paint.DrawGlyphRun:
			if r.Y < lo-epsilon || r.Y >= hi-epsilon {
				continue
			}
		case paint.DrawImage:
			if r.Y < lo-epsilon || r.Y >= hi-epsilon {
				continue
			}
			if r.Y+r.Height > hi+epsilon {
				return fmt.Errorf("%w: image crossed a page cut", session.ErrUnbreakableContent)
			}
		case paint.FillRect:
			top, bottom := math.Max(r.Y, lo), math.Min(r.Y+r.Height, hi)
			if bottom <= top+epsilon {
				continue
			}
			if op.Border && r.Width > r.Height {
				if closeStart && r.Y < lo-epsilon && r.Y+r.Height > lo+epsilon {
					if page.HeaderHeight > 0 {
						continue
					} // original header's closing edge owns the join
					bottom = math.Min(hi, lo+r.Height)
				}
				if closeEnd && r.Y < hi-epsilon && r.Y+r.Height > hi+epsilon {
					top = math.Max(lo, hi-r.Height)
				}
			}
			op.Bounds.Y = top
			op.Bounds.Height = bottom - top
		default:
			return fmt.Errorf("%w: unsupported pagination operation", session.ErrUnsupportedFeature)
		}
		op.Bounds.Y += destination - lo
		if err := Append(s, &page.Paint, op); err != nil {
			return err
		}
	}
	return nil
}

// Append validates final physical geometry and accounts every painted copy.
func Append(s *session.Session, page *paint.Page, op paint.Operation) error {
	r := op.Bounds
	if !finite(r.X+r.Y+r.Width+r.Height) || r.Width < 0 || r.Height < 0 || r.X < -epsilon || r.Y < -epsilon || r.X+r.Width > page.WidthPt+epsilon || r.Y+r.Height > page.HeightPt+epsilon {
		return fmt.Errorf("%w: painted item exceeds physical page", session.ErrUnbreakableContent)
	}
	if op.Kind == paint.DrawGlyphRun {
		if err := s.Charge("glyphs", int64(len(op.Glyphs.Glyphs))); err != nil {
			return err
		}
		pen := r.X
		for _, g := range op.Glyphs.Glyphs {
			if err := s.Charge("layout_work", 1); err != nil {
				return err
			}
			if g.InkWidthPt > 0 && g.InkHeightPt > 0 {
				x, y := pen+g.InkLeftPt, r.Y-g.InkTopPt
				if x < -epsilon || y < -epsilon || x+g.InkWidthPt > page.WidthPt+epsilon || y+g.InkHeightPt > page.HeightPt+epsilon {
					return fmt.Errorf("%w: glyph ink exceeds physical page", session.ErrUnbreakableContent)
				}
			}
			pen += g.AdvancePt
		}
	}
	if err := s.Charge("display_ops", 1); err != nil {
		return err
	}
	page.Operations = append(page.Operations, op)
	return nil
}
func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

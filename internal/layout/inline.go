package layout

import (
	"fmt"
	"math"
	"strings"

	"github.com/arham09/html2pdf/internal/font"
	dom "github.com/arham09/html2pdf/internal/html"
	"github.com/arham09/html2pdf/internal/paint"
	"github.com/arham09/html2pdf/internal/session"
	"github.com/arham09/html2pdf/internal/style"
	"github.com/go-text/typesetting/segmenter"
)

type piece struct {
	text   string
	style  style.Computed
	source SourceID
	fontID string
	run    font.Run
}
type unit struct {
	pieces                         []piece
	source                         SourceID
	space, collapse, hard, atomic  bool
	asset                          string
	width, height, ascent, descent float64
	ops                            []paint.Operation
	boxes                          []Box
	lines                          []Line
	breaks                         []BreakOpportunity
	tables                         []Table
	atoms                          []Region
}

func (b *builder) collect(nodes []*dom.Node, available float64) ([]unit, error) {
	var units []unit
	appendText := func(text string, n *dom.Node, space, collapse bool) {
		if text == "" {
			return
		}
		if len(units) > 0 {
			last := &units[len(units)-1]
			if len(last.pieces) > 0 && !last.atomic && !last.hard && !last.space {
				p := &last.pieces[len(last.pieces)-1]
				if p.style.WhiteSpace == "nowrap" && strings.HasSuffix(p.text, " ") {
					if n.Style.WhiteSpace == "nowrap" && !space {
						text = strings.TrimPrefix(text, " ")
					} else if collapse || n.Style.WhiteSpace == "normal" || n.Style.WhiteSpace == "pre-line" {
						p.text = strings.TrimSuffix(p.text, " ")
						if !space {
							units = append(units, unit{space: true, collapse: true, source: SourceID(n.ID), pieces: []piece{{text: " ", style: n.Style, source: SourceID(n.ID)}}})
						}
					}
				}
			}
		}
		if space {
			if collapse && len(units) > 0 && units[len(units)-1].space && units[len(units)-1].collapse {
				return
			}
			units = append(units, unit{space: true, collapse: collapse, source: SourceID(n.ID), pieces: []piece{{text: text, style: n.Style, source: SourceID(n.ID)}}})
			return
		}
		if len(units) == 0 || units[len(units)-1].space || units[len(units)-1].hard || units[len(units)-1].atomic {
			units = append(units, unit{source: SourceID(n.ID)})
		}
		u := &units[len(units)-1]
		if len(u.pieces) > 0 {
			p := &u.pieces[len(u.pieces)-1]
			if p.style == n.Style && p.source == SourceID(n.ID) {
				p.text += text
				return
			}
		}
		u.pieces = append(u.pieces, piece{text: text, style: n.Style, source: SourceID(n.ID)})
	}
	var walk func(*dom.Node) error
	walk = func(n *dom.Node) error {
		if n.Style.Display == style.None {
			return nil
		}
		if err := b.validate(n); err != nil {
			return err
		}
		if n.Tag == "br" {
			units = append(units, unit{hard: true, source: SourceID(n.ID), height: n.Style.LineHeightPt})
			return nil
		}
		if n.Tag == "img" {
			u, err := b.image(n, available)
			if err != nil {
				return err
			}
			u.ascent = u.height
			units = append(units, u)
			return nil
		}
		if n.Style.Display == style.InlineBlock {
			u, err := b.inlineBlock(n, available)
			if err != nil {
				return err
			}
			units = append(units, u)
			return nil
		}
		if isBlock(n) {
			return unsupportedInline("block inside inline formatting")
		}
		if n.Tag != "" && inlineDecoration(n.Style) {
			return unsupportedInline("box decoration")
		}
		if n.Text != "" {
			mode := n.Style.WhiteSpace
			preserve := mode == "pre" || mode == "pre-wrap"
			newlines := preserve || mode == "pre-line"
			noWrap := mode == "pre" || mode == "nowrap"
			// Scan contiguous spans once; no per-character string concatenation.
			text := strings.ReplaceAll(n.Text, "\r\n", "\n")
			if mode == "nowrap" {
				var normalized strings.Builder
				space := false
				for _, r := range text {
					ws := r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\f'
					if ws {
						if !space {
							normalized.WriteByte(' ')
						}
					} else {
						normalized.WriteRune(r)
					}
					space = ws
				}
				text = normalized.String()
			}
			runes := []rune(text)
			start := 0
			flush := func(end int) { appendText(string(runes[start:end]), n, false, false) }
			for i, r := range runes {
				if r == '\r' {
					r = '\n'
					runes[i] = r
				}
				ws := r == ' ' || r == '\n' || r == '\t' || r == '\f'
				if !ws {
					continue
				}
				if noWrap && !(newlines && r == '\n') {
					if !preserve {
						runes[i] = ' '
					}
					continue
				}
				flush(i)
				if newlines && r == '\n' {
					units = append(units, unit{hard: true, source: SourceID(n.ID), height: n.Style.LineHeightPt})
				} else if preserve {
					if r == '\t' {
						return unsupportedInline("tab stops")
					}
					appendText(string(r), n, true, false)
				} else {
					appendText(" ", n, true, true)
				}
				start = i + 1
			}
			flush(len(runes))
		}
		for _, child := range n.Children {
			if err := walk(child); err != nil {
				return err
			}
		}
		return nil
	}
	for _, node := range nodes {
		if err := walk(node); err != nil {
			return nil, err
		}
	}
	for i := range units {
		if err := b.measure(&units[i]); err != nil {
			return nil, err
		}
	}
	return units, nil
}

func (b *builder) measure(u *unit) error {
	if u.atomic || u.hard {
		return nil
	}
	u.width, u.height, u.ascent, u.descent = 0, 0, 0, 0
	for i := range u.pieces {
		p := &u.pieces[i]
		id, run, err := b.fonts.Shape(p.text, p.style.FontFamily, p.style.FontWeight, p.style.Italic, p.style.FontSizePt)
		if err != nil {
			return err
		}
		p.fontID, p.run = id, run
		u.width += run.AdvancePt
		u.ascent = math.Max(u.ascent, run.AscentPt)
		u.descent = math.Max(u.descent, run.DescentPt)
		u.height = math.Max(u.height, p.style.LineHeightPt)
	}
	u.height = math.Max(u.height, u.ascent+u.descent)
	return nil
}

func (b *builder) inline(nodes []*dom.Node, owner style.Computed, x, y, width float64) (float64, error) {
	units, err := b.collect(nodes, width)
	if err != nil {
		return 0, err
	}
	var line []unit
	used := 0.0
	top := y
	canBreak := false
	lineSource := SourceID(0)
	// The owner source is stable even when a line starts with a nested span.
	if len(nodes) > 0 && nodes[0].Parent != nil {
		lineSource = SourceID(nodes[0].Parent.ID)
	}
	flush := func(force bool) error {
		for len(line) > 0 && line[len(line)-1].space && line[len(line)-1].collapse {
			used -= line[len(line)-1].width
			line = line[:len(line)-1]
		}
		if len(line) > 0 {
			u := &line[len(line)-1]
			if !u.atomic && len(u.pieces) > 0 {
				p := &u.pieces[len(u.pieces)-1]
				if p.style.WhiteSpace == "nowrap" {
					old := u.width
					p.text = strings.TrimRight(p.text, " ")
					if err := b.measure(u); err != nil {
						return err
					}
					used += u.width - old
				}
			}
		}
		if len(line) == 0 && !force {
			return nil
		}
		asc, desc, height := 0.0, 0.0, owner.LineHeightPt
		for _, u := range line {
			asc = math.Max(asc, u.ascent)
			desc = math.Max(desc, u.descent)
			height = math.Max(height, u.height)
		}
		height = math.Max(height, asc+desc)
		baseline := top + asc + (height-asc-desc)/2
		if len(line) == 0 {
			height = owner.LineHeightPt
			baseline = top + height
		}
		start := x
		if owner.TextAlign == "right" {
			start += width - used
		} else if owner.TextAlign == "center" {
			start += (width - used) / 2
		}
		var text strings.Builder
		cursor := start
		for _, u := range line {
			if u.atomic {
				if err := b.placeAtomic(u, cursor, baseline-u.ascent); err != nil {
					return err
				}
				for _, l := range u.lines {
					text.WriteString(l.Text)
				}
			} else {
				for _, p := range u.pieces {
					if err := b.glyph(p.fontID, p.run, p.style.Color, p.source, cursor, baseline); err != nil {
						return err
					}
					cursor += p.run.AdvancePt
					text.WriteString(p.text)
				}
			}
			if u.atomic {
				cursor += u.width
			}
		}
		if err := b.check(Rect{X: x, Y: top, Width: width, Height: height}); err != nil {
			return err
		}
		b.result.Lines = append(b.result.Lines, Line{Source: lineSource, Bounds: Rect{X: start, Y: top, Width: used, Height: height}, Baseline: baseline, Text: text.String()})
		b.result.Breaks = append(b.result.Breaks, BreakOpportunity{Source: lineSource, Kind: LineBoundary, Offset: len(b.result.Lines)})
		top += height
		line = nil
		used = 0
		canBreak = false
		return nil
	}
	for i := 0; i < len(units); i++ {
		if err := b.s.Charge("layout_work", 1); err != nil {
			return 0, err
		}
		u := units[i]
		if len(line) == 0 && !u.atomic && !u.hard && len(u.pieces) > 0 && u.pieces[0].style.WhiteSpace == "nowrap" {
			u.pieces[0].text = strings.TrimLeft(u.pieces[0].text, " ")
			if err := b.measure(&u); err != nil {
				return 0, err
			}
			if u.width == 0 {
				continue
			}
		}
		if u.hard {
			if err := flush(true); err != nil {
				return 0, err
			}
			continue
		}
		if u.space && u.collapse && len(line) == 0 {
			continue
		}
		if used+u.width > width+0.0001 {
			if u.space {
				if err := flush(false); err != nil {
					return 0, err
				}
				if u.collapse {
					continue
				}
			} else if (canBreak || u.atomic) && len(line) > 0 {
				if err := flush(false); err != nil {
					return 0, err
				}
			}
			if used+u.width > width+0.0001 {
				split := !u.atomic && len(u.pieces) == 1 && u.pieces[0].style.WhiteSpace != "pre" && u.pieces[0].style.WhiteSpace != "nowrap" && u.pieces[0].style.OverflowWrap != "normal"
				if !split {
					return 0, fmt.Errorf("%w: inline content exceeds available width", session.ErrUnbreakableContent)
				}
				first, rest, err := b.split(u, width-used)
				if err != nil {
					return 0, err
				}
				line = append(line, first)
				used += first.width
				if err := flush(false); err != nil {
					return 0, err
				}
				units[i] = rest
				i--
				continue
			}
		}
		line = append(line, u)
		used += u.width
		canBreak = u.space || u.atomic
	}
	if err := flush(false); err != nil {
		return 0, err
	}
	return top - y, nil
}

func (b *builder) split(u unit, width float64) (unit, unit, error) {
	p := u.pieces[0]
	runes := []rune(p.text)
	var seg segmenter.Segmenter
	seg.Init(runes)
	iter := seg.GraphemeIterator()
	ends := []int{}
	for iter.Next() {
		g := iter.Grapheme()
		ends = append(ends, g.Offset+len(g.Text))
	}
	lo, hi, best := 0, len(ends)-1, -1
	for lo <= hi {
		mid := (lo + hi) / 2
		probe := unit{source: u.source, pieces: []piece{{text: string(runes[:ends[mid]]), style: p.style, source: p.source}}}
		if err := b.measure(&probe); err != nil {
			return unit{}, unit{}, err
		}
		if probe.width <= width+0.0001 {
			best = mid
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	if best < 0 {
		return unit{}, unit{}, fmt.Errorf("%w: a grapheme exceeds the line", session.ErrUnbreakableContent)
	}
	offset := ends[best]
	first, rest := unit{source: u.source, pieces: []piece{{text: string(runes[:offset]), style: p.style, source: p.source}}}, unit{source: u.source, pieces: []piece{{text: string(runes[offset:]), style: p.style, source: p.source}}}
	if offset == len(runes) {
		return unit{}, unit{}, fmt.Errorf("%w: wrapping failed to progress", session.ErrUnbreakableContent)
	}
	if err := b.measure(&first); err != nil {
		return unit{}, unit{}, err
	}
	if err := b.measure(&rest); err != nil {
		return unit{}, unit{}, err
	}
	return first, rest, nil
}

func (b *builder) glyph(id string, run font.Run, ink style.Color, source SourceID, x, baseline float64) error {
	pen := x
	for _, g := range run.Glyphs {
		if !b.local && g.InkWidthPt > 0 && g.InkHeightPt > 0 {
			left, top := pen+g.InkLeftPt, baseline-g.InkTopPt
			if left < 0 || left+g.InkWidthPt > b.result.Page.WidthPt || !b.flow && (top < 0 || top+g.InkHeightPt > b.result.Page.HeightPt) {
				return fmt.Errorf("%w: glyph ink exceeds page", session.ErrUnbreakableContent)
			}
		}
		pen += g.AdvancePt
	}
	// Glyph operations use a baseline, not a top-left rectangle height.
	return b.emit(paint.Operation{Kind: paint.DrawGlyphRun, Source: source, Bounds: Rect{X: x, Y: baseline, Width: run.AdvancePt}, Color: ink, Glyphs: run, FontID: id})
}

func (b *builder) inlineBlock(n *dom.Node, available float64) (unit, error) {
	margin, padding, border := b.edges(n.Style, available)
	contentWidth := n.Style.Width.Resolve(available, n.Style.FontSizePt, b.rootSize)
	if n.Style.Width.Unit == style.Auto {
		// Intrinsic auto inline-blocks support inline-only children. Explicit widths
		// can also contain block children and reuse the ordinary block algorithm.
		units, err := b.collect(n.Children, available)
		if err != nil {
			return unit{}, err
		}
		line, maxWidth := 0.0, 0.0
		for _, u := range units {
			if u.hard {
				maxWidth = math.Max(maxWidth, line)
				line = 0
			} else {
				line += u.width
			}
		}
		maxWidth = math.Max(maxWidth, line)
		contentWidth = math.Min(maxWidth, available-padding[1]-padding[3]-border[1]-border[3]-margin[1]-margin[3])
	}
	totalWidth := contentWidth + padding[1] + padding[3] + border[1] + border[3] + margin[1] + margin[3]
	if totalWidth < 0 || totalWidth > available+0.0001 {
		return unit{}, fmt.Errorf("%w: inline-block width", session.ErrUnbreakableContent)
	}
	clone := *n
	clone.Style.Width = style.Length{Value: contentWidth, Unit: style.Point}
	clone.Style.Display = style.Block
	u, err := b.localBlock(&clone, totalWidth)
	if err != nil {
		return unit{}, err
	}
	if len(u.lines) > 0 {
		u.ascent = u.lines[len(u.lines)-1].Baseline
	}
	u.descent = u.height - u.ascent
	return u, nil
}

// localBlock measures an atomic fragment without committing its operations or
// geometry. Font/image resources and cumulative work budgets remain shared.
func (b *builder) localBlock(n *dom.Node, totalWidth float64) (unit, error) {
	return b.localBlockSized(n, totalWidth, -1)
}

func (b *builder) localBlockSized(n *dom.Node, totalWidth, contentHeight float64) (unit, error) {
	oldBody := b.body
	oldLocal := b.local
	b.local = true
	opStart, boxStart, lineStart, breakStart := len(b.result.Page.Operations), len(b.result.Boxes), len(b.result.Lines), len(b.result.Breaks)
	tableStart := len(b.result.Tables)
	atomStart := len(b.result.Atoms)
	b.body = Rect{Width: totalWidth, Height: oldBody.Height}
	height, err := b.blockSized(n, 0, 0, totalWidth, contentHeight)
	b.body = oldBody
	b.local = oldLocal
	u := unit{source: SourceID(n.ID), atomic: true, width: totalWidth, height: height, ascent: height,
		ops: append([]paint.Operation(nil), b.result.Page.Operations[opStart:]...), boxes: append([]Box(nil), b.result.Boxes[boxStart:]...), lines: append([]Line(nil), b.result.Lines[lineStart:]...), breaks: append([]BreakOpportunity(nil), b.result.Breaks[breakStart:]...), tables: append([]Table(nil), b.result.Tables[tableStart:]...), atoms: append([]Region(nil), b.result.Atoms[atomStart:]...)}
	for i := range u.breaks {
		if u.breaks[i].Kind == LineBoundary {
			u.breaks[i].Offset -= lineStart
		}
		u.breaks[i].Avoid = true
	}
	b.result.Page.Operations = b.result.Page.Operations[:opStart]
	b.result.Boxes = b.result.Boxes[:boxStart]
	b.result.Lines = b.result.Lines[:lineStart]
	b.result.Breaks = b.result.Breaks[:breakStart]
	b.result.Tables = b.result.Tables[:tableStart]
	b.result.Atoms = b.result.Atoms[:atomStart]
	return u, err
}

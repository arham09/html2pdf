package layout

import (
	"fmt"
	"image"
	"math"
	"strconv"
	"strings"

	"github.com/arham09/html2pdf/internal/font"
	dom "github.com/arham09/html2pdf/internal/html"
	"github.com/arham09/html2pdf/internal/paint"
	"github.com/arham09/html2pdf/internal/resource"
	"github.com/arham09/html2pdf/internal/session"
	"github.com/arham09/html2pdf/internal/style"
)

type Line struct {
	Source   SourceID
	Bounds   Rect
	Baseline float64
	Text     string
}
type Result struct {
	Page   paint.Page
	Boxes  []Box
	Lines  []Line
	Breaks []BreakOpportunity
	Tables []Table
	Atoms  []Region
	Events []PageEvent
}
type builder struct {
	s           *session.Session
	fonts       *font.Registry
	loader      resource.Loader
	result      Result
	body        Rect
	rootSize    float64
	imageCache  map[uint64]string
	local       bool
	flow        bool
	listNumbers map[uint64]int64
	flexDepth   int
	gridDepth   int
}

// BuildFlow measures logical vertical flow; pagination checks physical pages.
func BuildFlow(s *session.Session, doc *dom.Document, fonts *font.Registry, loader resource.Loader, pageWidth, pageHeight float64, body Rect) (Result, error) {
	body.Height = math.MaxFloat64 / 4
	return build(s, doc, fonts, loader, pageWidth, pageHeight, body, true, false)
}

// BuildRegion lays out a fixed running decoration without charging a body page.
func BuildRegion(s *session.Session, doc *dom.Document, fonts *font.Registry, loader resource.Loader, pageWidth, pageHeight float64, region Rect) (Result, error) {
	return build(s, doc, fonts, loader, pageWidth, pageHeight, region, false, false)
}
func build(s *session.Session, doc *dom.Document, fonts *font.Registry, loader resource.Loader, pageWidth, pageHeight float64, body Rect, flow, countPage bool) (Result, error) {
	if countPage {
		if err := s.Charge("pages", 1); err != nil {
			return Result{}, err
		}
	}
	b := &builder{s: s, fonts: fonts, loader: loader, body: body, rootSize: 12, imageCache: map[uint64]string{}, result: Result{Page: paint.Page{WidthPt: pageWidth, HeightPt: pageHeight, Fonts: fonts.Faces, Images: map[string]image.Image{}}}}
	b.listNumbers = map[uint64]int64{}
	b.flow = flow
	var root *dom.Node
	for _, n := range doc.Nodes {
		if n.Tag == "html" {
			root = n
			b.rootSize = n.Style.FontSizePt
			break
		}
	}
	if root == nil {
		return Result{}, fmt.Errorf("%w: missing document root", session.ErrInvalidInput)
	}
	if _, err := b.block(root, body.X, body.Y, body.Width); err != nil {
		return Result{}, err
	}
	return b.result, nil
}

func (b *builder) emit(op paint.Operation) error {
	if err := b.s.Charge("display_ops", 1); err != nil {
		return err
	}
	if err := b.check(op.Bounds); err != nil {
		return err
	}
	b.result.Page.Operations = append(b.result.Page.Operations, op)
	return nil
}
func (b *builder) check(r Rect) error {
	if math.IsNaN(r.X+r.Y+r.Width+r.Height) || math.IsInf(r.X+r.Y+r.Width+r.Height, 0) || r.Width < 0 || r.Height < 0 {
		return fmt.Errorf("%w: invalid layout geometry", session.ErrInvalidInput)
	}
	const epsilon = 0.0001
	if r.X < b.body.X-epsilon || r.Y < b.body.Y-epsilon || r.X+r.Width > b.body.X+b.body.Width+epsilon || r.Y+r.Height > b.body.Y+b.body.Height+epsilon {
		return fmt.Errorf("%w: content does not fit its layout region", session.ErrUnbreakableContent)
	}
	return nil
}
func isBlock(n *dom.Node) bool {
	return n.Style.Display == style.Block || n.Style.Display == style.Flex || n.Style.Display == style.Grid || n.Style.Display == style.ListItem || n.Style.Display == style.Table || n.Style.Display == style.TableRow || n.Style.Display == style.TableCell || n.Style.Display == style.TableRowGroup || n.Style.Display == style.TableHeaderGroup
}
func (b *builder) validate(n *dom.Node) error {
	if err := b.s.Charge("layout_work", 1); err != nil {
		return err
	}
	if n.Tag != "img" && n.Style.Display != style.Flex && n.Style.Height.Unit != style.Auto {
		return fmt.Errorf("%w: fixed block heights are outside the print profile", session.ErrUnsupportedFeature)
	}
	if n.Style.MinWidth.Unit != style.Auto || n.Style.MinHeight.Unit != style.Auto {
		return fmt.Errorf("%w: minimum sizes require a flex/grid item", session.ErrUnsupportedFeature)
	}
	if n.Style.Display == style.TableRow || n.Style.Display == style.TableCell || n.Style.Display == style.TableRowGroup || n.Style.Display == style.TableHeaderGroup {
		return fmt.Errorf("%w: table parts require a table formatting context", session.ErrUnsupportedFeature)
	}
	if (n.Style.BreakBefore != "auto" || n.Style.BreakAfter != "auto") && (!b.flow || b.local || !isBlock(n)) {
		return fmt.Errorf("%w: page breaks require non-atomic body blocks", session.ErrUnsupportedFeature)
	}
	for _, v := range n.Style.Margin {
		if v.Value < 0 {
			return fmt.Errorf("%w: negative margins are outside the print profile", session.ErrUnsupportedFeature)
		}
	}
	return nil
}
func (b *builder) edges(st style.Computed, width float64) (margin, padding, border [4]float64) {
	for i := 0; i < 4; i++ {
		margin[i] = st.Margin[i].Resolve(width, st.FontSizePt, b.rootSize)
		padding[i] = st.Padding[i].Resolve(width, st.FontSizePt, b.rootSize)
		if st.Border[i].Style != "none" {
			border[i] = st.Border[i].Width.Resolve(width, st.FontSizePt, b.rootSize)
		}
	}
	return
}

func (b *builder) block(n *dom.Node, x, y, available float64) (float64, error) {
	return b.blockSized(n, x, y, available, -1)
}

// A nonnegative contentHeight is an internal flex/grid-item allocation. Ordinary
// fixed-height blocks remain outside the print profile.
func (b *builder) blockSized(n *dom.Node, x, y, available, contentHeight float64) (float64, error) {
	if n.Style.Display == style.None {
		return 0, nil
	}
	if err := b.validate(n); err != nil {
		return 0, err
	}
	margin, padding, border := b.edges(n.Style, available)
	if n.Style.Display == style.Table && n.Style.BorderCollapse == "collapse" {
		// The collapsed grid owns the table's border candidates and insets.
		border = [4]float64{}
	}
	width := available - margin[1] - margin[3] - padding[1] - padding[3] - border[1] - border[3]
	if n.Style.Width.Unit != style.Auto {
		width = n.Style.Width.Resolve(available, n.Style.FontSizePt, b.rootSize)
	}
	if width < 0 {
		return 0, fmt.Errorf("%w: negative content width", session.ErrUnbreakableContent)
	}
	spare := available - width - padding[1] - padding[3] - border[1] - border[3] - margin[1] - margin[3]
	if n.Style.Width.Unit != style.Auto && spare > 0 {
		left, right := n.Style.Margin[3].Unit == style.Auto, n.Style.Margin[1].Unit == style.Auto
		if left && right {
			margin[1] = spare / 2
			margin[3] = spare / 2
		} else if left {
			margin[3] = spare
		} else if right {
			margin[1] = spare
		}
	}
	outerX, outerY := x+margin[3], y+margin[0]
	contentX, contentY := outerX+border[3]+padding[3], outerY+border[0]+padding[0]
	outerWidth := width + padding[1] + padding[3] + border[1] + border[3]
	if err := b.check(Rect{X: outerX, Y: outerY, Width: outerWidth}); err != nil {
		return 0, err
	}
	// Insert a background placeholder before children so painting order is correct.
	backgroundIndex := -1
	if n.Style.Background.A != 0 {
		backgroundIndex = len(b.result.Page.Operations)
		if err := b.emit(paint.Operation{Kind: paint.FillRect, Source: SourceID(n.ID), Bounds: Rect{X: outerX, Y: outerY, Width: outerWidth}, Color: n.Style.Background}); err != nil {
			return 0, err
		}
	}
	if b.flow && !b.local && n.Style.BreakBefore != "auto" {
		b.result.Events = append(b.result.Events, PageEvent{Source: SourceID(n.ID), Y: y, Mode: n.Style.BreakBefore})
	}
	current := contentY
	contentStart, lineStart := len(b.result.Page.Operations), len(b.result.Lines)
	if n.Tag == "ol" {
		if _, ok := n.Attributes["reversed"]; ok {
			return 0, fmt.Errorf("%w: reversed lists", session.ErrUnsupportedFeature)
		}
		number := int64(1)
		if raw := n.Attributes["start"]; raw != "" {
			v, err := strconv.ParseInt(raw, 10, 32)
			if err != nil {
				return 0, fmt.Errorf("%w: ordered list start", session.ErrInvalidInput)
			}
			number = v
		}
		for _, child := range n.Children {
			if err := b.s.Charge("layout_work", 1); err != nil {
				return 0, err
			}
			if child.Tag != "li" || child.Style.Display == style.None {
				continue
			}
			if raw := child.Attributes["value"]; raw != "" {
				v, err := strconv.ParseInt(raw, 10, 32)
				if err != nil {
					return 0, fmt.Errorf("%w: list item value", session.ErrInvalidInput)
				}
				number = v
			}
			b.listNumbers[child.ID] = number
			number++
		}
	}
	if n.Style.Display == style.Flex {
		height, err := b.flex(n, contentX, current, width)
		if err != nil {
			return 0, err
		}
		current += height
	} else if n.Style.Display == style.Grid {
		height, err := b.grid(n, contentX, current, width)
		if err != nil {
			return 0, err
		}
		current += height
	} else if n.Style.Display == style.Table {
		height, err := b.table(n, contentX, current, width)
		if err != nil {
			return 0, err
		}
		current += height
	} else if n.Tag == "img" {
		atom, err := b.image(n, width)
		if err != nil {
			return 0, err
		}
		if err := b.placeAtomic(atom, contentX, current); err != nil {
			return 0, err
		}
		current += atom.height
	} else if n.Tag == "hr" {
		height := 0.75
		if err := b.emit(paint.Operation{Kind: paint.FillRect, Source: SourceID(n.ID), Bounds: Rect{X: contentX, Y: current, Width: width, Height: height}, Color: n.Style.Color}); err != nil {
			return 0, err
		}
		current += height
	} else {
		childX, childWidth := contentX, width
		if n.Tag == "ul" || n.Tag == "ol" {
			childX += 24
			childWidth -= 24
			if childWidth < 0 {
				return 0, fmt.Errorf("%w: list indent", session.ErrUnbreakableContent)
			}
		}
		var pending []*dom.Node
		flush := func() error {
			if len(pending) == 0 {
				return nil
			}
			height, err := b.inline(pending, n.Style, childX, current, childWidth)
			if err != nil {
				return err
			}
			current += height
			pending = nil
			return nil
		}
		for _, child := range n.Children {
			if child.Style.Display == style.None {
				continue
			}
			if isBlock(child) {
				if err := flush(); err != nil {
					return 0, err
				}
				height, err := b.block(child, childX, current, childWidth)
				if err != nil {
					return 0, err
				}
				current += height
			} else {
				pending = append(pending, child)
			}
		}
		if err := flush(); err != nil {
			return 0, err
		}
	}
	if n.Style.Display == style.ListItem {
		marker := "•"
		if n.Parent != nil && n.Parent.Tag == "ol" {
			marker = strconv.FormatInt(b.listNumbers[n.ID], 10) + "."
		}
		id, run, err := b.fonts.Shape(marker, n.Style.FontFamily, n.Style.FontWeight, n.Style.Italic, n.Style.FontSizePt)
		if err != nil {
			return 0, err
		}
		baseline := contentY + run.AscentPt
		if len(b.result.Lines) > lineStart {
			baseline = b.result.Lines[lineStart].Baseline
		}
		if err := b.glyph(id, run, n.Style.Color, SourceID(n.ID), contentX-run.AdvancePt-6, baseline); err != nil {
			return 0, err
		}
		// Emit markers before their item text in PDF logical reading order.
		ops := b.result.Page.Operations
		last := ops[len(ops)-1]
		copy(ops[contentStart+1:], ops[contentStart:len(ops)-1])
		ops[contentStart] = last
		if current == contentY {
			current += math.Max(n.Style.LineHeightPt, run.AscentPt+run.DescentPt)
		}
	}
	if contentHeight >= 0 {
		if current-contentY > contentHeight+0.0001 {
			return 0, fmt.Errorf("%w: item content exceeds its allocated height", session.ErrUnbreakableContent)
		}
		current = contentY + contentHeight
	}
	outerHeight := current - contentY + padding[0] + padding[2] + border[0] + border[2]
	rect := Rect{X: outerX, Y: outerY, Width: outerWidth, Height: outerHeight}
	if err := b.check(rect); err != nil {
		return 0, err
	}
	if backgroundIndex >= 0 {
		b.result.Page.Operations[backgroundIndex].Bounds = rect
	}
	for i, w := range border {
		if w == 0 || n.Style.Border[i].Color.A == 0 {
			continue
		}
		edge := rect
		switch i {
		case 0:
			edge.Height = w
		case 1:
			edge.X += rect.Width - w
			edge.Width = w
		case 2:
			edge.Y += rect.Height - w
			edge.Height = w
		case 3:
			edge.Width = w
		}
		if err := b.emit(paint.Operation{Kind: paint.FillRect, Source: SourceID(n.ID), Bounds: edge, Color: n.Style.Border[i].Color, Border: true}); err != nil {
			return 0, err
		}
	}
	b.result.Boxes = append(b.result.Boxes, Box{Source: SourceID(n.ID), Style: n.Style, Bounds: rect})
	if n.Style.Display == style.Flex && outerHeight > 0 {
		b.result.Atoms = append(b.result.Atoms, Region{Source: SourceID(n.ID), Bounds: rect})
	}
	b.result.Breaks = append(b.result.Breaks, BreakOpportunity{Source: SourceID(n.ID), Kind: BlockBoundary, Avoid: n.Style.BreakInside == "avoid"})
	used := margin[0] + outerHeight + margin[2]
	if b.flow && !b.local && n.Style.BreakAfter != "auto" {
		b.result.Events = append(b.result.Events, PageEvent{Source: SourceID(n.ID), Y: y + used, Mode: n.Style.BreakAfter})
	}
	return used, nil
}

func (b *builder) image(n *dom.Node, available float64) (unit, error) {
	if n.Attributes["src"] == "" {
		return unit{}, fmt.Errorf("%w: image src is required", session.ErrMissingResource)
	}
	if inlineDecoration(n.Style) {
		return unit{}, fmt.Errorf("%w: decorate a block wrapper around images", session.ErrUnsupportedFeature)
	}
	id := b.imageCache[n.ID]
	if id == "" {
		data, err := b.loader.Load(resource.Image, n.Attributes["src"])
		if err != nil {
			return unit{}, err
		}
		img, err := b.loader.DecodeImage(data)
		if err != nil {
			return unit{}, err
		}
		id = fmt.Sprintf("image-%d", n.ID)
		b.result.Page.Images[id] = img
		b.imageCache[n.ID] = id
	}
	img := b.result.Page.Images[id]
	w, h := float64(img.Bounds().Dx())*0.75, float64(img.Bounds().Dy())*0.75
	explicitW, explicitH := false, false
	if raw := n.Attributes["width"]; raw != "" {
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil || !finiteNumber(v) || v <= 0 {
			return unit{}, fmt.Errorf("%w: image width", session.ErrInvalidInput)
		}
		w = v * 0.75
		explicitW = true
	}
	if raw := n.Attributes["height"]; raw != "" {
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil || !finiteNumber(v) || v <= 0 {
			return unit{}, fmt.Errorf("%w: image height", session.ErrInvalidInput)
		}
		h = v * 0.75
		explicitH = true
	}
	if n.Style.Width.Unit != style.Auto {
		w = n.Style.Width.Resolve(available, n.Style.FontSizePt, b.rootSize)
		explicitW = true
	}
	if n.Style.Height.Unit != style.Auto {
		if n.Style.Height.Unit == style.Percent {
			return unit{}, fmt.Errorf("%w: percentage image height", session.ErrUnsupportedFeature)
		}
		h = n.Style.Height.Resolve(0, n.Style.FontSizePt, b.rootSize)
		explicitH = true
	}
	if explicitW && !explicitH {
		h = w * float64(img.Bounds().Dy()) / float64(img.Bounds().Dx())
	}
	if explicitH && !explicitW {
		w = h * float64(img.Bounds().Dx()) / float64(img.Bounds().Dy())
	}
	if w < 0 || h < 0 || !finiteNumber(w+h) {
		return unit{}, fmt.Errorf("%w: image size", session.ErrInvalidInput)
	}
	// Image box decoration is not silently discarded: use a decorated block wrapper.
	for i := 0; i < 4; i++ {
		if n.Style.Padding[i].Value != 0 || n.Style.Border[i].Style != "none" || n.Style.Margin[i].Value != 0 {
			return unit{}, fmt.Errorf("%w: image edges require a block wrapper", session.ErrUnsupportedFeature)
		}
	}
	return unit{source: SourceID(n.ID), asset: id, width: w, height: h, atomic: true}, nil
}
func finiteNumber(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func (b *builder) placeAtomic(u unit, x, y float64) error {
	if u.height > 0 {
		b.result.Atoms = append(b.result.Atoms, Region{Source: u.source, Bounds: Rect{X: x, Y: y, Width: u.width, Height: u.height}})
	}
	for _, region := range u.atoms {
		region.Bounds.X += x
		region.Bounds.Y += y
		b.result.Atoms = append(b.result.Atoms, region)
	}
	if u.asset != "" {
		return b.emit(paint.Operation{Kind: paint.DrawImage, Source: u.source, Bounds: Rect{X: x, Y: y, Width: u.width, Height: u.height}, AssetID: u.asset})
	}
	for _, op := range u.ops {
		op.Bounds.X += x
		op.Bounds.Y += y
		var err error
		if op.Kind == paint.DrawGlyphRun {
			err = b.glyph(op.FontID, op.Glyphs, op.Color, op.Source, op.Bounds.X, op.Bounds.Y)
		} else {
			err = b.emit(op)
		}
		if err != nil {
			return err
		}
	}
	for _, box := range u.boxes {
		box.Bounds.X += x
		box.Bounds.Y += y
		b.result.Boxes = append(b.result.Boxes, box)
	}
	for _, line := range u.lines {
		line.Bounds.X += x
		line.Bounds.Y += y
		line.Baseline += y
		b.result.Lines = append(b.result.Lines, line)
	}
	for _, br := range u.breaks {
		if br.Kind == LineBoundary {
			br.Offset += len(b.result.Lines) - len(u.lines)
		}
		b.result.Breaks = append(b.result.Breaks, br)
	}
	for _, table := range u.tables {
		table.translate(x, y)
		b.result.Tables = append(b.result.Tables, table)
	}
	return nil
}

func inlineDecoration(st style.Computed) bool {
	if st.Background.A != 0 {
		return true
	}
	for i := 0; i < 4; i++ {
		if st.Margin[i].Value != 0 || st.Padding[i].Value != 0 || st.Border[i].Style != "none" {
			return true
		}
	}
	return false
}
func unsupportedInline(tag string) error {
	return fmt.Errorf("%w: unsupported inline %s; use a block/inline-block wrapper", session.ErrUnsupportedFeature, strings.TrimSpace(tag))
}

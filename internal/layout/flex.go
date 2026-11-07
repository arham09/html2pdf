package layout

import (
	"fmt"
	"math"

	dom "github.com/arham09/html2pdf/internal/html"
	"github.com/arham09/html2pdf/internal/session"
	"github.com/arham09/html2pdf/internal/style"
)

const flexEpsilon = 0.0001

type flexItem struct {
	node                     dom.Node
	original                 style.Computed
	base, minimum, size      float64
	mainExtra, crossExtra    float64
	width, height, minHeight float64
	fragment                 unit
	align                    string
}

func flexError(message string) error {
	return fmt.Errorf("%w: %s", session.ErrUnsupportedFeature, message)
}

// flex keeps source order and uses the existing block/text/table fragments.
// The enclosing block records the complete container as a hard page interval.
func (b *builder) flex(n *dom.Node, x, y, width float64) (float64, error) {
	if b.flexDepth != 0 || b.gridDepth != 0 {
		return 0, flexError("nested flex containers")
	}
	b.flexDepth++
	defer func() { b.flexDepth-- }()
	column := n.Style.FlexDirection == "column"
	if column && n.Style.FlexWrap != "nowrap" {
		return 0, flexError("column wrapping")
	}
	height := -1.0
	if n.Style.Height.Unit != style.Auto {
		if n.Style.Height.Unit == style.Percent {
			return 0, flexError("percentage flex-container height")
		}
		height = n.Style.Height.Resolve(0, n.Style.FontSizePt, b.rootSize)
	}
	rowGap := n.Style.RowGap.Resolve(0, n.Style.FontSizePt, b.rootSize)
	columnGap := n.Style.ColumnGap.Resolve(0, n.Style.FontSizePt, b.rootSize)
	main, gap := width, columnGap
	if column {
		main, gap = height, rowGap
	}
	var items []flexItem
	for _, child := range n.Children {
		if err := b.s.Charge("layout_work", 1); err != nil {
			return 0, err
		}
		if child.Style.Display == style.None || whitespaceNode(child) {
			continue
		}
		item, err := b.flexItem(child, n.Style, width, height, column)
		if err != nil {
			return 0, err
		}
		items = append(items, item)
	}
	if len(items) == 0 {
		return math.Max(0, height), nil
	}
	if column {
		if main < 0 {
			main = gap * float64(len(items)-1)
			for _, item := range items {
				main += math.Max(item.base, item.minimum) + item.mainExtra
			}
		}
		if err := b.flexSizes(items, main, gap); err != nil {
			return 0, err
		}
		used := gap * float64(len(items)-1)
		for i := range items {
			item := &items[i]
			item.height = item.size
			if err := b.measureFlexItem(item); err != nil {
				return 0, err
			}
			used += item.size + item.mainExtra
		}
		cursor, between := flexSpacing(n.Style.JustifyContent, math.Max(0, main-used), len(items))
		for _, item := range items {
			offset := flexOffset(item.align, width-item.fragment.width)
			if err := b.placeAtomic(item.fragment, x+offset, y+cursor); err != nil {
				return 0, err
			}
			cursor += item.fragment.height + gap + between
		}
		return main, nil
	}

	// Collect row lines using hypothetical outer sizes, before flexing each line.
	var lines [][2]int
	start, used := 0, 0.0
	for i := range items {
		if err := b.s.Charge("layout_work", 1); err != nil {
			return 0, err
		}
		size := math.Max(items[i].base, items[i].minimum) + items[i].mainExtra
		if n.Style.FlexWrap == "wrap" && i > start && used+gap+size > width+flexEpsilon {
			lines = append(lines, [2]int{start, i})
			start, used = i, 0
		}
		if i > start {
			used += gap
		}
		used += size
	}
	lines = append(lines, [2]int{start, len(items)})
	crosses := make([]float64, len(lines))
	totalCross := rowGap * float64(len(lines)-1)
	for i, span := range lines {
		line := items[span[0]:span[1]]
		if err := b.flexSizes(line, width, gap); err != nil {
			return 0, err
		}
		for j := range line {
			item := &line[j]
			item.width = item.size
			if err := b.measureFlexItem(item); err != nil {
				return 0, err
			}
			crosses[i] = math.Max(crosses[i], item.fragment.height)
		}
		totalCross += crosses[i]
	}
	if height < 0 {
		height = totalCross
	} else if totalCross > height+flexEpsilon {
		return 0, fmt.Errorf("%w: flex lines exceed container height", session.ErrUnbreakableContent)
	}
	lineCursor, lineBetween := 0.0, 0.0
	if n.Style.FlexWrap == "nowrap" {
		crosses[0] = height
	} else if n.Style.AlignContent == "stretch" {
		for i := range crosses {
			crosses[i] += math.Max(0, height-totalCross) / float64(len(crosses))
		}
	} else {
		lineCursor, lineBetween = flexSpacing(n.Style.AlignContent, math.Max(0, height-totalCross), len(lines))
	}
	for i, span := range lines {
		line := items[span[0]:span[1]]
		used := gap * float64(len(line)-1)
		for j := range line {
			item := &line[j]
			if item.align == "stretch" && item.original.Height.Unit == style.Auto {
				item.height = crosses[i] - item.crossExtra
				if err := b.measureFlexItem(item); err != nil {
					return 0, err
				}
			}
			used += item.size + item.mainExtra
		}
		cursor, between := flexSpacing(n.Style.JustifyContent, math.Max(0, width-used), len(line))
		for _, item := range line {
			offset := flexOffset(item.align, crosses[i]-item.fragment.height)
			if err := b.placeAtomic(item.fragment, x+cursor, y+lineCursor+offset); err != nil {
				return 0, err
			}
			cursor += item.fragment.width + gap + between
		}
		lineCursor += crosses[i] + rowGap + lineBetween
	}
	return height, nil
}

func (b *builder) flexItem(n *dom.Node, container style.Computed, width, height float64, column bool) (flexItem, error) {
	item := flexItem{node: *n, original: n.Style, height: -1, align: n.Style.AlignSelf}
	if item.align == "auto" {
		item.align = container.AlignItems
	}
	var tables bool
	var inspect func(*dom.Node) error
	inspect = func(child *dom.Node) error {
		if err := b.s.Charge("layout_work", 1); err != nil {
			return err
		}
		if child.Style.Display == style.None {
			return nil
		}
		if child.Style.Display == style.Flex || child.Style.Display == style.Grid {
			return flexError("nested Flex/Grid containers")
		}
		tables = tables || child.Style.Display == style.Table
		for _, c := range child.Children {
			if err := inspect(c); err != nil {
				return err
			}
		}
		return nil
	}
	if err := inspect(n); err != nil {
		return item, err
	}
	if n.Tag == "img" || n.Tag == "hr" || n.Tag == "br" || n.Style.Display == style.Table || n.Style.Display == style.ListItem || n.Style.Display >= style.TableRowGroup && n.Style.Display <= style.TableCell {
		return item, flexError("wrap replaced elements, lists and tables in block flex items")
	}
	for _, margin := range n.Style.Margin {
		if margin.Unit == style.Auto {
			return item, flexError("auto flex-item margins")
		}
	}
	m, p, border := b.edges(n.Style, width)
	horizontal := m[1] + m[3] + p[1] + p[3] + border[1] + border[3]
	vertical := m[0] + m[2] + p[0] + p[2] + border[0] + border[2]
	item.mainExtra, item.crossExtra = horizontal, vertical
	if column {
		item.mainExtra, item.crossExtra = vertical, horizontal
	}
	// Resolve item percentage edges against the containing flex width once.
	// Local block measurement must not reinterpret them against its own width.
	for i := range m {
		item.node.Style.Margin[i] = style.Length{Value: m[i]}
		item.node.Style.Padding[i] = style.Length{Value: p[i]}
		item.node.Style.Border[i].Width = style.Length{Value: border[i]}
	}
	item.node.Style.Display = style.Block
	item.node.Style.Height = style.Length{Unit: style.Auto}
	item.node.Style.MinWidth = style.Length{Unit: style.Auto}
	item.node.Style.MinHeight = style.Length{Unit: style.Auto}
	if n.Tag == "" {
		// Anonymous text items keep the original text node and source ID.
		item.node.Text = ""
		item.node.Children = []*dom.Node{n}
	}
	basis := n.Style.FlexBasis
	if basis.Unit == style.Auto {
		if column {
			basis = n.Style.Height
		} else {
			basis = n.Style.Width
		}
	}
	needIntrinsic := n.Style.MinWidth.Unit == style.Auto || !column && basis.Unit == style.Auto || column && n.Style.Width.Unit == style.Auto && item.align != "stretch"
	lo, hi := 0.0, 0.0
	if needIntrinsic {
		if tables {
			return item, flexError("tables in flex items require definite width/basis and explicit min-width")
		}
		var err error
		lo, hi, err = b.intrinsic(item.node.Children, width)
		if err != nil {
			return item, err
		}
	}
	minWidth := lo
	if n.Style.MinWidth.Unit != style.Auto {
		minWidth = n.Style.MinWidth.Resolve(width, n.Style.FontSizePt, b.rootSize)
	} else if n.Style.Width.Unit != style.Auto {
		minWidth = math.Min(minWidth, n.Style.Width.Resolve(width, n.Style.FontSizePt, b.rootSize))
	}
	var err error
	item.minHeight, err = b.flexLength(n.Style.MinHeight, height, height >= 0, n.Style.FontSizePt, 0)
	if err != nil {
		return item, err
	}
	item.height, err = b.flexLength(n.Style.Height, height, height >= 0, n.Style.FontSizePt, -1)
	if err != nil {
		return item, err
	}
	if !column {
		item.base, err = b.flexLength(basis, width, true, n.Style.FontSizePt, hi)
		item.minimum = minWidth
		return item, err
	}
	item.width = width - horizontal
	if n.Style.Width.Unit != style.Auto {
		item.width = n.Style.Width.Resolve(width, n.Style.FontSizePt, b.rootSize)
	} else if item.align != "stretch" {
		item.width = math.Min(hi, item.width)
	}
	item.width = math.Max(item.width, minWidth)
	if item.width < 0 || item.width+horizontal > width+flexEpsilon {
		return item, fmt.Errorf("%w: flex-item cross width", session.ErrUnbreakableContent)
	}
	explicitHeight := item.height
	item.height = -1
	if err := b.measureFlexItem(&item); err != nil {
		return item, err
	}
	natural := item.fragment.height - vertical
	item.height = explicitHeight
	item.base, err = b.flexLength(basis, height, height >= 0, n.Style.FontSizePt, natural)
	if err != nil {
		return item, err
	}
	item.minimum = natural
	if n.Style.MinHeight.Unit != style.Auto {
		item.minimum = item.minHeight
	} else if n.Style.Height.Unit != style.Auto {
		item.minimum = math.Min(natural, item.height)
	}
	return item, nil
}

func (b *builder) flexLength(l style.Length, main float64, definite bool, fontSize, auto float64) (float64, error) {
	if l.Unit == style.Auto {
		return auto, nil
	}
	if l.Unit == style.Percent && !definite {
		return 0, flexError("indefinite percentage flex size")
	}
	return l.Resolve(main, fontSize, b.rootSize), nil
}

func (b *builder) measureFlexItem(item *flexItem) error {
	item.node.Style.Width = style.Length{Value: item.width}
	contentHeight := item.height
	if contentHeight >= 0 {
		contentHeight = math.Max(contentHeight, item.minHeight)
	}
	m, p, border := b.edges(item.node.Style, 0)
	horizontal := m[1] + m[3] + p[1] + p[3] + border[1] + border[3]
	u, err := b.localBlockSized(&item.node, item.width+horizontal, contentHeight)
	if err != nil {
		return err
	}
	if contentHeight < 0 && item.minHeight > 0 {
		vertical := m[0] + m[2] + p[0] + p[2] + border[0] + border[2]
		if u.height-vertical < item.minHeight {
			u, err = b.localBlockSized(&item.node, item.width+horizontal, item.minHeight)
		}
	}
	item.fragment = u
	return err
}

// flexSizes implements bounded grow/scaled-shrink distribution with minimum
// freezing. Each unsuccessful pass freezes at least one item; all work is charged.
func (b *builder) flexSizes(items []flexItem, main, gap float64) error {
	space := main - gap*float64(len(items)-1)
	hypothetical := 0.0
	for _, item := range items {
		space -= item.mainExtra
		hypothetical += math.Max(item.base, item.minimum)
	}
	grow := hypothetical < space
	frozen := make([]bool, len(items))
	factor := func(item flexItem) float64 {
		if grow {
			return item.original.FlexGrow
		}
		return item.original.FlexShrink
	}
	initial := space
	for i := range items {
		item := &items[i]
		item.size = item.base
		if factor(*item) == 0 || !grow && item.base < item.minimum {
			item.size = math.Max(item.base, item.minimum)
			frozen[i] = true
		}
		initial -= item.size
	}
	for pass := 0; pass <= len(items); pass++ {
		if err := b.s.Charge("layout_work", int64(len(items))); err != nil {
			return err
		}
		remaining, factors, weights := space, 0.0, 0.0
		for i, item := range items {
			if frozen[i] {
				remaining -= item.size
			} else {
				remaining -= item.base
				factors += factor(item)
				weight := factor(item)
				if !grow {
					weight *= item.base
				}
				weights += weight
			}
		}
		if factors < 1 && math.Abs(initial*factors) < math.Abs(remaining) {
			remaining = initial * factors
		}
		clamped := false
		for i := range items {
			if frozen[i] {
				continue
			}
			item := &items[i]
			weight := factor(*item)
			if !grow {
				weight *= item.base
			}
			item.size = item.base
			if weights > 0 {
				item.size += remaining * weight / weights
			}
			if item.size < item.minimum {
				item.size, frozen[i], clamped = item.minimum, true, true
			}
		}
		if !clamped {
			used := 0.0
			for _, item := range items {
				if !finiteNumber(item.size) || item.size < 0 {
					return fmt.Errorf("%w: invalid flex size", session.ErrInvalidInput)
				}
				used += item.size
			}
			if used > space+flexEpsilon {
				return fmt.Errorf("%w: flex minimum sizes exceed available main size", session.ErrUnbreakableContent)
			}
			return nil
		}
	}
	return fmt.Errorf("%w: flex sizing did not converge", session.ErrInvalidInput)
}

func flexSpacing(mode string, spare float64, count int) (offset, between float64) {
	switch mode {
	case "flex-end":
		offset = spare
	case "center":
		offset = spare / 2
	case "space-between":
		if count > 1 {
			between = spare / float64(count-1)
		}
	case "space-around":
		between = spare / float64(count)
		offset = between / 2
	case "space-evenly":
		between = spare / float64(count+1)
		offset = between
	}
	return
}
func flexOffset(mode string, spare float64) float64 {
	offset, _ := flexSpacing(mode, math.Max(0, spare), 1)
	return offset
}

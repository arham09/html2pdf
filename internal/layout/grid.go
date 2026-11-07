package layout

import (
	"fmt"
	"math"
	"sort"

	dom "github.com/arham09/html2pdf/internal/html"
	"github.com/arham09/html2pdf/internal/session"
	"github.com/arham09/html2pdf/internal/style"
)

type gridItem struct {
	node                        *dom.Node
	row, column, rows, columns  int
	fragment                    unit
	horizontal, vertical        float64
	contentWidth, contentHeight float64
	areaWidth                   float64
	align, justify              string
}

func gridError(message string) error {
	return fmt.Errorf("%w: Grid: %s", session.ErrUnsupportedFeature, message)
}
func gridOverflow(message string) error {
	return fmt.Errorf("%w: Grid: %s", session.ErrUnbreakableContent, message)
}

func (b *builder) grid(n *dom.Node, x, y, width float64) (float64, error) {
	if b.gridDepth != 0 || b.flexDepth != 0 {
		return 0, gridError("nested Flex/Grid contexts")
	}
	if n.Style.GridColumns == nil {
		return 0, gridError("explicit grid-template-columns required")
	}
	b.gridDepth++
	defer func() { b.gridDepth-- }()
	gap := n.Style.ColumnGap.Resolve(0, n.Style.FontSizePt, b.rootSize)
	rowGap := n.Style.RowGap.Resolve(0, n.Style.FontSizePt, b.rootSize)
	columns, err := b.gridColumns(n.Style.GridColumns.Columns, width, gap, n.Style.FontSizePt)
	if err != nil {
		return 0, err
	}
	used := gap * float64(len(columns)-1)
	for _, w := range columns {
		used += w
	}
	start, between := flexSpacing(n.Style.JustifyContent, math.Max(0, width-used), len(columns))
	gap += between
	xs := make([]float64, len(columns)+1)
	xs[0] = start
	for i, w := range columns {
		xs[i+1] = xs[i] + w + gap
	}
	var items []gridItem
	for _, child := range n.Children {
		if err := b.s.Charge("layout_work", 1); err != nil {
			return 0, err
		}
		if child.Style.Display == style.None || whitespaceNode(child) {
			continue
		}
		items = append(items, gridItem{node: child, row: -1, column: -1, rows: child.Style.GridRow.Span, columns: child.Style.GridColumn.Span})
	}
	rowCount, err := b.gridPlace(items, len(columns))
	if err != nil {
		return 0, err
	}
	if rowCount == 0 {
		return 0, nil
	}
	if err := b.s.Charge("layout_work", int64(rowCount)); err != nil {
		return 0, err
	}
	heights := make([]float64, rowCount)
	for i := range items {
		item := &items[i]
		areaWidth := xs[item.column+item.columns] - xs[item.column] - gap
		if err := b.measureGridItem(item, n.Style, areaWidth); err != nil {
			return 0, err
		}
		if item.rows == 1 {
			heights[item.row] = math.Max(heights[item.row], item.fragment.height)
		}
	}
	// Longer spans distribute only their unmet height, after shorter spans. This
	// bounded print rule is deterministic; it is not full browser track sizing.
	work := int64(len(items))
	for count := len(items); count > 1; count = (count + 1) / 2 {
		work += int64(len(items))
	}
	if err := b.s.Charge("layout_work", work); err != nil {
		return 0, err
	}
	order := make([]int, len(items))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool { return items[order[i]].rows < items[order[j]].rows })
	for _, index := range order {
		item := items[index]
		if item.rows == 1 {
			continue
		}
		if err := b.s.Charge("layout_work", int64(item.rows)); err != nil {
			return 0, err
		}
		available := rowGap * float64(item.rows-1)
		for _, h := range heights[item.row : item.row+item.rows] {
			available += h
		}
		if deficit := item.fragment.height - available; deficit > 0 {
			for r := item.row; r < item.row+item.rows; r++ {
				heights[r] += deficit / float64(item.rows)
			}
		}
	}
	ys := make([]float64, rowCount+1)
	for r, h := range heights {
		ys[r+1] = ys[r] + h + rowGap
	}
	for i := range items {
		item := &items[i]
		areaWidth := xs[item.column+item.columns] - xs[item.column] - gap
		areaHeight := ys[item.row+item.rows] - ys[item.row] - rowGap
		if item.align == "stretch" && item.node.Style.Height.Unit == style.Auto {
			if err := b.stretchGridItem(item, areaHeight-item.vertical); err != nil {
				return 0, err
			}
		}
		dx := flexOffset(item.justify, areaWidth-item.fragment.width)
		dy := flexOffset(item.align, areaHeight-item.fragment.height)
		if err := b.placeAtomic(item.fragment, x+xs[item.column]+dx, y+ys[item.row]+dy); err != nil {
			return 0, err
		}
	}
	// Span-connected rows form one hard interval even when the spanning item is
	// short, transparent or explicitly aligned. Merely protecting its ink fails.
	ends := make([]int, rowCount)
	for r := range ends {
		ends[r] = r + 1
	}
	for _, item := range items {
		ends[item.row] = max(ends[item.row], item.row+item.rows)
	}
	for first := 0; first < rowCount; {
		last := ends[first]
		for r := first; r < last; r++ {
			if err := b.s.Charge("layout_work", 1); err != nil {
				return 0, err
			}
			last = max(last, ends[r])
		}
		h := ys[last] - ys[first] - rowGap
		if h > 0 {
			b.result.Atoms = append(b.result.Atoms, Region{Source: SourceID(n.ID), Bounds: Rect{X: x, Y: y + ys[first], Width: width, Height: h}})
		}
		first = last
	}
	height := ys[rowCount] - rowGap
	// Empty rows can still consume gap space. A zero-height extent marker lets
	// pagination retain that space without making the gaps indivisible.
	b.result.Atoms = append(b.result.Atoms, Region{Source: SourceID(n.ID), Bounds: Rect{X: x, Y: y + height}})
	return height, nil
}

func (b *builder) gridColumns(tracks []style.Length, width, gap, fontSize float64) ([]float64, error) {
	if len(tracks) < 1 || len(tracks) > style.MaxGridColumns {
		return nil, gridError("column count outside profile")
	}
	if err := b.s.Charge("layout_work", int64(len(tracks))); err != nil {
		return nil, err
	}
	out := make([]float64, len(tracks))
	remaining, fractions := width-gap*float64(len(tracks)-1), 0.0
	for i, t := range tracks {
		if t.Unit == style.Fraction {
			fractions += t.Value
			continue
		}
		out[i] = t.Resolve(width, fontSize, b.rootSize)
		remaining -= out[i]
	}
	if remaining < -flexEpsilon {
		return nil, gridOverflow("fixed/percentage tracks and gaps exceed width")
	}
	for i, t := range tracks {
		if t.Unit == style.Fraction {
			out[i] = math.Max(0, remaining) * t.Value / math.Max(1, fractions)
		}
		if !finiteNumber(out[i]) || out[i] < 0 {
			return nil, gridOverflow("invalid column width")
		}
	}
	return out, nil
}

// Fully definite items reserve their cells first, followed by row-locked items.
// Remaining items use sparse row-major placement; implicit columns are forbidden.
func (b *builder) gridPlace(items []gridItem, columns int) (int, error) {
	occupied := map[int][]bool{}
	rows := 0
	fits := func(row, col, h, w int) (bool, error) {
		if row < 0 || col < 0 || row+h > style.MaxGridRows || col+w > columns {
			return false, nil
		}
		for r := row; r < row+h; r++ {
			for c := col; c < col+w; c++ {
				if err := b.s.Charge("layout_work", 1); err != nil {
					return false, err
				}
				if cells := occupied[r]; cells != nil && cells[c] {
					return false, nil
				}
			}
		}
		return true, nil
	}
	reserve := func(item *gridItem, row, col int) error {
		for r := row; r < row+item.rows; r++ {
			if occupied[r] == nil {
				if err := b.s.Charge("layout_work", int64(columns)); err != nil {
					return err
				}
				occupied[r] = make([]bool, columns)
			}
			if err := b.s.Charge("layout_work", int64(item.columns)); err != nil {
				return err
			}
			for c := col; c < col+item.columns; c++ {
				occupied[r][c] = true
			}
		}
		item.row, item.column = row, col
		rows = max(rows, row+item.rows)
		return nil
	}
	for i := range items {
		item := &items[i]
		if item.rows < 1 || item.columns < 1 || item.columns > columns || item.rows > style.MaxGridRows {
			return 0, gridError("span exceeds defined tracks")
		}
		row, col := item.node.Style.GridRow.Start-1, item.node.Style.GridColumn.Start-1
		if col >= 0 && col+item.columns > columns {
			return 0, gridError("placement would create implicit columns")
		}
		if row < 0 || col < 0 {
			continue
		}
		ok, err := fits(row, col, item.rows, item.columns)
		if err != nil {
			return 0, err
		}
		if !ok {
			return 0, gridError("explicit placement collision or extent outside profile")
		}
		if err := reserve(item, row, col); err != nil {
			return 0, err
		}
	}
	for i := range items {
		item := &items[i]
		row := item.node.Style.GridRow.Start - 1
		if item.row >= 0 || row < 0 {
			continue
		}
		placed := false
		for col := 0; col+item.columns <= columns; col++ {
			ok, err := fits(row, col, item.rows, item.columns)
			if err != nil {
				return 0, err
			}
			if ok {
				if err := reserve(item, row, col); err != nil {
					return 0, err
				}
				placed = true
				break
			}
		}
		if !placed {
			return 0, gridError("row-locked item has no free explicit columns")
		}
	}
	cursorRow, cursorColumn := 0, 0
	for i := range items {
		item := &items[i]
		if item.row >= 0 {
			continue
		}
		col := item.node.Style.GridColumn.Start - 1
		placed := false
		if col >= 0 && col < cursorColumn {
			cursorRow++
		}
		for row := cursorRow; row+item.rows <= style.MaxGridRows; row++ {
			first, last := 0, columns-item.columns
			if row == cursorRow {
				first = cursorColumn
			}
			if col >= 0 {
				first, last = col, col
			}
			for c := first; c <= last; c++ {
				ok, err := fits(row, c, item.rows, item.columns)
				if err != nil {
					return 0, err
				}
				if ok {
					if err := reserve(item, row, c); err != nil {
						return 0, err
					}
					cursorRow, cursorColumn = row, c+item.columns
					placed = true
					break
				}
			}
			if placed {
				break
			}
			// Charge a row transition even when no candidate columns fit.
			if err := b.s.Charge("layout_work", 1); err != nil {
				return 0, err
			}
		}
		if !placed {
			return 0, gridError("content rows exceed bounded profile")
		}
	}
	return rows, nil
}

func (b *builder) measureGridItem(item *gridItem, container style.Computed, areaWidth float64) error {
	item.areaWidth = areaWidth
	n := item.node
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
			return gridError("nested Flex/Grid contexts")
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
		return err
	}
	if n.Tag == "img" || n.Tag == "hr" || n.Tag == "br" || n.Style.Display == style.Table || n.Style.Display == style.ListItem || n.Style.Display >= style.TableRowGroup && n.Style.Display <= style.TableCell {
		return gridError("wrap replaced elements, lists and tables in block items")
	}
	for _, m := range n.Style.Margin {
		if m.Unit == style.Auto {
			return gridError("auto item margins")
		}
	}
	if n.Style.Height.Unit == style.Percent || n.Style.MinHeight.Unit == style.Percent {
		return gridError("percentage item height in content-sized rows")
	}
	item.align, item.justify = n.Style.AlignSelf, n.Style.JustifySelf
	if item.align == "auto" {
		item.align = container.AlignItems
	}
	if item.justify == "auto" {
		item.justify = container.JustifyItems
	}
	m, p, border := b.edges(n.Style, areaWidth)
	item.horizontal = m[1] + m[3] + p[1] + p[3] + border[1] + border[3]
	item.vertical = m[0] + m[2] + p[0] + p[2] + border[0] + border[2]
	available := areaWidth - item.horizontal
	item.contentWidth = available
	if n.Style.Width.Unit != style.Auto {
		item.contentWidth = n.Style.Width.Resolve(areaWidth, n.Style.FontSizePt, b.rootSize)
	} else if item.justify != "stretch" {
		if tables {
			return gridError("non-stretched table item requires definite width")
		}
		children := n.Children
		if n.Tag == "" {
			children = []*dom.Node{n}
		}
		lo, hi, err := b.intrinsic(children, areaWidth)
		if err != nil {
			return err
		}
		item.contentWidth = math.Max(lo, math.Min(hi, available))
	}
	if n.Style.MinWidth.Unit != style.Auto {
		item.contentWidth = math.Max(item.contentWidth, n.Style.MinWidth.Resolve(areaWidth, n.Style.FontSizePt, b.rootSize))
	}
	if item.contentWidth < 0 || item.contentWidth > available+flexEpsilon {
		return gridOverflow("item width exceeds its assigned area")
	}
	item.contentHeight = -1
	if n.Style.Height.Unit != style.Auto {
		item.contentHeight = n.Style.Height.Resolve(0, n.Style.FontSizePt, b.rootSize)
	}
	if n.Style.MinHeight.Unit != style.Auto && item.contentHeight >= 0 {
		item.contentHeight = math.Max(item.contentHeight, n.Style.MinHeight.Resolve(0, n.Style.FontSizePt, b.rootSize))
	}
	clone := b.gridItemClone(item)
	u, err := b.localBlockSized(&clone, item.contentWidth+item.horizontal, item.contentHeight)
	if err != nil {
		return err
	}
	if n.Style.MinHeight.Unit != style.Auto && item.contentHeight < 0 {
		minimum := n.Style.MinHeight.Resolve(0, n.Style.FontSizePt, b.rootSize)
		if u.height-item.vertical < minimum {
			u, err = b.localBlockSized(&clone, item.contentWidth+item.horizontal, minimum)
		}
	}
	item.fragment = u
	return err
}

func (b *builder) gridItemClone(item *gridItem) dom.Node {
	clone := *item.node
	m, p, border := b.edges(clone.Style, item.areaWidth)
	// Edges are always percentages of the grid area, not the shrink-to-fit item.
	for i := range m {
		clone.Style.Margin[i] = style.Length{Value: m[i]}
		clone.Style.Padding[i] = style.Length{Value: p[i]}
		clone.Style.Border[i].Width = style.Length{Value: border[i]}
	}
	clone.Style.Display = style.Block
	clone.Style.Width = style.Length{Value: item.contentWidth}
	clone.Style.Height = style.Length{Unit: style.Auto}
	clone.Style.MinWidth = style.Length{Unit: style.Auto}
	clone.Style.MinHeight = style.Length{Unit: style.Auto}
	if clone.Tag == "" {
		clone.Text = ""
		clone.Children = []*dom.Node{item.node}
	}
	return clone
}
func (b *builder) stretchGridItem(item *gridItem, height float64) error {
	clone := b.gridItemClone(item)
	u, err := b.localBlockSized(&clone, item.contentWidth+item.horizontal, height)
	item.fragment = u
	return err
}

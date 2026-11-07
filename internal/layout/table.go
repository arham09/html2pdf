package layout

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	dom "github.com/arham09/html2pdf/internal/html"
	"github.com/arham09/html2pdf/internal/paint"
	"github.com/arham09/html2pdf/internal/session"
	"github.com/arham09/html2pdf/internal/style"
)

// Table retains the single column model and source identities for fragmentation.
// Bounds and cells include border insets; column widths sum to Bounds.Width.
type Table struct {
	Source  SourceID
	Bounds  Rect
	Columns []float64
	Rows    []TableRow
}
type TableRow struct {
	Source, Group SourceID
	Header        bool
	Bounds        Rect
	Cells         []TableCell
}
type TableCell struct {
	Source                SourceID
	Column, Colspan       int
	Bounds, ContentBounds Rect
}

func (t *Table) translate(x, y float64) {
	t.Bounds.X += x
	t.Bounds.Y += y
	for i := range t.Rows {
		r := &t.Rows[i]
		r.Bounds.X += x
		r.Bounds.Y += y
		for j := range r.Cells {
			c := &r.Cells[j]
			c.Bounds.X += x
			c.Bounds.Y += y
			c.ContentBounds.X += x
			c.ContentBounds.Y += y
		}
	}
}

type tableCell struct {
	node           *dom.Node
	column, span   int
	padding, inset [4]float64
	fragment       unit
}
type tableRow struct {
	node, group *dom.Node
	header      bool
	cells       []tableCell
}
type tableEdge struct {
	width  float64
	color  style.Color
	source SourceID
	cell   bool
}

type tableGrid struct {
	columns              int
	horizontal, vertical []tableEdge
}

func (g tableGrid) h(row, col int) *tableEdge { return &g.horizontal[row*g.columns+col] }
func (g tableGrid) v(row, col int) *tableEdge { return &g.vertical[row*(g.columns+1)+col] }
func chooseEdge(dst *tableEdge, edge tableEdge) {
	if edge.width > dst.width || edge.width == dst.width && (edge.cell && !dst.cell || edge.cell == dst.cell && edge.source > dst.source) {
		*dst = edge
	}
}

func tableError(message string) error {
	return fmt.Errorf("%w: %s", session.ErrUnsupportedFeature, message)
}
func whitespaceNode(n *dom.Node) bool { return n.Tag == "" && strings.Trim(n.Text, " \t\r\n\f") == "" }

// tableRows accepts normalized table roles; it never fabricates anonymous cells.
func (b *builder) tableRows(n *dom.Node) ([]tableRow, int, error) {
	var headers, body []tableRow
	columns := 0
	var descendants func(*dom.Node) error
	descendants = func(node *dom.Node) error {
		if err := b.s.Charge("layout_work", 1); err != nil {
			return err
		}
		if node.Style.Display == style.None {
			return nil
		}
		if n.Style.TableLayout != "fixed" && node.Style.Width.Unit == style.Percent {
			return tableError("percentage descendant widths in auto table cells; use fixed layout")
		}
		if node.Style.Display == style.Table || node.Tag == "table" {
			return tableError("nested tables")
		}
		for _, c := range node.Children {
			if err := descendants(c); err != nil {
				return err
			}
		}
		return nil
	}
	addRow := func(row, group *dom.Node, header bool) error {
		if err := b.tablePart(row, style.TableRow); err != nil {
			return err
		}
		r := tableRow{node: row, group: group, header: header}
		col := 0
		for _, c := range row.Children {
			if whitespaceNode(c) || c.Style.Display == style.None {
				continue
			}
			if err := b.tablePart(c, style.TableCell); err != nil {
				return err
			}
			if raw, ok := c.Attributes["rowspan"]; ok && strings.TrimSpace(raw) != "1" {
				return tableError("rowspan other than one")
			}
			span := 1
			if raw, ok := c.Attributes["colspan"]; ok {
				value, err := strconv.Atoi(strings.TrimSpace(raw))
				if err != nil || value < 1 || value > 1000 {
					return fmt.Errorf("%w: colspan must be 1..1000", session.ErrInvalidInput)
				}
				span = value
			}
			if err := b.s.Charge("layout_work", int64(span)); err != nil {
				return err
			}
			if col > 1000-span {
				return tableError("tables are limited to 1000 columns")
			}
			for _, child := range c.Children {
				if err := descendants(child); err != nil {
					return err
				}
			}
			r.cells = append(r.cells, tableCell{node: c, column: col, span: span})
			col += span
		}
		columns = max(columns, col)
		if header {
			headers = append(headers, r)
		} else {
			body = append(body, r)
		}
		return nil
	}
	for _, child := range n.Children {
		if whitespaceNode(child) || child.Style.Display == style.None {
			continue
		}
		if child.Tag == "tfoot" || child.Tag == "caption" || child.Tag == "col" || child.Tag == "colgroup" {
			return nil, 0, tableError("caption/col/colgroup/tfoot are unsupported")
		}
		switch child.Style.Display {
		case style.TableRow:
			if err := addRow(child, n, false); err != nil {
				return nil, 0, err
			}
		case style.TableHeaderGroup, style.TableRowGroup:
			if child.Tag == "tfoot" {
				return nil, 0, tableError("tfoot; use ordinary body rows or a following block for totals")
			}
			if err := b.tablePart(child, child.Style.Display); err != nil {
				return nil, 0, err
			}
			for _, row := range child.Children {
				if whitespaceNode(row) || row.Style.Display == style.None {
					continue
				}
				if err := addRow(row, child, child.Style.Display == style.TableHeaderGroup); err != nil {
					return nil, 0, err
				}
			}
		default:
			return nil, 0, tableError("table requires rows or header/body row groups; caption/col/colgroup/tfoot are unsupported")
		}
	}
	return append(headers, body...), columns, nil
}

func (b *builder) tablePart(n *dom.Node, display style.Display) error {
	if err := b.s.Charge("layout_work", 1); err != nil {
		return err
	}
	if n.Style.Display != display {
		return tableError("invalid table structure")
	}
	st := n.Style
	if st.Height.Unit != style.Auto || st.BreakBefore != "auto" || st.BreakAfter != "auto" {
		return tableError("table-part height/page breaks")
	}
	for i := range 4 {
		if st.Margin[i].Value != 0 || st.Margin[i].Unit == style.Auto {
			return tableError("table-part margins")
		}
		if display != style.TableCell && (st.Padding[i].Value != 0 || st.Border[i].Style != "none") {
			return tableError("row/group padding and borders")
		}
	}
	if display != style.TableCell && st.Width.Unit != style.Auto {
		return tableError("row/group widths")
	}
	return nil
}

func (b *builder) table(n *dom.Node, x, y, width float64) (float64, error) {
	if n.Style.TableLayout == "fixed" && n.Style.Width.Unit == style.Auto {
		return 0, tableError("fixed table layout requires an explicit table width")
	}
	rows, count, err := b.tableRows(n)
	if err != nil {
		return 0, err
	}
	table := Table{Source: SourceID(n.ID), Bounds: Rect{X: x, Y: y, Width: width}}
	if count == 0 {
		if n.Style.BorderCollapse == "collapse" {
			for _, edge := range n.Style.Border {
				if edge.Style != "none" && edge.Width.Value > 0 {
					return 0, tableError("collapsed table borders require at least one cell")
				}
			}
		}
		for i, row := range rows {
			table.Rows = append(table.Rows, TableRow{Source: SourceID(row.node.ID), Group: SourceID(row.group.ID), Header: row.header, Bounds: table.Bounds})
			b.result.Breaks = append(b.result.Breaks, BreakOpportunity{Source: SourceID(row.node.ID), Kind: RowBoundary, Offset: i + 1})
		}
		b.result.Tables = append(b.result.Tables, table)
		return 0, nil
	}
	// Reserve dense border-grid work before allocation. Spans can otherwise make a
	// tiny DOM expand into an expensive row-by-column matrix.
	work := int64(len(rows)+1)*int64(count) + int64(len(rows))*int64(count+1)
	if err := b.s.Charge("layout_work", work); err != nil {
		return 0, err
	}
	grid := tableGrid{columns: count, horizontal: make([]tableEdge, (len(rows)+1)*count), vertical: make([]tableEdge, len(rows)*(count+1))}
	collapse := n.Style.BorderCollapse == "collapse"
	if collapse {
		_, _, edges := b.edges(n.Style, width)
		candidate := func(side int) tableEdge {
			return tableEdge{width: edges[side], color: n.Style.Border[side].Color, source: SourceID(n.ID)}
		}
		for c := range count {
			chooseEdge(grid.h(0, c), candidate(0))
			chooseEdge(grid.h(len(rows), c), candidate(2))
		}
		for r := range rows {
			chooseEdge(grid.v(r, 0), candidate(3))
			chooseEdge(grid.v(r, count), candidate(1))
		}
	}
	for r := range rows {
		for j := range rows[r].cells {
			cell := &rows[r].cells[j]
			_, padding, border := b.edges(cell.node.Style, width)
			cell.padding = padding
			if !collapse {
				cell.inset = border
				continue
			}
			candidate := func(side int) tableEdge {
				return tableEdge{width: border[side], color: cell.node.Style.Border[side].Color, source: SourceID(cell.node.ID), cell: true}
			}
			for c := cell.column; c < cell.column+cell.span; c++ {
				chooseEdge(grid.h(r, c), candidate(0))
				chooseEdge(grid.h(r+1, c), candidate(2))
			}
			chooseEdge(grid.v(r, cell.column), candidate(3))
			chooseEdge(grid.v(r, cell.column+cell.span), candidate(1))
		}
	}
	if collapse {
		for r := range rows {
			for j := range rows[r].cells {
				c := &rows[r].cells[j]
				end := c.column + c.span
				for col := c.column; col < end; col++ {
					c.inset[0] = math.Max(c.inset[0], grid.h(r, col).width/2)
					c.inset[2] = math.Max(c.inset[2], grid.h(r+1, col).width/2)
				}
				c.inset[3] = grid.v(r, c.column).width / 2
				c.inset[1] = grid.v(r, end).width / 2
				if r == 0 {
					c.inset[0] *= 2
				}
				if r == len(rows)-1 {
					c.inset[2] *= 2
				}
				if c.column == 0 {
					c.inset[3] *= 2
				}
				if end == count {
					c.inset[1] *= 2
				}
			}
		}
	}
	columns, err := b.tableColumns(n, rows, count, width)
	if err != nil {
		return 0, err
	}
	table.Columns = columns
	xs := make([]float64, count+1)
	xs[0] = x
	for i, w := range columns {
		xs[i+1] = xs[i] + w
	}
	ys := make([]float64, len(rows)+1)
	ys[0] = y
	// Measure cells once at their final width, before painting any row backgrounds.
	for r := range rows {
		rowHeight := 0.0
		if collapse {
			for col := range count {
				top, bottom := grid.h(r, col).width/2, grid.h(r+1, col).width/2
				if r == 0 {
					top *= 2
				}
				if r == len(rows)-1 {
					bottom *= 2
				}
				rowHeight = math.Max(rowHeight, top+bottom)
			}
		}
		for j := range rows[r].cells {
			cell := &rows[r].cells[j]
			cellWidth := xs[cell.column+cell.span] - xs[cell.column]
			contentWidth := cellWidth - cell.padding[1] - cell.padding[3] - cell.inset[1] - cell.inset[3]
			if contentWidth < -0.0001 {
				return 0, fmt.Errorf("%w: cell edges exceed its column width", session.ErrUnbreakableContent)
			}
			contentWidth = math.Max(0, contentWidth)
			clone := *cell.node
			clone.Style.Display = style.Block
			clone.Style.Width = style.Length{Value: contentWidth, Unit: style.Point}
			clone.Style.Margin = [4]style.Length{}
			clone.Style.Padding = [4]style.Length{}
			clone.Style.Border = [4]style.Border{}
			clone.Style.Background = style.Color{}
			fragment, err := b.localBlock(&clone, contentWidth)
			if err != nil {
				return 0, fmt.Errorf("cell %d: %w", cell.node.ID, err)
			}
			// The synthetic content wrapper shares the cell's source ID. Keep
			// only its children; the real cell box is recorded at row height.
			if len(fragment.boxes) > 0 {
				fragment.boxes = fragment.boxes[:len(fragment.boxes)-1]
			}
			cell.fragment = fragment
			rowHeight = math.Max(rowHeight, fragment.height+cell.padding[0]+cell.padding[2]+cell.inset[0]+cell.inset[2])
		}
		ys[r+1] = ys[r] + rowHeight
	}
	table.Bounds.Height = ys[len(rows)] - y
	if err := b.check(table.Bounds); err != nil {
		return 0, err
	}
	for r, row := range rows {
		rect := Rect{X: x, Y: ys[r], Width: width, Height: ys[r+1] - ys[r]}
		record := TableRow{Source: SourceID(row.node.ID), Group: SourceID(row.group.ID), Header: row.header, Bounds: rect}
		// Group backgrounds occupy the union of their contiguous rows.
		if row.group != n {
			if err := b.tableFill(SourceID(row.group.ID), rect, row.group.Style.Background); err != nil {
				return 0, err
			}
		}
		if err := b.tableFill(SourceID(row.node.ID), rect, row.node.Style.Background); err != nil {
			return 0, err
		}
		for _, cell := range row.cells {
			cellRect := Rect{X: xs[cell.column], Y: ys[r], Width: xs[cell.column+cell.span] - xs[cell.column], Height: rect.Height}
			content := Rect{X: cellRect.X + cell.padding[3] + cell.inset[3], Y: cellRect.Y + cell.padding[0] + cell.inset[0], Width: cell.fragment.width, Height: cell.fragment.height}
			if err := b.tableFill(SourceID(cell.node.ID), cellRect, cell.node.Style.Background); err != nil {
				return 0, err
			}
			if err := b.placeAtomic(cell.fragment, content.X, content.Y); err != nil {
				return 0, err
			}
			if !collapse {
				if err := b.cellBorders(cell.node, cellRect, cell.inset); err != nil {
					return 0, err
				}
			}
			b.result.Boxes = append(b.result.Boxes, Box{Source: SourceID(cell.node.ID), Style: cell.node.Style, Bounds: cellRect})
			record.Cells = append(record.Cells, TableCell{Source: SourceID(cell.node.ID), Column: cell.column, Colspan: cell.span, Bounds: cellRect, ContentBounds: content})
		}
		b.result.Boxes = append(b.result.Boxes, Box{Source: record.Source, Style: row.node.Style, Bounds: rect})
		b.result.Breaks = append(b.result.Breaks, BreakOpportunity{Source: record.Source, Kind: RowBoundary, Offset: r + 1, Avoid: row.node.Style.BreakInside == "avoid" || row.group.Style.BreakInside == "avoid" || n.Style.BreakInside == "avoid"})
		table.Rows = append(table.Rows, record)
	}
	if collapse {
		if err := b.collapsedBorders(grid, xs, ys); err != nil {
			return 0, err
		}
	}
	// Preserve group styling/avoid ranges without inventing anonymous rows.
	for first := 0; first < len(rows); {
		last := first + 1
		for last < len(rows) && rows[last].group.ID == rows[first].group.ID {
			last++
		}
		group := rows[first].group
		if group != n {
			b.result.Boxes = append(b.result.Boxes, Box{Source: SourceID(group.ID), Style: group.Style, Bounds: Rect{X: x, Y: ys[first], Width: width, Height: ys[last] - ys[first]}})
		}
		first = last
	}
	b.result.Tables = append(b.result.Tables, table)
	return table.Bounds.Height, nil
}

func (b *builder) tableColumns(n *dom.Node, rows []tableRow, count int, width float64) ([]float64, error) {
	cols := make([]float64, count)
	extra := func(c tableCell) float64 { return c.padding[1] + c.padding[3] + c.inset[1] + c.inset[3] }
	hint := func(c tableCell) float64 {
		return c.node.Style.Width.Resolve(width, c.node.Style.FontSizePt, b.rootSize) + extra(c)
	}
	if n.Style.TableLayout == "fixed" {
		assigned := make([]bool, count)
		// Single-column hints take priority over span hints, independent of source order.
		first := append([]tableCell(nil), rows[0].cells...)
		sort.SliceStable(first, func(i, j int) bool { return first[i].span < first[j].span })
		for _, c := range first {
			if c.node.Style.Width.Unit == style.Auto {
				continue
			}
			current, unknown := 0.0, 0
			for i := c.column; i < c.column+c.span; i++ {
				current += cols[i]
				if !assigned[i] {
					unknown++
				}
			}
			target := hint(c)
			if unknown > 0 {
				for i := c.column; i < c.column+c.span; i++ {
					if !assigned[i] {
						cols[i] = math.Max(0, target-current) / float64(unknown)
						assigned[i] = true
					}
				}
			}
			if target > current+0.0001 && unknown == 0 {
				return nil, fmt.Errorf("%w: conflicting fixed column hints", session.ErrUnbreakableContent)
			}
		}
		used, unknown := 0.0, 0
		for i, v := range cols {
			used += v
			if !assigned[i] {
				unknown++
			}
		}
		if used > width+0.0001 {
			return nil, fmt.Errorf("%w: fixed column hints exceed table width", session.ErrUnbreakableContent)
		}
		for i := range cols {
			if unknown == 0 {
				cols[i] += (width - used) / float64(count)
			} else if !assigned[i] {
				cols[i] = (width - used) / float64(unknown)
			}
		}
		return cols, nil
	}
	type constraint struct {
		cell             tableCell
		minimum, maximum float64
	}
	var constraints []constraint
	for _, row := range rows {
		for _, c := range row.cells {
			lo, hi, err := b.intrinsic(c.node.Children, width)
			if err != nil {
				return nil, err
			}
			lo += extra(c)
			hi += extra(c)
			if c.node.Style.Width.Unit != style.Auto {
				lo = math.Max(lo, hint(c))
				hi = math.Max(hi, lo)
			}
			constraints = append(constraints, constraint{c, lo, hi})
		}
	}
	sort.SliceStable(constraints, func(i, j int) bool { return constraints[i].cell.span < constraints[j].cell.span })
	maxima := make([]float64, count)
	distribute := func(values []float64, c tableCell, target float64) {
		sum := 0.0
		for i := c.column; i < c.column+c.span; i++ {
			sum += values[i]
		}
		if target > sum {
			for i := c.column; i < c.column+c.span; i++ {
				values[i] += (target - sum) / float64(c.span)
			}
		}
	}
	for _, c := range constraints {
		if err := b.s.Charge("layout_work", int64(c.cell.span)*4); err != nil {
			return nil, err
		}
		distribute(cols, c.cell, c.minimum)
		distribute(maxima, c.cell, c.maximum)
	}
	minimum, maximum := 0.0, 0.0
	for i := range cols {
		maxima[i] = math.Max(maxima[i], cols[i])
		minimum += cols[i]
		maximum += maxima[i]
	}
	if minimum > width+0.0001 {
		return nil, fmt.Errorf("%w: table min-content width exceeds available width", session.ErrUnbreakableContent)
	}
	spare := math.Max(0, width-minimum)
	flexibility := maximum - minimum
	for i := range cols {
		if spare >= flexibility {
			cols[i] = maxima[i] + (spare-flexibility)/float64(count)
		} else if flexibility > 0 {
			cols[i] += spare * (maxima[i] - cols[i]) / flexibility
		}
	}
	return cols, nil
}

// intrinsic uses the same shaped inline units as final layout. It is a bounded
// print algorithm: longest unbroken unit and widest explicit line, recursively
// combined for blocks. Forced word splitting does not reduce intrinsic minima.
func (b *builder) intrinsic(nodes []*dom.Node, available float64) (float64, float64, error) {
	minimum, maximum := 0.0, 0.0
	var pending []*dom.Node
	flush := func() error {
		units, err := b.collect(pending, available)
		if err != nil {
			return err
		}
		pending = nil
		line, leading := 0.0, true
		trailing := 0.0
		for _, u := range units {
			if u.hard {
				maximum = math.Max(maximum, line-trailing)
				line = 0
				trailing = 0
				leading = true
				continue
			}
			if u.space && u.collapse && leading {
				continue
			}
			leading = false
			line += u.width
			trailing = 0
			if u.space && u.collapse {
				trailing = u.width
			}
			if !u.space || !u.collapse {
				minimum = math.Max(minimum, u.width)
			}
		}
		maximum = math.Max(maximum, line-trailing)
		return nil
	}
	for _, n := range nodes {
		if err := b.s.Charge("layout_work", 1); err != nil {
			return 0, 0, err
		}
		if n.Style.Display == style.None {
			continue
		}
		// Intrinsic percentage descendants depend on the still-unknown cell width.
		if n.Style.Display == style.Grid {
			return 0, 0, tableError("intrinsic Grid sizing requires a definite containing context; use fixed table layout")
		}
		if n.Style.Width.Unit == style.Percent {
			return 0, 0, tableError("percentage descendant widths in auto table cells; use fixed layout")
		}
		if !isBlock(n) {
			pending = append(pending, n)
			continue
		}
		if err := flush(); err != nil {
			return 0, 0, err
		}
		if err := b.validate(n); err != nil {
			return 0, 0, err
		}
		lo, hi, err := b.intrinsic(n.Children, available)
		if err != nil {
			return 0, 0, err
		}
		if n.Tag == "img" {
			u, err := b.image(n, available)
			if err != nil {
				return 0, 0, err
			}
			lo = u.width
			hi = u.width
		}
		if n.Style.Width.Unit != style.Auto {
			lo = math.Max(lo, n.Style.Width.Resolve(available, n.Style.FontSizePt, b.rootSize))
			hi = math.Max(hi, lo)
		}
		m, p, edge := b.edges(n.Style, available)
		extra := m[1] + m[3] + p[1] + p[3] + edge[1] + edge[3]
		if n.Tag == "ul" || n.Tag == "ol" {
			extra += 24
		}
		minimum = math.Max(minimum, lo+extra)
		maximum = math.Max(maximum, hi+extra)
	}
	if err := flush(); err != nil {
		return 0, 0, err
	}
	return minimum, math.Max(minimum, maximum), nil
}

func (b *builder) tableFill(source SourceID, r Rect, color style.Color) error {
	if color.A == 0 || r.Width == 0 || r.Height == 0 {
		return nil
	}
	return b.emit(paint.Operation{Kind: paint.FillRect, Source: source, Bounds: r, Color: color})
}
func (b *builder) tableStroke(source SourceID, r Rect, color style.Color) error {
	if color.A == 0 || r.Width == 0 || r.Height == 0 {
		return nil
	}
	return b.emit(paint.Operation{Kind: paint.FillRect, Source: source, Bounds: r, Color: color, Border: true})
}
func (b *builder) cellBorders(n *dom.Node, r Rect, widths [4]float64) error {
	for side, w := range widths {
		if w == 0 {
			continue
		}
		edge := r
		switch side {
		case 0:
			edge.Height = w
		case 1:
			edge.X += r.Width - w
			edge.Width = w
		case 2:
			edge.Y += r.Height - w
			edge.Height = w
		case 3:
			edge.Width = w
		}
		if err := b.tableStroke(SourceID(n.ID), edge, n.Style.Border[side].Color); err != nil {
			return err
		}
	}
	return nil
}
func (b *builder) collapsedBorders(g tableGrid, xs, ys []float64) error {
	bounds := Rect{X: xs[0], Y: ys[0], Width: xs[len(xs)-1] - xs[0], Height: ys[len(ys)-1] - ys[0]}
	paintEdge := func(source SourceID, r Rect, color style.Color) error {
		if r.X < bounds.X-0.0001 || r.Y < bounds.Y-0.0001 || r.X+r.Width > bounds.X+bounds.Width+0.0001 || r.Y+r.Height > bounds.Y+bounds.Height+0.0001 {
			return fmt.Errorf("%w: collapsed border exceeds table grid", session.ErrUnbreakableContent)
		}
		return b.tableStroke(source, r, color)
	}
	for r := range ys {
		for c := 0; c < g.columns; c++ {
			e := *g.h(r, c)
			if e.width == 0 {
				continue
			}
			y := ys[r] - e.width/2
			if r == 0 {
				y = ys[r]
			}
			if r == len(ys)-1 {
				y = ys[r] - e.width
			}
			if err := paintEdge(e.source, Rect{X: xs[c], Y: y, Width: xs[c+1] - xs[c], Height: e.width}, e.color); err != nil {
				return err
			}
		}
	}
	for r := 0; r < len(ys)-1; r++ {
		for c := range xs {
			e := *g.v(r, c)
			if e.width == 0 {
				continue
			}
			x := xs[c] - e.width/2
			if c == 0 {
				x = xs[c]
			}
			if c == len(xs)-1 {
				x = xs[c] - e.width
			}
			if err := paintEdge(e.source, Rect{X: x, Y: ys[r], Width: e.width, Height: ys[r+1] - ys[r]}, e.color); err != nil {
				return err
			}
		}
	}
	return nil
}

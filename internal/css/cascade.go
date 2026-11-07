package css

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	dom "github.com/arham09/html2pdf/internal/html"
	"github.com/arham09/html2pdf/internal/session"
	"github.com/arham09/html2pdf/internal/style"
)

var initialValues = map[string]string{
	"display": "inline", "position": "static", "float": "none", "width": "auto", "height": "auto",
	"font-family": "Noto Sans", "font-size": "12pt", "font-weight": "400", "font-style": "normal", "line-height": "normal",
	"color": "black", "background-color": "transparent", "text-align": "left", "white-space": "normal", "overflow-wrap": "normal", "word-wrap": "normal",
	"border-collapse": "separate", "table-layout": "auto", "break-before": "auto", "break-after": "auto", "break-inside": "auto",
	"flex-direction": "row", "flex-wrap": "nowrap", "flex-grow": "0", "flex-shrink": "1", "flex-basis": "auto",
	"justify-content": "flex-start", "align-items": "stretch", "align-self": "auto", "align-content": "stretch",
	"row-gap": "0", "column-gap": "0", "min-width": "auto", "min-height": "auto",
	"grid-template-columns": "none", "grid-column": "auto", "grid-row": "auto", "grid-auto-flow": "row",
	"justify-items": "stretch", "justify-self": "auto",
}
var inherited = map[string]bool{"font-family": true, "font-size": true, "font-weight": true, "font-style": true, "line-height": true, "color": true, "text-align": true, "white-space": true, "overflow-wrap": true, "word-wrap": true, "border-collapse": true}

type winner struct {
	declaration
	specificity specificity
	inline      bool
}

func (a winner) beats(b winner) bool {
	if a.important != b.important {
		return a.important
	}
	if a.inline != b.inline {
		return a.inline
	}
	if a.specificity != b.specificity {
		return b.specificity.less(a.specificity)
	}
	return a.order >= b.order
}
func defaults(tag string) map[string]string {
	out := make(map[string]string)
	for k, v := range initialValues {
		out[k] = v
	}
	for _, side := range sides {
		out["margin-"+side] = "0"
		out["padding-"+side] = "0"
		out["border-"+side+"-width"] = "3px"
		out["border-"+side+"-style"] = "none"
		out["border-"+side+"-color"] = "currentcolor"
	}
	return out
}
func ua(values map[string]string, tag string) {
	switch tag {
	case "html", "body", "div", "section", "p", "h1", "h2", "h3", "h4", "h5", "h6", "ul", "ol", "hr":
		values["display"] = "block"
	case "table":
		values["display"] = "table"
	case "li":
		values["display"] = "list-item"
	case "thead":
		values["display"] = "table-header-group"
	case "tbody":
		values["display"] = "table-row-group"
	case "tr":
		values["display"] = "table-row"
	case "td", "th":
		values["display"] = "table-cell"
	}
	if tag == "strong" || tag == "th" || len(tag) == 2 && tag[0] == 'h' && tag[1] >= '1' && tag[1] <= '6' {
		values["font-weight"] = "700"
	}
	if tag == "em" {
		values["font-style"] = "italic"
	}
	if len(tag) == 2 && tag[0] == 'h' && tag[1] >= '1' && tag[1] <= '6' {
		values["font-size"] = []string{"2em", "1.5em", "1.17em", "1em", "0.83em", "0.67em"}[tag[1]-'1']
		values["margin-top"] = "0.67em"
		values["margin-bottom"] = "0.67em"
	}
	if tag == "p" || tag == "ul" || tag == "ol" {
		values["margin-top"] = "1em"
		values["margin-bottom"] = "1em"
	}
}

// Compute preserves source IDs and writes typed styles to the normalized nodes.
func (sheet *Sheet) Compute(s *session.Session, doc *dom.Document, strict bool) (Page, error) {
	previous := make(map[*dom.Node]map[string]string)
	rootSize := 12.0
	for _, n := range doc.Nodes {
		if err := s.Charge("layout_work", 1); err != nil {
			return Page{}, err
		}
		vals := defaults(n.Tag)
		parent := previous[n.Parent]
		base := 12.0
		if n.Parent != nil {
			base = n.Parent.Style.FontSizePt
		}
		for k := range inherited {
			if v, ok := parent[k]; ok {
				vals[k] = v
			}
		}
		ua(vals, n.Tag)
		wins := map[string]winner{}
		apply := func(d declaration, sp specificity, inline bool) {
			v := winner{d, sp, inline}
			if old, ok := wins[d.name]; !ok || v.beats(old) {
				wins[d.name] = v
			}
		}
		if n.Tag != "" {
			for _, rule := range sheet.rules {
				for _, sel := range rule.selectors {
					matched, err := sel.matches(s, n)
					if err != nil {
						return Page{}, err
					}
					if matched {
						for _, d := range rule.declarations {
							if err := s.Charge("layout_work", 1); err != nil {
								return Page{}, err
							}
							apply(d, sel.specificity, false)
						}
					}
				}
			}
			if raw, ok := n.Attributes["style"]; ok {
				var inline []declaration
				if err := (&parser{s, strict, sheet}).parse(raw, true, n.ID, &inline); err != nil {
					return Page{}, err
				}
				for _, d := range inline {
					apply(d, specificity{}, true)
				}
			}
		}
		initial := defaults(n.Tag)
		for name, win := range wins {
			value := win.value
			if value == "initial" || value == "unset" && !inherited[name] {
				value = initial[name]
			} else if value == "inherit" || value == "unset" {
				if v, ok := parent[name]; ok {
					value = v
				} else {
					value = initial[name]
				}
			}
			vals[name] = value
		}
		if vals["color"] == "currentcolor" {
			if c, ok := parent["color"]; ok {
				vals["color"] = c
			} else {
				vals["color"] = "black"
			}
		}
		computed, err := materialize(vals, base, rootSize)
		if err != nil {
			return Page{}, fmt.Errorf("%w: invalid computed geometry at source %d", session.ErrInvalidInput, n.ID)
		}
		n.Style = computed
		previous[n] = vals
		if n.Tag == "html" {
			rootSize = computed.FontSizePt
		}
	}
	return sheet.computedPage(), nil
}

func materialize(v map[string]string, parent, root float64) (style.Computed, error) {
	var out style.Computed
	out.Display = style.Inline
	switch v["display"] {
	case "block":
		out.Display = style.Block
	case "flex":
		out.Display = style.Flex
	case "grid":
		out.Display = style.Grid
	case "inline-block":
		out.Display = style.InlineBlock
	case "none":
		out.Display = style.None
	case "table":
		out.Display = style.Table
	case "table-row":
		out.Display = style.TableRow
	case "table-row-group":
		out.Display = style.TableRowGroup
	case "table-header-group":
		out.Display = style.TableHeaderGroup
	case "table-cell":
		out.Display = style.TableCell
	case "list-item":
		out.Display = style.ListItem
	}
	switch v["font-size"] {
	case "small":
		out.FontSizePt = 10
	case "medium":
		out.FontSizePt = 12
	case "large":
		out.FontSizePt = 14
	case "larger":
		out.FontSizePt = parent * 1.2
	case "smaller":
		out.FontSizePt = parent / 1.2
	default:
		l, _ := length(v["font-size"], false, false)
		out.FontSizePt = l.Resolve(parent, parent, root)
	}
	if !finite(out.FontSizePt) || out.FontSizePt <= 0 || out.FontSizePt > 1000000 {
		return out, fmt.Errorf("font size out of bounds")
	}
	out.FontFamily = v["font-family"]
	out.FontWeight = 400
	if keyword(v["font-weight"], "bold", "700") {
		out.FontWeight = 700
	}
	out.Italic = v["font-style"] == "italic"
	if v["color"] == "currentcolor" {
		v["color"] = "black"
	}
	out.Color, _ = color(v["color"])
	if v["background-color"] == "currentcolor" {
		out.Background = out.Color
	} else {
		out.Background, _ = color(v["background-color"])
	}
	out.TextAlign = v["text-align"]
	out.WhiteSpace = v["white-space"]
	out.OverflowWrap = v["overflow-wrap"]
	if v["word-wrap"] != "normal" && out.OverflowWrap == "normal" {
		out.OverflowWrap = v["word-wrap"]
	}
	if v["line-height"] == "normal" {
		out.LineHeightFactor = 1.2
	} else if n, err := strconv.ParseFloat(v["line-height"], 64); err == nil {
		out.LineHeightFactor = n
	}
	if out.LineHeightFactor != 0 {
		out.LineHeightPt = out.FontSizePt * out.LineHeightFactor
	} else {
		l, _ := length(v["line-height"], false, false)
		out.LineHeightPt = l.Resolve(out.FontSizePt, out.FontSizePt, root)
	}
	if !finite(out.LineHeightPt) || out.LineHeightPt <= 0 || out.LineHeightPt > 1e9 {
		return out, fmt.Errorf("line height out of bounds")
	}
	// Canonicalize inherited computed values; keep unitless line-height a factor.
	v["font-size"] = point(out.FontSizePt)
	if out.LineHeightFactor == 0 {
		v["line-height"] = point(out.LineHeightPt)
	}
	v["color"] = colorText(out.Color)
	v["background-color"] = colorText(out.Background)
	resolve := func(name string, negative, auto bool) style.Length {
		l, _ := length(v[name], negative, auto)
		if l.Unit == style.Em || l.Unit == style.Rem {
			l = style.Length{Value: l.Resolve(0, out.FontSizePt, root), Unit: style.Point}
			v[name] = point(l.Value)
		}
		return l
	}
	out.Width = resolve("width", false, true)
	out.Height = resolve("height", false, true)
	out.MinWidth = resolve("min-width", false, true)
	out.MinHeight = resolve("min-height", false, true)
	out.FlexBasis = resolve("flex-basis", false, true)
	out.RowGap = resolve("row-gap", false, false)
	out.ColumnGap = resolve("column-gap", false, false)
	out.FlexGrow, _ = strconv.ParseFloat(v["flex-grow"], 64)
	out.FlexShrink, _ = strconv.ParseFloat(v["flex-shrink"], 64)
	out.FlexDirection, out.FlexWrap = v["flex-direction"], v["flex-wrap"]
	out.JustifyContent, out.AlignItems = v["justify-content"], v["align-items"]
	out.AlignSelf, out.AlignContent = v["align-self"], v["align-content"]
	out.JustifyContent = alignment(out.JustifyContent)
	out.AlignItems, out.AlignSelf = alignment(out.AlignItems), alignment(out.AlignSelf)
	out.AlignContent = alignment(out.AlignContent)
	out.JustifyItems, out.JustifySelf = alignment(v["justify-items"]), alignment(v["justify-self"])
	out.GridColumn, _ = gridPlacement(v["grid-column"], style.MaxGridColumns)
	out.GridRow, _ = gridPlacement(v["grid-row"], style.MaxGridRows)
	if v["grid-template-columns"] != "none" {
		columns, err := gridTracks(v["grid-template-columns"])
		if err != nil {
			return out, err
		}
		canonical := make([]string, len(columns))
		for i, l := range columns {
			if l.Unit == style.Em || l.Unit == style.Rem {
				l = style.Length{Value: l.Resolve(0, out.FontSizePt, root)}
				columns[i] = l
			}
			suffix := map[style.Unit]string{style.Point: "pt", style.Pixel: "px", style.Percent: "%", style.Fraction: "fr"}[l.Unit]
			canonical[i] = strconv.FormatFloat(l.Value, 'g', -1, 64) + suffix
		}
		v["grid-template-columns"] = strings.Join(canonical, " ")
		out.GridColumns = &style.GridTracks{Columns: columns}
	}
	for i, side := range sides {
		out.Margin[i] = resolve("margin-"+side, true, true)
		out.Padding[i] = resolve("padding-"+side, false, false)
		out.Border[i].Width = resolve("border-"+side+"-width", false, false)
		out.Border[i].Style = v["border-"+side+"-style"]
		ink := v["border-"+side+"-color"]
		if ink == "currentcolor" {
			out.Border[i].Color = out.Color
		} else {
			out.Border[i].Color, _ = color(ink)
		}
		v["border-"+side+"-color"] = colorText(out.Border[i].Color)
	}
	out.BorderCollapse = v["border-collapse"]
	out.TableLayout = v["table-layout"]
	out.BreakBefore = v["break-before"]
	out.BreakAfter = v["break-after"]
	out.BreakInside = v["break-inside"]
	return out, nil
}
func colorText(c style.Color) string {
	if c.A == 0 {
		return "transparent"
	}
	return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
}

func alignment(v string) string {
	if v == "start" {
		return "flex-start"
	}
	if v == "end" {
		return "flex-end"
	}
	return v
}
func finite(v float64) bool  { return !math.IsNaN(v) && !math.IsInf(v, 0) }
func point(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) + "pt" }

func (sheet *Sheet) computedPage() Page {
	wins := make(map[string]declaration)
	for _, d := range sheet.page {
		old, ok := wins[d.name]
		if !ok || d.important && !old.important || d.important == old.important && d.order >= old.order {
			wins[d.name] = d
		}
	}
	var out Page
	if d, ok := wins["size"]; ok {
		w, h, _ := pageSize(strings.ToLower(d.value))
		out.WidthPt = &w
		out.HeightPt = &h
	}
	for i, side := range sides {
		if d, ok := wins["margin-"+side]; ok {
			v, _ := absolute(d.value)
			out.Margins[i] = &v
		}
	}
	return out
}

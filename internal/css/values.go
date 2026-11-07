package css

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/arham09/html2pdf/internal/style"
)

var sides = []string{"top", "right", "bottom", "left"}

func length(raw string, negative, auto bool) (style.Length, error) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if auto && raw == "auto" {
		return style.Length{Unit: style.Auto}, nil
	}
	unit := style.Point
	factor := 1.0
	number := raw
	for _, suffix := range []string{"rem", "px", "pt", "mm", "cm", "in", "em", "%"} {
		if strings.HasSuffix(raw, suffix) {
			number = strings.TrimSuffix(raw, suffix)
			switch suffix {
			case "px":
				unit = style.Pixel
			case "mm":
				factor = 72 / 25.4
			case "cm":
				factor = 72 / 2.54
			case "in":
				factor = 72
			case "em":
				unit = style.Em
			case "rem":
				unit = style.Rem
			case "%":
				unit = style.Percent
			}
			break
		}
	}
	n, err := strconv.ParseFloat(number, 64)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || math.Abs(n) > 1000000 || (!negative && n < 0) || (number == raw && n != 0) {
		return style.Length{}, fmt.Errorf("invalid length")
	}
	return style.Length{Value: n * factor, Unit: unit}, nil
}
func absolute(raw string) (float64, error) {
	l, err := length(raw, false, false)
	if err != nil || l.Unit == style.Percent || l.Unit == style.Em || l.Unit == style.Rem {
		return 0, fmt.Errorf("absolute length required")
	}
	return l.Resolve(0, 0, 0), nil
}

func color(raw string) (style.Color, error) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	named := map[string]string{"black": "#000000", "white": "#ffffff", "red": "#ff0000", "green": "#008000", "blue": "#0000ff", "gray": "#808080", "grey": "#808080", "yellow": "#ffff00", "navy": "#000080", "silver": "#c0c0c0", "maroon": "#800000", "teal": "#008080", "purple": "#800080", "orange": "#ffa500"}
	if raw == "transparent" {
		return style.Color{}, nil
	}
	if v, ok := named[raw]; ok {
		raw = v
	}
	if strings.HasPrefix(raw, "rgb(") && strings.HasSuffix(raw, ")") {
		parts := strings.Split(raw[4:len(raw)-1], ",")
		if len(parts) != 3 {
			return style.Color{}, fmt.Errorf("invalid rgb")
		}
		out := style.Color{A: 255}
		values := []*uint8{&out.R, &out.G, &out.B}
		for i, v := range parts {
			n, e := strconv.Atoi(strings.TrimSpace(v))
			if e != nil || n < 0 || n > 255 {
				return style.Color{}, fmt.Errorf("invalid rgb component")
			}
			*values[i] = uint8(n)
		}
		return out, nil
	}
	if len(raw) == 4 && raw[0] == '#' {
		raw = fmt.Sprintf("#%c%c%c%c%c%c", raw[1], raw[1], raw[2], raw[2], raw[3], raw[3])
	}
	if len(raw) == 7 && raw[0] == '#' {
		n, e := strconv.ParseUint(raw[1:], 16, 24)
		if e == nil {
			return style.Color{R: uint8(n >> 16), G: uint8(n >> 8), B: uint8(n), A: 255}, nil
		}
	}
	return style.Color{}, fmt.Errorf("invalid color")
}
func keyword(v string, allowed ...string) bool {
	for _, a := range allowed {
		if v == a {
			return true
		}
	}
	return false
}
func global(v string) bool { return keyword(v, "inherit", "initial", "unset") }
func edgeValues(raw string) ([4]string, error) {
	fields := strings.Fields(raw)
	var out [4]string
	if len(fields) < 1 || len(fields) > 4 {
		return out, fmt.Errorf("invalid edge shorthand")
	}
	out = [4]string{fields[0], fields[0], fields[0], fields[0]}
	if len(fields) > 1 {
		out[1] = fields[1]
		out[3] = fields[1]
	}
	if len(fields) > 2 {
		out[2] = fields[2]
	}
	if len(fields) > 3 {
		out[3] = fields[3]
	}
	return out, nil
}

func expand(name, raw string, page bool) ([]declaration, error) {
	if name == "word-wrap" {
		name = "overflow-wrap"
	}
	value := strings.TrimSpace(raw)
	lower := strings.ToLower(value)
	if name != "font-family" || global(lower) {
		value = lower
	}
	if name == "page-break-before" || name == "page-break-after" || name == "page-break-inside" {
		name = strings.TrimPrefix(name, "page-")
		if lower == "always" {
			value = "page"
			lower = value
		}
	}
	var result []declaration
	add := func(n, v string) { result = append(result, declaration{name: n, value: v}) }
	if name == "flex" {
		grow, shrink, basis, err := flexValues(value)
		if err != nil {
			return nil, err
		}
		add("flex-grow", grow)
		add("flex-shrink", shrink)
		add("flex-basis", basis)
	} else if name == "gap" {
		fields := strings.Fields(value)
		if len(fields) < 1 || len(fields) > 2 {
			return nil, fmt.Errorf("invalid gap")
		}
		if len(fields) > 1 && (global(fields[0]) || global(fields[1])) {
			return nil, fmt.Errorf("global gap value must stand alone")
		}
		add("row-gap", fields[0])
		add("column-gap", fields[len(fields)-1])
	} else if name == "flex-flow" {
		direction, wrap := "row", "nowrap"
		if global(value) {
			direction, wrap = value, value
		} else {
			seenDirection, seenWrap := false, false
			fields := strings.Fields(value)
			if len(fields) == 0 || len(fields) > 2 {
				return nil, fmt.Errorf("invalid flex-flow")
			}
			for _, f := range fields {
				if keyword(f, "row", "column") && !seenDirection {
					direction, seenDirection = f, true
				} else if keyword(f, "wrap", "nowrap") && !seenWrap {
					wrap, seenWrap = f, true
				} else {
					return nil, fmt.Errorf("unsupported flex-flow")
				}
			}
		}
		add("flex-direction", direction)
		add("flex-wrap", wrap)
	} else if name == "margin" || name == "padding" || name == "border-width" || name == "border-style" || name == "border-color" {
		values, err := edgeValues(value)
		if err != nil {
			return nil, err
		}
		for i, side := range sides {
			property := name + "-" + side
			if strings.HasPrefix(name, "border-") {
				property = "border-" + side + strings.TrimPrefix(name, "border")
			}
			add(property, values[i])
		}
	} else if name == "border" || keyword(name, "border-top", "border-right", "border-bottom", "border-left") {
		targets := sides
		if name != "border" {
			targets = []string{strings.TrimPrefix(name, "border-")}
		}
		width, kind, ink := "3px", "none", "currentcolor"
		if global(lower) {
			width, kind, ink = lower, lower, lower
		} else {
			seen := map[string]bool{}
			fields := strings.Fields(value)
			if len(fields) == 0 {
				return nil, fmt.Errorf("empty border")
			}
			for _, field := range fields {
				f := strings.ToLower(field)
				key := ""
				if keyword(f, "solid", "none") {
					key = "style"
					kind = f
				} else if _, err := length(f, false, false); err == nil {
					key = "width"
					width = f
				} else if _, err := color(f); err == nil || f == "currentcolor" {
					key = "color"
					ink = f
				} else {
					return nil, fmt.Errorf("unsupported border")
				}
				if seen[key] {
					return nil, fmt.Errorf("duplicate border component")
				}
				seen[key] = true
			}
		}
		for _, side := range targets {
			add("border-"+side+"-width", width)
			add("border-"+side+"-style", kind)
			add("border-"+side+"-color", ink)
		}
	} else if name == "background" {
		add("background-color", value)
	} else {
		add(name, value)
	}
	for _, d := range result {
		if err := validateValue(d.name, d.value, page); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func validateValue(name, value string, page bool) error {
	v := strings.ToLower(value)
	if page {
		if name == "size" {
			_, _, err := pageSize(v)
			return err
		}
		if strings.HasPrefix(name, "margin-") {
			_, err := absolute(v)
			return err
		}
		return fmt.Errorf("unsupported page declaration")
	}
	if global(v) && knownProperty(name) {
		return nil
	}
	switch name {
	case "display":
		if keyword(v, "block", "flex", "grid", "inline", "inline-block", "none", "table", "table-row-group", "table-header-group", "table-row", "table-cell", "list-item") {
			return nil
		}
	case "position":
		if v == "static" {
			return nil
		}
	case "float":
		if v == "none" {
			return nil
		}
	case "width", "height", "flex-basis", "min-width", "min-height":
		_, err := length(v, false, true)
		return err
	case "flex-direction":
		if keyword(v, "row", "column") {
			return nil
		}
	case "flex-wrap":
		if keyword(v, "nowrap", "wrap") {
			return nil
		}
	case "flex-grow", "flex-shrink":
		n, err := strconv.ParseFloat(v, 64)
		if err == nil && finite(n) && n >= 0 && n <= 1e6 {
			return nil
		}
	case "row-gap", "column-gap":
		l, err := length(v, false, false)
		if err == nil && l.Unit != style.Percent {
			return nil
		}
	case "justify-content":
		if keyword(v, "start", "end", "flex-start", "flex-end", "center", "space-between", "space-around", "space-evenly") {
			return nil
		}
	case "align-items", "align-self", "align-content":
		if keyword(v, "start", "end", "flex-start", "flex-end", "center", "stretch") || name == "align-self" && v == "auto" || name == "align-content" && keyword(v, "space-between", "space-around", "space-evenly") {
			return nil
		}
	case "justify-items", "justify-self":
		if keyword(v, "start", "end", "flex-start", "flex-end", "center", "stretch") || name == "justify-self" && v == "auto" {
			return nil
		}
	case "grid-template-columns":
		_, err := gridTracks(v)
		return err
	case "grid-column":
		_, err := gridPlacement(v, style.MaxGridColumns)
		return err
	case "grid-row":
		_, err := gridPlacement(v, style.MaxGridRows)
		return err
	case "grid-auto-flow":
		if v == "row" {
			return nil
		}
	case "font-size":
		if keyword(v, "small", "medium", "large", "larger", "smaller") {
			return nil
		}
		l, e := length(v, false, false)
		if e == nil && l.Value > 0 {
			return nil
		}
	case "font-family":
		if value != "" && !strings.ContainsAny(value, "();{}\\") {
			for _, f := range strings.Split(value, ",") {
				f = strings.TrimSpace(f)
				if f == "" {
					return fmt.Errorf("empty font family")
				}
				if f[0] == '\'' || f[0] == '"' {
					if len(f) < 2 || f[len(f)-1] != f[0] {
						return fmt.Errorf("unclosed font name")
					}
				} else if strings.ContainsAny(f, "\"'") {
					return fmt.Errorf("invalid font name")
				}
			}
			return nil
		}
	case "font-weight":
		if keyword(v, "normal", "bold", "400", "700") {
			return nil
		}
	case "font-style":
		if keyword(v, "normal", "italic") {
			return nil
		}
	case "line-height":
		if v == "normal" {
			return nil
		}
		if n, e := strconv.ParseFloat(v, 64); e == nil && n > 0 && n <= 100 {
			return nil
		}
		if l, e := length(v, false, false); e == nil && l.Value > 0 {
			return nil
		}
	case "color", "background-color":
		if v == "currentcolor" {
			return nil
		}
		_, err := color(v)
		return err
	case "text-align":
		if keyword(v, "left", "right", "center") {
			return nil
		}
	case "white-space":
		if keyword(v, "normal", "nowrap", "pre", "pre-wrap", "pre-line") {
			return nil
		}
	case "overflow-wrap", "word-wrap":
		if keyword(v, "normal", "break-word", "anywhere") {
			return nil
		}
	case "border-collapse":
		if keyword(v, "collapse", "separate") {
			return nil
		}
	case "table-layout":
		if keyword(v, "auto", "fixed") {
			return nil
		}
	case "break-before", "break-after":
		if keyword(v, "auto", "page", "avoid") {
			return nil
		}
	case "break-inside":
		if keyword(v, "auto", "avoid") {
			return nil
		}
	default:
		for _, side := range sides {
			if name == "margin-"+side {
				_, err := length(v, true, true)
				return err
			}
			if name == "padding-"+side || name == "border-"+side+"-width" {
				l, err := length(v, false, false)
				if strings.HasPrefix(name, "border") && l.Unit == style.Percent {
					return fmt.Errorf("percent border")
				}
				return err
			}
			if name == "border-"+side+"-style" && keyword(v, "none", "solid") {
				return nil
			}
			if name == "border-"+side+"-color" {
				if v == "currentcolor" {
					return nil
				}
				_, err := color(v)
				return err
			}
		}
	}
	return fmt.Errorf("unsupported property/value")
}
func knownProperty(name string) bool {
	// Known inherited/global declarations are validated through a representative
	// initial value, not accepted just because their value happens to be 'inherit'.
	_, ok := initialValues[name]
	if ok {
		return true
	}
	for _, side := range sides {
		if name == "margin-"+side || name == "padding-"+side || strings.HasPrefix(name, "border-"+side+"-") {
			return keyword(strings.TrimPrefix(name, "border-"+side+"-"), "width", "style", "color") || name == "margin-"+side || name == "padding-"+side
		}
	}
	return false
}

func flexValues(value string) (grow, shrink, basis string, err error) {
	if global(value) {
		return value, value, value, nil
	}
	switch value {
	case "none":
		return "0", "0", "auto", nil
	case "auto":
		return "1", "1", "auto", nil
	}
	fields := strings.Fields(value)
	grow, shrink, basis = "1", "1", "0pt"
	if len(fields) == 0 || len(fields) > 3 {
		return "", "", "", fmt.Errorf("invalid flex shorthand")
	}
	numbers, hasBasis := 0, false
	for _, f := range fields {
		if global(f) {
			return "", "", "", fmt.Errorf("global flex value must stand alone")
		}
		if _, e := strconv.ParseFloat(f, 64); e == nil && numbers < 2 && !hasBasis {
			if numbers == 0 {
				grow = f
			} else {
				shrink = f
			}
			numbers++
		} else if !hasBasis {
			basis, hasBasis = f, true
		} else {
			return "", "", "", fmt.Errorf("invalid flex shorthand")
		}
	}
	return grow, shrink, basis, nil
}

func pageSize(raw string) (float64, float64, error) {
	fields := strings.Fields(raw)
	landscape := false
	if len(fields) > 1 && keyword(fields[len(fields)-1], "portrait", "landscape") {
		landscape = fields[len(fields)-1] == "landscape"
		fields = fields[:len(fields)-1]
	}
	var w, h float64
	if len(fields) == 1 {
		switch fields[0] {
		case "a4":
			w, h = 595.2756, 841.8898
		case "letter":
			w, h = 612, 792
		default:
			return 0, 0, fmt.Errorf("unsupported page size")
		}
	} else if len(fields) == 2 {
		var e error
		w, e = absolute(fields[0])
		if e != nil {
			return 0, 0, e
		}
		h, e = absolute(fields[1])
		if e != nil {
			return 0, 0, e
		}
	} else {
		return 0, 0, fmt.Errorf("unsupported page size")
	}
	if w <= 0 || h <= 0 || w > 14400 || h > 14400 {
		return 0, 0, fmt.Errorf("invalid page size")
	}
	if landscape && w < h {
		w, h = h, w
	}
	return w, h, nil
}

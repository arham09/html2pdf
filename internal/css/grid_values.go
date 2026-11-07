package css

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/arham09/html2pdf/internal/style"
)

func gridTracks(raw string) ([]style.Length, error) {
	if raw == "none" {
		return nil, nil
	}
	fields := strings.Fields(raw)
	if len(fields) == 0 || len(fields) > style.MaxGridColumns {
		return nil, fmt.Errorf("grid requires 1..%d explicit columns", style.MaxGridColumns)
	}
	tracks := make([]style.Length, 0, len(fields))
	for _, f := range fields {
		var l style.Length
		if strings.HasSuffix(f, "fr") {
			n, err := strconv.ParseFloat(strings.TrimSuffix(f, "fr"), 64)
			if err != nil || !finite(n) || n < 0 || n > 1e6 {
				return nil, fmt.Errorf("invalid fractional track")
			}
			l = style.Length{Value: n, Unit: style.Fraction}
		} else {
			var err error
			l, err = length(f, false, false)
			if err != nil {
				return nil, fmt.Errorf("unsupported Grid track")
			}
		}
		tracks = append(tracks, l)
	}
	return tracks, nil
}

// Only positive unnamed lines and one span are supported. There are no implicit
// columns, negative indices, named lines or browser-style overlap in this profile.
func gridPlacement(raw string, limit int) (style.GridPlacement, error) {
	p := style.GridPlacement{Span: 1}
	parts := strings.Split(raw, "/")
	if len(parts) > 2 {
		return p, fmt.Errorf("invalid Grid placement")
	}
	left := strings.Fields(strings.TrimSpace(parts[0]))
	integer := func(s string) (int, error) {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > limit+1 {
			return 0, fmt.Errorf("grid line outside profile")
		}
		return n, nil
	}
	if len(left) == 2 && left[0] == "span" && len(parts) == 1 {
		n, err := integer(left[1])
		if err != nil || n > limit {
			return p, fmt.Errorf("invalid Grid span")
		}
		p.Span = n
		return p, nil
	}
	if len(left) != 1 {
		return p, fmt.Errorf("invalid Grid start")
	}
	if left[0] != "auto" {
		n, err := integer(left[0])
		if err != nil {
			return p, err
		}
		p.Start = n
	}
	if len(parts) == 2 {
		right := strings.Fields(strings.TrimSpace(parts[1]))
		if len(right) == 2 && right[0] == "span" {
			n, err := integer(right[1])
			if err != nil || n > limit {
				return p, fmt.Errorf("invalid Grid span")
			}
			p.Span = n
		} else if len(right) == 1 && right[0] == "auto" {
			// Default one-track extent.
		} else if len(right) == 1 && p.Start > 0 {
			n, err := integer(right[0])
			if err != nil || n <= p.Start {
				return p, fmt.Errorf("grid end must follow start")
			}
			p.Span = n - p.Start
		} else {
			return p, fmt.Errorf("unsupported grid end")
		}
	}
	if p.Start > 0 && p.Start+p.Span > limit+1 {
		return p, fmt.Errorf("grid extent outside profile")
	}
	return p, nil
}

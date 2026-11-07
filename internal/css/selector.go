package css

import (
	"fmt"
	"strings"
	"unicode"

	dom "github.com/arham09/html2pdf/internal/html"
	"github.com/arham09/html2pdf/internal/session"
)

type specificity [3]int

func (a specificity) less(b specificity) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

type compound struct {
	tag          string
	ids, classes []string
}
type selector struct {
	parts       []compound
	relations   []byte
	specificity specificity
}

func parseSelector(text string) (selector, error) {
	var out selector
	text = strings.TrimSpace(text)
	for len(text) > 0 {
		var part compound
		if text[0] == '*' {
			part.tag = "*"
			text = text[1:]
		} else if text[0] != '.' && text[0] != '#' {
			id, rest := identifier(text)
			if id == "" {
				return out, fmt.Errorf("unsupported selector")
			}
			part.tag = strings.ToLower(id)
			text = rest
			out.specificity[2]++
		}
		for len(text) > 0 && (text[0] == '.' || text[0] == '#') {
			kind := text[0]
			id, rest := identifier(text[1:])
			if id == "" {
				return out, fmt.Errorf("invalid selector identifier")
			}
			text = rest
			if kind == '#' {
				part.ids = append(part.ids, id)
				out.specificity[0]++
			} else {
				part.classes = append(part.classes, id)
				out.specificity[1]++
			}
		}
		if part.tag == "" && len(part.ids)+len(part.classes) == 0 {
			return out, fmt.Errorf("empty selector")
		}
		out.parts = append(out.parts, part)
		if len(out.parts) > 64 {
			return out, fmt.Errorf("selector too long")
		}
		trimmed := strings.TrimLeftFunc(text, unicode.IsSpace)
		spaced := len(trimmed) != len(text)
		text = trimmed
		if text == "" {
			break
		}
		relation := byte(' ')
		if text[0] == '>' {
			relation = '>'
			text = strings.TrimSpace(text[1:])
			if text == "" {
				return out, fmt.Errorf("trailing combinator")
			}
		} else if !spaced {
			return out, fmt.Errorf("unsupported selector syntax")
		}
		out.relations = append(out.relations, relation)
	}
	if len(out.parts) == 0 {
		return out, fmt.Errorf("empty selector")
	}
	return out, nil
}

func identifier(text string) (string, string) {
	end := 0
	for i, r := range text {
		if !(unicode.IsLetter(r) || r == '_' || r == '-' || (i > 0 && unicode.IsDigit(r))) {
			break
		}
		end = i + len(string(r))
	}
	return text[:end], text[end:]
}

func matchCompound(s *session.Session, c compound, n *dom.Node) (bool, error) {
	if err := s.Charge("layout_work", 1); err != nil {
		return false, err
	}
	if n == nil || n.Tag == "" {
		return false, nil
	}
	if c.tag != "" && c.tag != "*" && c.tag != n.Tag {
		return false, nil
	}
	for _, id := range c.ids {
		if err := s.Charge("layout_work", 1); err != nil {
			return false, err
		}
		if n.Attributes["id"] != id {
			return false, nil
		}
	}
	for _, class := range c.classes {
		if err := s.Charge("layout_work", 1); err != nil {
			return false, err
		}
		if !n.Classes[class] {
			return false, nil
		}
	}
	return true, nil
}

func (sel selector) matches(s *session.Session, n *dom.Node) (bool, error) {
	if len(sel.parts) == 1 {
		return matchCompound(s, sel.parts[0], n)
	}
	// Dynamic programming avoids exponential ancestor backtracking for repeated
	// descendant compounds while still handling mixed child/descendant chains.
	ancestors := []*dom.Node{}
	for p := n; p != nil; p = p.Parent {
		if err := s.Charge("layout_work", 1); err != nil {
			return false, err
		}
		ancestors = append(ancestors, p)
	}
	possible := make([]bool, len(ancestors))
	ok, err := matchCompound(s, sel.parts[len(sel.parts)-1], n)
	if err != nil || !ok {
		return ok, err
	}
	possible[0] = true
	for k := len(sel.parts) - 2; k >= 0; k-- {
		next := make([]bool, len(ancestors))
		reachable := false
		for i := 1; i < len(ancestors); i++ {
			if err := s.Charge("layout_work", 1); err != nil {
				return false, err
			}
			reachable = reachable || possible[i-1]
			allowed := reachable
			if sel.relations[k] == '>' {
				allowed = possible[i-1]
			}
			if allowed {
				next[i], err = matchCompound(s, sel.parts[k], ancestors[i])
				if err != nil {
					return false, err
				}
			}
		}
		possible = next
	}
	for _, v := range possible {
		if v {
			return true, nil
		}
	}
	return false, nil
}

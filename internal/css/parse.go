// Package css parses a bounded print-oriented CSS profile and computes styles.
package css

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/arham09/html2pdf/internal/session"
	"github.com/tdewolff/parse/v2"
	syntax "github.com/tdewolff/parse/v2/css"
)

type declaration struct {
	name, value string
	important   bool
	order       int
}
type rule struct {
	selectors    []selector
	declarations []declaration
}
type Page struct {
	WidthPt, HeightPt *float64
	Margins           [4]*float64
}
type Sheet struct {
	rules []rule
	page  []declaration
	order int
}

// Clone shares immutable parsed rules while keeping appended inline/page/order
// state private to a decoration document.
func (sheet *Sheet) Clone() Sheet {
	return Sheet{rules: append([]rule(nil), sheet.rules...), page: append([]declaration(nil), sheet.page...), order: sheet.order}
}

type parser struct {
	session *session.Session
	strict  bool
	sheet   *Sheet
}

func diagnostic(s *session.Session, strict bool, code string, source uint64, critical bool) error {
	if strict || critical {
		return fmt.Errorf("%w: %s (source %d)", session.ErrUnsupportedFeature, code, source)
	}
	return s.Warn(session.Warning{Code: code, Message: "Outside supported CSS profile", SourceID: source})
}

func (sheet *Sheet) Add(s *session.Session, text string, strict bool) error {
	if err := s.Charge("stylesheets", 1); err != nil {
		return err
	}
	return (&parser{s, strict, sheet}).parse(text, false, 0, nil)
}

func (p *parser) parse(text string, inline bool, source uint64, into *[]declaration) error {
	if err := p.session.Charge("css_bytes", int64(len(text))); err != nil {
		return err
	}
	if !utf8.ValidString(text) {
		return fmt.Errorf("%w: CSS must be UTF-8", session.ErrInvalidInput)
	}
	// Bound lexical nesting before the grammar parser buffers nested values.
	lexer := syntax.NewLexer(parse.NewInputString(text))
	depth := int64(0)
	for {
		if err := p.session.Charge("layout_work", 1); err != nil {
			return err
		}
		kind, data := lexer.Next()
		if kind == syntax.ErrorToken {
			break
		}
		if inline && kind == syntax.AtKeywordToken {
			return diagnostic(p.session, p.strict, "unsupported-inline-at-rule", source, false)
		}
		if kind == syntax.FunctionToken || string(data) == "{" || string(data) == "(" || string(data) == "[" {
			depth++
			if err := p.session.CheckSize("dom_depth", depth); err != nil {
				return err
			}
		}
		if string(data) == "}" || string(data) == ")" || string(data) == "]" {
			depth--
			if depth < 0 {
				depth = 0
			}
		}
	}
	grammar := syntax.NewParser(parse.NewInputString(text), inline)
	type frame struct{ active, page bool }
	frames := []frame{{active: true}}
	var target *[]declaration
	if inline {
		target = into
	}
	for {
		if err := p.session.Charge("layout_work", 1); err != nil {
			return err
		}
		kind, _, data := grammar.Next()
		if kind == syntax.ErrorGrammar {
			if grammar.HasParseError() {
				if err := diagnostic(p.session, p.strict, "malformed-css", source, false); err != nil {
					return err
				}
				continue
			}
			if grammar.Err() != io.EOF {
				return fmt.Errorf("%w: CSS read: %w", session.ErrInvalidInput, grammar.Err())
			}
			break
		}
		var value strings.Builder
		for _, token := range grammar.Values() {
			value.Write(token.Data)
		}
		prelude := strings.TrimSpace(value.String())
		current := frames[len(frames)-1]
		switch kind {
		case syntax.BeginAtRuleGrammar:
			name := strings.ToLower(string(data))
			active := false
			page := false
			if current.active {
				switch name {
				case "@media":
					for _, part := range strings.Split(strings.ToLower(prelude), ",") {
						part = strings.TrimSpace(part)
						if part == "all" || part == "print" {
							active = true
						} else if part != "screen" {
							if err := diagnostic(p.session, p.strict, "unsupported-media", source, false); err != nil {
								return err
							}
						}
					}
				case "@page":
					if !current.page && prelude == "" {
						active = true
						page = true
					} else {
						if err := diagnostic(p.session, p.strict, "unsupported-page-selector", source, false); err != nil {
							return err
						}
					}
				default:
					if err := diagnostic(p.session, p.strict, "unsupported-at-rule", source, false); err != nil {
						return err
					}
				}
			}
			frames = append(frames, frame{active, page})
			target = nil
			if page {
				target = &p.sheet.page
			}
		case syntax.EndAtRuleGrammar:
			if len(frames) > 1 {
				frames = frames[:len(frames)-1]
			}
			target = nil
		case syntax.AtRuleGrammar:
			if current.active {
				if err := diagnostic(p.session, p.strict, "unsupported-at-rule", source, false); err != nil {
					return err
				}
			}
		case syntax.BeginRulesetGrammar:
			target = nil
			if current.active && !current.page {
				selectors := []selector{}
				valid := true
				for _, raw := range strings.Split(prelude, ",") {
					sel, err := parseSelector(raw)
					if err != nil {
						valid = false
						break
					}
					selectors = append(selectors, sel)
				}
				if !valid {
					if err := diagnostic(p.session, p.strict, "unsupported-selector", source, false); err != nil {
						return err
					}
				} else {
					p.sheet.rules = append(p.sheet.rules, rule{selectors: selectors})
					target = &p.sheet.rules[len(p.sheet.rules)-1].declarations
				}
			}
		case syntax.EndRulesetGrammar:
			target = nil
		case syntax.DeclarationGrammar:
			if !current.active || target == nil {
				continue
			}
			name := strings.ToLower(string(data))
			raw := strings.TrimSpace(prelude)
			important := false
			// Use tokenizer tokens, not suffix searches inside quoted strings.
			tokens := grammar.Values()
			end := len(tokens) - 1
			for end >= 0 && tokens[end].TokenType == syntax.WhitespaceToken {
				end--
			}
			if end >= 0 && tokens[end].TokenType == syntax.IdentToken && strings.EqualFold(string(tokens[end].Data), "important") {
				previous := end - 1
				for previous >= 0 && tokens[previous].TokenType == syntax.WhitespaceToken {
					previous--
				}
				if previous >= 0 && string(tokens[previous].Data) == "!" {
					important = true
					var b strings.Builder
					for _, v := range tokens[:previous] {
						b.Write(v.Data)
					}
					raw = strings.TrimSpace(b.String())
				}
			}
			expanded, err := expand(name, raw, current.page)
			if err != nil {
				critical := name == "display" || name == "position" || name == "float" || name == "flex" || strings.HasPrefix(name, "flex-") || strings.HasPrefix(name, "align-") || strings.HasPrefix(name, "justify-") || name == "grid" || strings.HasPrefix(name, "grid-") || name == "order" || name == "gap" || strings.HasSuffix(name, "-gap") || name == "min-width" || name == "min-height" || name == "max-width" || name == "max-height"
				if e := diagnostic(p.session, p.strict, "unsupported-css-"+name, source, critical); e != nil {
					return e
				}
				continue
			}
			for _, d := range expanded {
				p.sheet.order++
				d.important = important
				d.order = p.sheet.order
				*target = append(*target, d)
			}
		case syntax.CustomPropertyGrammar, syntax.QualifiedRuleGrammar, syntax.TokenGrammar:
			if current.active {
				if err := diagnostic(p.session, p.strict, "unsupported-css-syntax", source, false); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

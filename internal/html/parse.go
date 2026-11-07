// Package html normalizes bounded static HTML5 input without resource access.
package html

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/arham09/html2pdf/internal/session"
	"github.com/arham09/html2pdf/internal/style"
	nethtml "golang.org/x/net/html"
)

type Node struct {
	ID         uint64
	Tag, Text  string
	Attributes map[string]string
	Classes    map[string]bool
	Parent     *Node
	Children   []*Node
	Style      style.Computed
}
type Document struct {
	Root        *Node
	Nodes       []*Node
	Stylesheets []string
}

var void = words("area base br col embed hr img input link meta param source track wbr")
var supported = words("html body div section span p h1 h2 h3 h4 h5 h6 strong em br hr table thead tbody tr th td img ul ol li")
var suppressed = words("script iframe object embed template title meta base link")

func words(s string) map[string]bool {
	m := make(map[string]bool)
	for _, v := range strings.Fields(s) {
		m[v] = true
	}
	return m
}

func Parse(s *session.Session, input string) (*Document, error) {
	if err := s.Charge("html_bytes", int64(len(input))); err != nil {
		return nil, err
	}
	if !utf8.ValidString(input) {
		return nil, fmt.Errorf("%w: HTML must be UTF-8", session.ErrInvalidInput)
	}
	// Preflight the tokenizer before the tree builder allocates normalized nodes.
	// Reserve conservatively for implied/reconstructed elements as nesting grows.
	z := nethtml.NewTokenizer(strings.NewReader(input))
	z.SetMaxBuf(int(min(s.Limits.MaxHTMLBytes+1, 1<<30)))
	stack := []string{}
	reserve := int64(4)
	for {
		if err := s.Charge("layout_work", 1); err != nil {
			return nil, err
		}
		tt := z.Next()
		if tt == nethtml.ErrorToken {
			if z.Err() == io.EOF {
				break
			}
			return nil, fmt.Errorf("%w: HTML tokenization: %w", session.ErrInvalidInput, z.Err())
		}
		reserve += int64(len(stack) + 1)
		if err := s.CheckSize("dom_nodes", reserve); err != nil {
			return nil, err
		}
		if tt == nethtml.StartTagToken || tt == nethtml.SelfClosingTagToken {
			name, _ := z.TagName()
			tag := string(name)
			if tt == nethtml.StartTagToken && !void[tag] {
				stack = append(stack, tag)
			}
			if err := s.CheckSize("dom_depth", int64(len(stack))); err != nil {
				return nil, err
			}
		} else if tt == nethtml.EndTagToken {
			name, _ := z.TagName()
			for i := len(stack) - 1; i >= 0; i-- {
				if stack[i] == string(name) {
					stack = stack[:i]
					break
				}
			}
		}
	}
	parsed, err := nethtml.ParseWithOptions(strings.NewReader(input), nethtml.ParseOptionEnableScripting(false))
	if err != nil {
		return nil, fmt.Errorf("%w: HTML parse: %w", session.ErrInvalidInput, err)
	}
	doc := &Document{}
	var visit func(*nethtml.Node, *Node, int, bool) error
	visit = func(raw *nethtml.Node, parent *Node, depth int, hidden bool) error {
		if err := s.Charge("dom_nodes", 1); err != nil {
			return err
		}
		source := s.NextSourceID()
		if err := s.Charge("layout_work", 1); err != nil {
			return err
		}
		if err := s.CheckSize("dom_depth", int64(depth)); err != nil {
			return err
		}
		tag := raw.Data
		if raw.Type == nethtml.ElementNode {
			if tag == "style" && !hidden {
				var css strings.Builder
				for c := raw.FirstChild; c != nil; c = c.NextSibling {
					css.WriteString(c.Data)
				}
				// Honor the style element's simple media attribute without parsing screen CSS.
				media := ""
				for _, a := range raw.Attr {
					if a.Key == "media" {
						media = strings.ToLower(strings.TrimSpace(a.Val))
					}
				}
				text := css.String()
				if media != "" {
					text = "@media " + media + " {" + text + "}"
				}
				doc.Stylesheets = append(doc.Stylesheets, text)
				return nil
			}
			if tag == "link" && !hidden {
				for _, a := range raw.Attr {
					if a.Key == "rel" && strings.EqualFold(a.Val, "stylesheet") {
						return fmt.Errorf("%w: linked stylesheets are not loaded; supply Stylesheets", session.ErrUnsupportedFeature)
					}
				}
			}
			if suppressed[tag] || raw.Namespace != "" {
				return nil
			}
			if tag == "head" {
				for c := raw.FirstChild; c != nil; c = c.NextSibling {
					if c.Type == nethtml.ElementNode && (c.Data == "style" || c.Data == "link") {
						if err := visit(c, parent, depth+1, false); err != nil {
							return err
						}
					}
				}
				return nil
			}
			if !supported[tag] {
				if err := s.Warn(session.Warning{Code: "unsupported-element", Message: "Unsupported static wrapper preserved", SourceID: source}); err != nil {
					return err
				}
			}
		}
		// Document/comment/doctype nodes never paint. Keep an explicit document root.
		if raw.Type != nethtml.ElementNode && raw.Type != nethtml.TextNode && raw.Type != nethtml.DocumentNode {
			return nil
		}
		n := &Node{ID: source, Parent: parent, Attributes: make(map[string]string)}
		if raw.Type == nethtml.TextNode {
			n.Text = raw.Data
		} else if raw.Type == nethtml.ElementNode {
			n.Tag = tag
		}
		for _, a := range raw.Attr {
			if a.Namespace == "" && !strings.HasPrefix(strings.ToLower(a.Key), "on") {
				if _, exists := n.Attributes[a.Key]; !exists {
					n.Attributes[a.Key] = a.Val
				}
			}
		}
		n.Classes = make(map[string]bool)
		for _, class := range strings.Fields(n.Attributes["class"]) {
			if err := s.Charge("layout_work", 1); err != nil {
				return err
			}
			n.Classes[class] = true
		}
		doc.Nodes = append(doc.Nodes, n)
		if parent != nil {
			parent.Children = append(parent.Children, n)
		} else {
			doc.Root = n
		}
		if n.Tag == "tr" {
			if err := s.Charge("table_rows", 1); err != nil {
				return err
			}
		}
		if n.Tag == "td" || n.Tag == "th" {
			if err := s.Charge("table_cells", 1); err != nil {
				return err
			}
		}
		for c := raw.FirstChild; c != nil; c = c.NextSibling {
			if err := visit(c, n, depth+1, hidden); err != nil {
				return err
			}
		}
		return nil
	}
	// Collect head styles, but exclude its non-content text from the output tree.
	// The hidden flag suppresses active/template content, not ordinary head CSS.
	if err := visit(parsed, nil, 0, false); err != nil {
		return nil, err
	}
	return doc, nil
}

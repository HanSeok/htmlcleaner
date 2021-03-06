package htmlcleaner

import (
	"bytes"
	"io"
	"net/url"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// DefaultMaxDepth is the default maximum depth of the node trees returned by
// Parse.
const DefaultMaxDepth = 100

// Preprocess escapes disallowed tags in a cleaner way, but does not fix
// nesting problems. Use with Clean.
func Preprocess(config *Config, fragment string) string {
	if config == nil {
		config = DefaultConfig
	}

	var buf bytes.Buffer
	write := func(raw string) {
		_, err := buf.WriteString(raw)

		// The only possible error is running out of memory.
		expectError(err, nil)
	}

	t := html.NewTokenizer(strings.NewReader(fragment))
	for {
		switch tok := t.Next(); tok {
		case html.ErrorToken:
			err := t.Err()

			// The only possible errors are from the Reader or from
			// the buffer capacity being exceeded. Neither can
			// happen with strings.NewReader as the string must
			// already fit into memory.
			expectError(err, io.EOF)

			if err == io.EOF {
				write(html.EscapeString(string(t.Raw())))
				return buf.String()
			}
		case html.TextToken:
			write(string(t.Raw()))
		case html.StartTagToken, html.EndTagToken, html.SelfClosingTagToken:
			raw := string(t.Raw())
			tagName, _ := t.TagName()
			allowed := false
			if tag := atom.Lookup(tagName); tag != 0 {
				if _, ok := config.elem[tag]; ok {
					allowed = true
				}
			}
			if !allowed {
				if _, ok := config.elemCustom[string(tagName)]; ok {
					allowed = true
				}
			}
			if !allowed {
				raw = html.EscapeString(raw)
			}
			write(raw)
		case html.CommentToken:
			raw := string(t.Raw())
			if config.EscapeComments || !strings.HasPrefix(raw, "<!--") || !strings.HasSuffix(raw, "-->") {
				raw = html.EscapeString(raw)
			}
			write(raw)
		default:
			write(html.EscapeString(string(t.Raw())))
		}
	}
}

// Parse is a convenience wrapper that calls ParseDepth with DefaultMaxDepth.
func Parse(fragment string) []*html.Node {
	return ParseDepth(fragment, DefaultMaxDepth)
}

// ParseDepth is a convenience function that wraps html.ParseFragment but takes
// a string instead of an io.Reader and omits deep trees.
func ParseDepth(fragment string, maxDepth int) []*html.Node {
	nodes, err := html.ParseFragment(strings.NewReader(fragment), &html.Node{
		Type:     html.ElementNode,
		Data:     "div",
		DataAtom: atom.Div,
	})
	expectError(err, nil)

	if maxDepth > 0 {
		for _, n := range nodes {
			forceMaxDepth(n, maxDepth)
		}
	}

	return nodes
}

// Render is a convenience function that wraps html.Render and renders to a
// string instead of an io.Writer.
func Render(nodes ...*html.Node) string {
	var buf bytes.Buffer

	for _, n := range nodes {
		err := html.Render(&buf, n)
		expectError(err, nil)
	}

	return string(buf.Bytes())
}

// Clean a fragment of HTML using the specified Config, or the DefaultConfig
// if it is nil.
func Clean(c *Config, fragment string) string {
	return Render(CleanNodes(c, Parse(fragment))...)
}

var isBlockElement = map[atom.Atom]bool{
	0:               true, // custom elements are not wrapped
	atom.Address:    true,
	atom.Article:    true,
	atom.Aside:      true,
	atom.Blockquote: true,
	atom.Center:     true,
	atom.Dd:         true,
	atom.Details:    true,
	atom.Dir:        true,
	atom.Div:        true,
	atom.Dl:         true,
	atom.Dt:         true,
	atom.Fieldset:   true,
	atom.Figcaption: true,
	atom.Figure:     true,
	atom.Footer:     true,
	atom.Form:       true,
	atom.H1:         true,
	atom.H2:         true,
	atom.H3:         true,
	atom.H4:         true,
	atom.H5:         true,
	atom.H6:         true,
	atom.Header:     true,
	atom.Hgroup:     true,
	atom.Hr:         true,
	atom.Li:         true,
	atom.Listing:    true,
	atom.Menu:       true,
	atom.Nav:        true,
	atom.Ol:         true,
	atom.P:          true,
	atom.Plaintext:  true,
	atom.Pre:        true,
	atom.Section:    true,
	atom.Summary:    true,
	atom.Table:      true,
	atom.Ul:         true,
}

// CleanNodes calls CleanNode on each node, and additionally wraps inline
// elements in <p> tags and wraps dangling <li> tags in <ul> tags.
func CleanNodes(c *Config, nodes []*html.Node) []*html.Node {
	return cleanNodes(c, deepCopyAll(nodes))
}

func deepCopyAll(nodes []*html.Node) []*html.Node {
	clone := make([]*html.Node, len(nodes))
	for i, n := range nodes {
		clone[i] = deepCopy(n)
	}
	return clone
}

func deepCopy(n *html.Node) *html.Node {
	clone := &html.Node{
		Type:      n.Type,
		Attr:      make([]html.Attribute, len(n.Attr)),
		Namespace: n.Namespace,
		Data:      n.Data,
		DataAtom:  n.DataAtom,
	}
	copy(clone.Attr, n.Attr)
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		clone.AppendChild(deepCopy(c))
	}
	return clone
}

func cleanNodes(c *Config, nodes []*html.Node) []*html.Node {
	if c == nil {
		c = DefaultConfig
	}

	for i, n := range nodes {
		nodes[i] = filterNode(c, n)
		if nodes[i].DataAtom == atom.Li {
			wrapper := &html.Node{
				Type:     html.ElementNode,
				Data:     "ul",
				DataAtom: atom.Ul,
			}
			wrapper.AppendChild(nodes[i])
			nodes[i] = wrapper
		}
	}

	if c.WrapText {
		nodes = wrapText(nodes)
	}

	return nodes
}

func wrapText(nodes []*html.Node) []*html.Node {
	wrapped := make([]*html.Node, 0, len(nodes))
	var wrapper *html.Node
	appendWrapper := func() {
		if wrapper != nil {
			// render and re-parse so p-inline-p expands
			wrapped = append(wrapped, ParseDepth(Render(wrapper), 0)...)
			wrapper = nil
		}
	}
	for _, n := range nodes {
		if n.Type == html.ElementNode && isBlockElement[n.DataAtom] {
			appendWrapper()
			wrapped = append(wrapped, n)
			continue
		}
		if wrapper == nil && n.Type == html.TextNode && strings.TrimSpace(n.Data) == "" {
			wrapped = append(wrapped, n)
			continue
		}
		if wrapper == nil {
			wrapper = &html.Node{
				Type:     html.ElementNode,
				Data:     "p",
				DataAtom: atom.P,
			}
		}

		wrapper.AppendChild(n)
	}
	appendWrapper()
	return wrapped
}

func text(s string) *html.Node {
	return &html.Node{Type: html.TextNode, Data: s}
}

// CleanNode cleans an HTML node using the specified config. Text nodes are
// returned as-is. Element nodes are recursively  checked for legality and have
// their attributes checked for legality as well. Elements with illegal
// attributes are copied and the problematic attributes are removed. Elements
// that are not in the set of legal elements are replaced with a textual
// version of their source code.
func CleanNode(c *Config, n *html.Node) *html.Node {
	if c == nil {
		c = DefaultConfig
	}
	return filterNode(c, deepCopy(n))
}

func filterNode(c *Config, n *html.Node) *html.Node {
	if n.Type == html.TextNode {
		return n
	}
	if n.Type == html.CommentNode && !c.EscapeComments {
		return n
	}
	if n.Type != html.ElementNode {
		return text(Render(n))
	}
	return cleanNode(c, n)
}

func cleanNode(c *Config, n *html.Node) *html.Node {
	allowedAttr, ok1 := c.elem[n.DataAtom]
	customAttr, ok2 := c.elemCustom[n.Data]
	if ok1 || ok2 {
		cleanChildren(c, n)

		haveSrc := false

		attrs := n.Attr
		n.Attr = make([]html.Attribute, 0, len(attrs))
		for _, attr := range attrs {
			a := atom.Lookup([]byte(attr.Key))

			re1, ok1 := allowedAttr[a]
			re2, ok2 := customAttr[attr.Key]
			_, ok3 := c.attr[a]
			_, ok4 := c.attrCustom[attr.Key]

			if attr.Namespace != "" || (!ok1 && !ok2 && !ok3 && !ok4) {
				continue
			}

			if !cleanURL(c, a, &attr) {
				continue
			}

			if re1 != nil && !re1.MatchString(attr.Val) {
				continue
			}
			if re2 != nil && !re2.MatchString(attr.Val) {
				continue
			}

			haveSrc = haveSrc || a == atom.Src

			n.Attr = append(n.Attr, attr)
		}

		if n.DataAtom == atom.Img && !haveSrc {
			// replace it with an empty text node
			return &html.Node{Type: html.TextNode}
		}

		return n
	}
	return text(html.UnescapeString(Render(n)))
}

var allowedURLSchemes = map[string]bool{
	"http":   true,
	"https":  true,
	"mailto": true,
	"data":   true,
	"":       true,
}

// SafeURLScheme returns true if u.Scheme is http, https, mailto, data, or an
// empty string.
func SafeURLScheme(u *url.URL) bool {
	return allowedURLSchemes[u.Scheme]
}

func cleanURL(c *Config, a atom.Atom, attr *html.Attribute) bool {
	if a != atom.Href && a != atom.Src && a != atom.Poster {
		return true
	}

	u, err := url.Parse(attr.Val)
	if err != nil {
		return false
	}
	if c.ValidateURL != nil && !c.ValidateURL(u) {
		return false
	}
	attr.Val = u.String()
	return true
}

func cleanChildren(c *Config, parent *html.Node) {
	var children []*html.Node
	for parent.FirstChild != nil {
		child := parent.FirstChild
		parent.RemoveChild(child)
		children = append(children, filterNode(c, child))
	}

	if c.WrapText {
		_, ok := c.wrap[parent.DataAtom]
		if !ok && parent.DataAtom == 0 {
			_, ok = c.wrapCustom[parent.Data]
		}
		if ok {
			children = wrapText(children)
		}
	}

	for _, child := range children {
		parent.AppendChild(child)
	}
}

func forceMaxDepth(n *html.Node, depth int) {
	if depth == 0 {
		n.Type = html.TextNode
		n.FirstChild, n.LastChild = nil, nil
		n.Attr = nil
		n.Data = "[omitted]"
		for n.NextSibling != nil {
			n.Parent.RemoveChild(n.NextSibling)
		}
		return
	}

	if n.Type != html.ElementNode {
		return
	}

	for c := n.FirstChild; c != nil; c = c.NextSibling {
		forceMaxDepth(c, depth-1)
	}
}

func expectError(err, expected error) {
	if err != expected {
		panic("htmlcleaner: unexpected error: " + err.Error())
	}
}

package main

import (
	"mime"
	"strings"
)

// prettyXML indents a well-formed XML body. Tags, prefixes, and attribute
// quoting are left as they arrived; only whitespace between nodes changes.
// JSON and HTML stay untouched so those bodies keep their own views.
func prettyXML(contentType, body string) string {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" || jsonDeclared(contentType) {
		return ""
	}
	if htmlDeclared(contentType) && !xmlDeclared(contentType) {
		return ""
	}
	if !xmlDeclared(contentType) && !looksLikeXML(trimmed) {
		return ""
	}
	nodes, err := parseMarkup(trimmed)
	if err != nil || !containsElem(nodes) {
		return ""
	}
	var b strings.Builder
	writeXMLNodes(nodes, 0, &b)
	return b.String()
}

func xmlDeclared(contentType string) bool {
	return mediaHas(contentType, "/xml", "+xml")
}

func htmlDeclared(contentType string) bool {
	return mediaIs(contentType, "text/html")
}

func mediaIs(contentType, want string) bool {
	mt, ok := mediaType(contentType)
	if !ok {
		return strings.Contains(strings.ToLower(contentType), want)
	}
	return mt == want
}

func mediaHas(contentType string, parts ...string) bool {
	mt, ok := mediaType(contentType)
	if !ok {
		low := strings.ToLower(contentType)
		for _, part := range parts {
			if strings.Contains(low, part) {
				return true
			}
		}
		return false
	}
	for _, part := range parts {
		if strings.Contains(mt, part) {
			return true
		}
	}
	return false
}

func mediaType(contentType string) (string, bool) {
	ct := strings.TrimSpace(contentType)
	if ct == "" {
		return "", false
	}
	mt, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return "", false
	}
	return strings.ToLower(mt), true
}

func looksLikeXML(s string) bool {
	if len(s) < 2 || s[0] != '<' {
		return false
	}
	c := s[1]
	return c == '!' || c == '?' || isNameStart(c)
}

func isNameStart(c byte) bool {
	return c == '_' || c == ':' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}

type xmlNode struct {
	kind     string
	raw      string
	closeRaw string
	name     string
	self     bool
	text     string
	children []xmlNode
}

func parseMarkup(src string) ([]xmlNode, error) {
	p := &markupParser{s: src}
	nodes, err := p.parse("")
	if err != nil {
		return nil, err
	}
	if p.i < len(p.s) && strings.TrimSpace(p.s[p.i:]) != "" {
		return nil, errMarkup
	}
	return nodes, nil
}

var errMarkup = errString("markup")

type errString string

func (e errString) Error() string { return string(e) }

type markupParser struct {
	s string
	i int
}

func (p *markupParser) parse(until string) ([]xmlNode, error) {
	var nodes []xmlNode
	for p.i < len(p.s) {
		if p.s[p.i] != '<' {
			j := strings.IndexByte(p.s[p.i:], '<')
			if j < 0 {
				j = len(p.s) - p.i
			}
			nodes = append(nodes, xmlNode{kind: "text", text: p.s[p.i : p.i+j]})
			p.i += j
			continue
		}
		start := p.i
		end, err := scanTag(p.s, p.i)
		if err != nil {
			return nil, err
		}
		raw := p.s[p.i:end]
		p.i = end
		switch {
		case strings.HasPrefix(raw, "<!--"):
			nodes = append(nodes, xmlNode{kind: "comment", raw: raw})
		case strings.HasPrefix(raw, "<![CDATA["):
			nodes = append(nodes, xmlNode{kind: "cdata", raw: raw})
		case strings.HasPrefix(raw, "<?"):
			nodes = append(nodes, xmlNode{kind: "pi", raw: raw})
		case strings.HasPrefix(raw, "<!"):
			nodes = append(nodes, xmlNode{kind: "decl", raw: raw})
		default:
			name, closing, self := tagInfo(raw)
			if name == "" {
				return nil, errMarkup
			}
			if closing {
				if until == "" || name != until {
					return nil, errMarkup
				}
				p.i = start
				return nodes, nil
			}
			node := xmlNode{kind: "elem", raw: raw, name: name, self: self}
			if !self {
				kids, err := p.parse(name)
				if err != nil {
					return nil, err
				}
				node.children = kids
				node.closeRaw, err = p.takeClose(name)
				if err != nil {
					return nil, err
				}
			}
			nodes = append(nodes, node)
		}
	}
	if until != "" {
		return nil, errMarkup
	}
	return nodes, nil
}

func (p *markupParser) takeClose(name string) (string, error) {
	if p.i >= len(p.s) || p.s[p.i] != '<' {
		return "", errMarkup
	}
	end, err := scanTag(p.s, p.i)
	if err != nil {
		return "", err
	}
	raw := p.s[p.i:end]
	got, closing, self := tagInfo(raw)
	if !closing || self || got != name {
		return "", errMarkup
	}
	p.i = end
	return raw, nil
}

func scanTag(s string, i int) (int, error) {
	if i >= len(s) || s[i] != '<' {
		return 0, errMarkup
	}
	if strings.HasPrefix(s[i:], "<!--") {
		end := strings.Index(s[i+4:], "-->")
		if end < 0 {
			return 0, errMarkup
		}
		return i + 4 + end + 3, nil
	}
	if strings.HasPrefix(s[i:], "<![CDATA[") {
		end := strings.Index(s[i+9:], "]]>")
		if end < 0 {
			return 0, errMarkup
		}
		return i + 9 + end + 3, nil
	}
	if strings.HasPrefix(s[i:], "<?") {
		end := strings.Index(s[i+2:], "?>")
		if end < 0 {
			return 0, errMarkup
		}
		return i + 2 + end + 2, nil
	}
	quote := byte(0)
	for j := i + 1; j < len(s); j++ {
		c := s[j]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '"' || c == '\'' {
			quote = c
			continue
		}
		if c == '>' {
			return j + 1, nil
		}
	}
	return 0, errMarkup
}

func tagInfo(raw string) (name string, closing, self bool) {
	if len(raw) < 3 || raw[0] != '<' || raw[len(raw)-1] != '>' {
		return "", false, false
	}
	self = strings.HasSuffix(raw, "/>")
	inner := raw[1 : len(raw)-1]
	if self {
		inner = strings.TrimSuffix(inner, "/")
	}
	if strings.HasPrefix(inner, "/") {
		closing = true
		inner = inner[1:]
	}
	inner = strings.TrimLeft(inner, " \t\r\n")
	end := 0
	for end < len(inner) {
		c := inner[end]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '/' {
			break
		}
		end++
	}
	return inner[:end], closing, self
}

func containsElem(nodes []xmlNode) bool {
	for _, n := range nodes {
		if n.kind == "elem" || containsElem(n.children) {
			return true
		}
	}
	return false
}

func visibleKids(nodes []xmlNode) []xmlNode {
	out := make([]xmlNode, 0, len(nodes))
	for _, n := range nodes {
		if n.kind == "text" && strings.TrimSpace(n.text) == "" {
			continue
		}
		out = append(out, n)
	}
	return out
}

func writeXMLNodes(nodes []xmlNode, depth int, b *strings.Builder) {
	first := true
	for _, n := range visibleKids(nodes) {
		if !first {
			b.WriteByte('\n')
		}
		first = false
		writeIndent(b, depth)
		writeXMLNode(n, depth, b)
	}
}

func writeXMLNode(n xmlNode, depth int, b *strings.Builder) {
	if n.kind != "elem" {
		if n.kind == "text" {
			b.WriteString(strings.TrimSpace(n.text))
			return
		}
		b.WriteString(n.raw)
		return
	}
	if n.self {
		b.WriteString(n.raw)
		return
	}
	kids := visibleKids(n.children)
	if len(kids) == 0 {
		b.WriteString(n.raw)
		b.WriteString(n.closeRaw)
		return
	}
	if len(kids) == 1 && kids[0].kind == "text" {
		b.WriteString(n.raw)
		b.WriteString(strings.TrimSpace(kids[0].text))
		b.WriteString(n.closeRaw)
		return
	}
	b.WriteString(n.raw)
	b.WriteByte('\n')
	writeXMLNodes(kids, depth+1, b)
	b.WriteByte('\n')
	writeIndent(b, depth)
	b.WriteString(n.closeRaw)
}

func writeIndent(b *strings.Builder, depth int) {
	for i := 0; i < depth; i++ {
		b.WriteString("  ")
	}
}

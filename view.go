package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"mime"
	"net"
	"strings"
	"time"

	"requestbin/internal/bin"
)

func viewFuncs() template.FuncMap {
	return template.FuncMap{
		"prettyJSON":  prettyJSON,
		"formatBody":  formatBody,
		"trimNL":      func(s string) string { return strings.TrimRight(s, "\n") },
		"rawHead":     rawHead,
		"statLabel":   statLabel,
		"bytesLabel":  bytesLabel,
		"methodClass": methodClass,
		"relWhen":     relWhen,
		"stamp":       stamp,
		"clientIP":    clientIP,
		"hookHead":    hookHead,
		"hookBody":    hookBody,
		"hookStat":    hookStat,
	}
}

func prettyJSON(contentType, body string) string {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return ""
	}
	if !jsonDeclared(contentType) && !looksLikeJSON(trimmed) {
		return ""
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, []byte(trimmed), "", "  "); err != nil {
		return ""
	}
	return buf.String()
}

func jsonDeclared(contentType string) bool {
	ct := strings.TrimSpace(contentType)
	if ct == "" {
		return false
	}
	mt, _, err := mime.ParseMediaType(ct)
	if err != nil {
		low := strings.ToLower(ct)
		return strings.Contains(low, "/json") || strings.Contains(low, "+json")
	}
	mt = strings.ToLower(mt)
	return mt == "application/json" || strings.HasSuffix(mt, "+json")
}

// bodyView is how a captured body is shown: pretty text when it can be
// formatted, and a highlight class for JSON or XML.
type bodyView struct {
	Pretty string
	Kind   string
	Label  string
	Class  string
}

func formatBody(contentType, body string) bodyView {
	if strings.TrimSpace(body) == "" {
		return bodyView{}
	}
	if pretty := prettyJSON(contentType, body); pretty != "" {
		return bodyView{Pretty: pretty, Kind: "json", Label: "Pretty JSON", Class: "json"}
	}
	if jsonDeclared(contentType) {
		return bodyView{}
	}
	if htmlDeclared(contentType) && !xmlDeclared(contentType) {
		return bodyView{}
	}
	if pretty := prettyXML(contentType, body); pretty != "" {
		return bodyView{Pretty: pretty, Kind: "xml", Label: "Pretty XML", Class: "xml"}
	}
	trimmed := strings.TrimSpace(body)
	if xmlDeclared(contentType) || looksLikeXML(trimmed) {
		return bodyView{Kind: "xml", Class: "xml"}
	}
	return bodyView{}
}

func looksLikeJSON(s string) bool {
	if s == "" {
		return false
	}
	switch s[0] {
	case '{', '[', '"', 't', 'f', 'n', '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		return true
	default:
		return false
	}
}

func rawHead(method, path string, headers []bin.Field) string {
	if strings.TrimSpace(path) == "" {
		path = "/"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s HTTP/1.1\n", method, path)
	for _, h := range headers {
		fmt.Fprintf(&b, "%s: %s\n", h.Name, h.Value)
	}
	b.WriteByte('\n')
	return b.String()
}

func measureText(s string) (lines, n int) {
	n = len(s)
	if s == "" {
		return 0, 0
	}
	lines = strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		lines++
	}
	if lines < 1 {
		lines = 1
	}
	return lines, n
}

func statLabel(s string) string {
	lines, n := measureText(s)
	lineWord := "lines"
	if lines == 1 {
		lineWord = "line"
	}
	byteWord := "bytes"
	if n == 1 {
		byteWord = "byte"
	}
	return fmt.Sprintf("%d %s · %d %s", lines, lineWord, n, byteWord)
}

func bytesLabel(n int) string {
	return fmt.Sprintf("%d B", n)
}

func methodClass(method string) string {
	switch strings.ToUpper(strings.TrimSpace(method)) {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
		return "method method-" + strings.ToUpper(strings.TrimSpace(method))
	default:
		return "method method-OTHER"
	}
}

func relWhen(ts string) string {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ts
	}
	d := time.Since(t)
	if d < 0 {
		d = 0
	}
	if d < 45*time.Second {
		return "just now"
	}
	if d < time.Hour {
		m := int(d.Round(time.Minute) / time.Minute)
		if m < 1 {
			m = 1
		}
		if m == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", m)
	}
	if d < 24*time.Hour {
		h := int(d.Round(time.Hour) / time.Hour)
		if h < 1 {
			h = 1
		}
		if h == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", h)
	}
	days := int(d.Round(24*time.Hour) / (24 * time.Hour))
	if days < 1 {
		days = 1
	}
	if days == 1 {
		return "1 day ago"
	}
	return fmt.Sprintf("%d days ago", days)
}

func stamp(ts string) string {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ts
	}
	return t.Local().Format("Jan 02 · 15:04:05")
}

func clientIP(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return "unknown"
	}
	if strings.HasPrefix(addr, "[") {
		if i := strings.Index(addr, "]"); i > 1 {
			return addr[1:i]
		}
		return addr
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

// hookHead and hookBody are the status line, the Content-Type writeJSON sets,
// and the JSON body sent for a captured hook. Other response headers are omitted.
func hookHead() string {
	return "HTTP/1.1 200 OK\nContent-Type: application/json; charset=utf-8\n\n"
}

func hookBody(binID, reqID string) string {
	payload, err := json.Marshal(hookOK{OK: true, Bin: binID, Request: reqID})
	if err != nil {
		return ""
	}
	return string(payload) + "\n"
}

func hookStat(binID, reqID string) string {
	return statLabel(hookHead() + hookBody(binID, reqID))
}

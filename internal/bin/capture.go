package bin

import (
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

func buildRequest(id string, r *http.Request) (Request, error) {
	raw, err := readBody(r)
	if err != nil {
		return Request{}, err
	}
	reqID, err := newID()
	if err != nil {
		return Request{}, err
	}

	captured, truncated := truncateBody(raw)
	text := strings.ToValidUTF8(string(captured), "\uFFFD")
	if truncated {
		text += "\n[truncated to 1 MiB]"
	}

	contentType := ""
	if r.Header != nil {
		contentType = r.Header.Get("Content-Type")
	}

	return Request{
		ID:            reqID,
		Method:        r.Method,
		Path:          hookPath(id, requestURI(r)),
		Timestamp:     time.Now().UTC().Format(time.RFC3339),
		RemoteAddr:    r.RemoteAddr,
		ContentType:   contentType,
		ContentLength: len(captured),
		Headers:       flattenHeaders(r.Header),
		Query:         parseEncodedPairs(rawQuery(r)),
		Body:          text,
		Form:          parseForm(contentType, captured),
	}, nil
}

func readBody(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return []byte{}, nil
	}
	defer r.Body.Close()
	// Read one extra byte so a body that is exactly the cap is not marked truncated.
	return io.ReadAll(io.LimitReader(r.Body, MaxBodyBytes+1))
}

func truncateBody(raw []byte) ([]byte, bool) {
	if len(raw) > MaxBodyBytes {
		return raw[:MaxBodyBytes], true
	}
	return raw, false
}

func requestURI(r *http.Request) string {
	if r.RequestURI != "" {
		return r.RequestURI
	}
	if r.URL == nil {
		return ""
	}
	return r.URL.RequestURI()
}

func rawQuery(r *http.Request) string {
	if r.URL == nil {
		return ""
	}
	return r.URL.RawQuery
}

// hookPath strips the /hooks/{id} prefix from the raw request URI.
// /hooks/{id} becomes "/". /hooks/{id}/foo?x=1 becomes "/foo?x=1".
func hookPath(id, requestURI string) string {
	prefix := "/hooks/" + id
	if !strings.HasPrefix(requestURI, prefix) {
		if requestURI == "" {
			return "/"
		}
		return requestURI
	}
	rest := requestURI[len(prefix):]
	if rest == "" || strings.HasPrefix(rest, "?") {
		return "/" + rest
	}
	if strings.HasPrefix(rest, "/") {
		return rest
	}
	return requestURI
}

// flattenHeaders keeps every header value, including duplicates.
// net/http does not retain the on-the-wire order of distinct header names,
// so names are sorted and the original value order of each name is kept.
func flattenHeaders(h http.Header) []Field {
	if len(h) == 0 {
		return []Field{}
	}
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]Field, 0, len(h))
	for _, name := range keys {
		for _, value := range h[name] {
			out = append(out, Field{Name: name, Value: value})
		}
	}
	return out
}

// parseEncodedPairs preserves order and duplicate names in a query or form body.
func parseEncodedPairs(raw string) []Field {
	if raw == "" {
		return []Field{}
	}
	parts := strings.Split(raw, "&")
	out := make([]Field, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		key, value, hasValue := strings.Cut(part, "=")
		key = unescapeQuery(key)
		if hasValue {
			value = unescapeQuery(value)
		} else {
			value = ""
		}
		out = append(out, Field{Name: key, Value: value})
	}
	return out
}

func unescapeQuery(s string) string {
	u, err := url.QueryUnescape(s)
	if err != nil {
		return s
	}
	return u
}

func parseForm(contentType string, body []byte) []Field {
	if contentType == "" || len(body) == 0 {
		return []Field{}
	}
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return []Field{}
	}
	switch mediaType {
	case "application/x-www-form-urlencoded":
		return parseEncodedPairs(string(body))
	case "multipart/form-data":
		boundary := params["boundary"]
		if boundary == "" {
			return []Field{}
		}
		return parseMultipart(body, boundary)
	default:
		return []Field{}
	}
}

func parseMultipart(body []byte, boundary string) []Field {
	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	out := []Field{}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			return out
		}
		if err != nil {
			return out
		}
		name := part.FormName()
		if name == "" {
			_, _ = io.Copy(io.Discard, part)
			continue
		}
		if filename := part.FileName(); filename != "" {
			out = append(out, Field{Name: name, Value: "filename: " + filename})
			_, _ = io.Copy(io.Discard, part)
			continue
		}
		value, _ := io.ReadAll(io.LimitReader(part, MaxBodyBytes))
		out = append(out, Field{
			Name:  name,
			Value: strings.ToValidUTF8(string(value), "\uFFFD"),
		})
	}
}

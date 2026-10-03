// Package httpbin mounts httpbin.org-compatible routes using
// github.com/mccutchen/go-httpbin/v2 (v2.25.0).
package httpbin

import (
	"bufio"
	"net"
	"net/http"
	"net/url"
	"strings"

	gohttpbin "github.com/mccutchen/go-httpbin/v2/httpbin"
)

// pathPrefix is the prefix Catalog paths use and the prefix Mount assumes
// when the caller passes an empty string.
const pathPrefix = "/httpbin"

// Mount registers httpbin.org-compatible routes on mux under prefix.
// prefix should be "/httpbin". Requests to /httpbin/get behave like
// https://httpbin.org/get.
//
// The engine is go-httpbin's HTTPBin handler. WithPrefix sets the library
// base path so relative redirects, cookie redirects, HTML links, and status
// Location headers stay under prefix, and the library wraps its own mux in
// http.StripPrefix. Mount does not strip the path a second time. An empty
// prefix is treated as "/httpbin". A trailing slash is removed because
// WithPrefix must not end in "/".
//
// The bare prefix (for example /httpbin) is not registered. Callers own that
// path. prefix+"/" is the engine subtree, so the library index is reachable
// there unless the caller registers a more specific exact pattern such as
// /httpbin/{$}. Engine routes such as /httpbin/get stay on this subtree.
//
// Absolute redirect Location values whose host equals the request host are
// rewritten so the path stays under prefix. go-httpbin builds those URLs from
// the stripped path (for example "http://host/get"). Redirects to any other
// host are left unchanged. WithAllowedRedirectDomains is not set, so
// /redirect-to keeps httpbin's behavior of accepting any absolute or
// root-relative URL. The JSON "url" field reports the stripped path the
// library observed.
func Mount(mux *http.ServeMux, prefix string) {
	prefix = cleanPrefix(prefix)
	engine := gohttpbin.New(
		gohttpbin.WithPrefix(prefix),
		gohttpbin.WithHostname("requestbin"),
	)
	var handler http.Handler = engine
	if prefix != "/" {
		handler = &absoluteLocationPrefix{prefix: prefix, next: engine}
	}

	if prefix == "/" {
		mux.Handle("/", engine)
		return
	}

	mux.Handle(prefix+"/", handler)
}

func cleanPrefix(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return pathPrefix
	}
	if !strings.HasPrefix(prefix, "/") {
		prefix = "/" + prefix
	}
	for len(prefix) > 1 && strings.HasSuffix(prefix, "/") {
		prefix = strings.TrimSuffix(prefix, "/")
	}
	return prefix
}

// absoluteLocationPrefix rewrites same-host absolute Location headers on
// redirect responses so they include the mount prefix. It implements
// http.Flusher and http.Hijacker because go-httpbin type-asserts those
// interfaces for streaming and WebSocket endpoints.
type absoluteLocationPrefix struct {
	prefix string
	next   http.Handler
}

func (p *absoluteLocationPrefix) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.next.ServeHTTP(&locationPrefixWriter{
		ResponseWriter: w,
		prefix:         p.prefix,
		host:           r.Host,
	}, r)
}

type locationPrefixWriter struct {
	http.ResponseWriter
	prefix string
	host   string
	wrote  bool
}

func (w *locationPrefixWriter) WriteHeader(code int) {
	if !w.wrote && code >= 300 && code < 400 {
		rewriteSameHostLocation(w.Header(), w.prefix, w.host)
	}
	w.wrote = true
	w.ResponseWriter.WriteHeader(code)
}

func (w *locationPrefixWriter) Write(b []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

func (w *locationPrefixWriter) Flush() {
	w.ResponseWriter.(http.Flusher).Flush()
}

func (w *locationPrefixWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errNoHijack
	}
	return hj.Hijack()
}

func (w *locationPrefixWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

type noHijackError struct{}

func (noHijackError) Error() string {
	return "httpbin: response writer does not support hijacking"
}

var errNoHijack error = noHijackError{}

func rewriteSameHostLocation(h http.Header, prefix, host string) {
	loc := h.Get("Location")
	if loc == "" || prefix == "" || prefix == "/" {
		return
	}
	u, err := url.Parse(loc)
	if err != nil || !u.IsAbs() || !strings.EqualFold(u.Host, host) {
		return
	}
	if u.Path == prefix || strings.HasPrefix(u.Path, prefix+"/") {
		return
	}
	if !strings.HasPrefix(u.Path, "/") {
		return
	}
	u.Path = prefix + u.Path
	u.RawPath = ""
	h.Set("Location", u.String())
}

// Group is a section of the httpbin endpoint catalog.
type Group struct {
	Title string
	Items []Item
}

// Param is one named query parameter or URL path segment.
type Param struct {
	Name string
	Text string
}

// Item is one httpbin endpoint. Path includes the /httpbin prefix.
// Href is the link target when Path contains placeholders; an empty Href
// means the link is Path. Segments explain {placeholders}. An empty Params
// list means the endpoint has no query parameters.
type Item struct {
	Method      string
	Path        string
	Href        string
	Description string
	Segments    []Param
	Params      []Param
}

// Catalog returns the httpbin.org-compatible routes Mount exposes.
// Every Path includes the /httpbin prefix.
func Catalog() []Group {
	out := make([]Group, len(catalogGroups))
	for i, g := range catalogGroups {
		out[i] = g
		out[i].Items = make([]Item, len(g.Items))
		for j, item := range g.Items {
			out[i].Items[j] = item
			out[i].Items[j].Segments = append([]Param(nil), item.Segments...)
			out[i].Items[j].Params = append([]Param(nil), item.Params...)
		}
	}
	return out
}

var catalogGroups = []Group{
	{
		Title: "Methods",
		Items: []Item{
			{Method: "GET", Path: "/httpbin/get", Description: "Returns the method, headers, query, client IP, and URL as JSON."},
			{Method: "POST", Path: "/httpbin/post", Description: "Returns the POST body, form fields, headers, and query as JSON."},
			{Method: "PUT", Path: "/httpbin/put", Description: "Returns the PUT body, form fields, headers, and query as JSON."},
			{Method: "PATCH", Path: "/httpbin/patch", Description: "Returns the PATCH body, form fields, headers, and query as JSON."},
			{Method: "DELETE", Path: "/httpbin/delete", Description: "Returns the DELETE body, headers, and query as JSON."},
			{Method: "HEAD", Path: "/httpbin/head", Description: "Same as GET, but the response has headers only."},
			{Method: "POST", Path: "/httpbin/upload", Description: "Accepts POST, PUT, or PATCH and discards the body."},
			{Method: "GET", Path: "/httpbin/forms/post", Description: "Shows an HTML form that posts to /httpbin/post."},
		},
	},
	{
		Title: "Auth",
		Items: []Item{
			{
				Method: "GET", Path: "/httpbin/basic-auth/{user}/{passwd}", Href: "/httpbin/basic-auth/user/passwd",
				Description: "Asks for HTTP Basic authentication, then returns whether the username and password matched.",
				Segments: []Param{
					{Name: "{user}", Text: "Expected username."},
					{Name: "{passwd}", Text: "Expected password."},
				},
			},
			{Method: "GET", Path: "/httpbin/bearer", Description: "Returns the token when Authorization is Bearer and a token. Otherwise 401."},
			{
				Method: "GET", Path: "/httpbin/digest-auth/{qop}/{user}/{passwd}", Href: "/httpbin/digest-auth/auth/user/passwd",
				Description: "Asks for HTTP Digest authentication using MD5.",
				Segments: []Param{
					{Name: "{qop}", Text: "Must be auth."},
					{Name: "{user}", Text: "Expected username."},
					{Name: "{passwd}", Text: "Expected password."},
				},
			},
			{
				Method: "GET", Path: "/httpbin/digest-auth/{qop}/{user}/{passwd}/{algorithm}", Href: "/httpbin/digest-auth/auth/user/passwd/SHA-256",
				Description: "Asks for HTTP Digest authentication using MD5 or SHA-256.",
				Segments: []Param{
					{Name: "{qop}", Text: "Must be auth."},
					{Name: "{user}", Text: "Expected username."},
					{Name: "{passwd}", Text: "Expected password."},
					{Name: "{algorithm}", Text: "MD5 or SHA-256. Leave this segment off to use MD5."},
				},
			},
			{
				Method: "GET", Path: "/httpbin/hidden-basic-auth/{user}/{passwd}", Href: "/httpbin/hidden-basic-auth/user/passwd",
				Description: "HTTP Basic authentication that returns 404 when the username or password is wrong.",
				Segments: []Param{
					{Name: "{user}", Text: "Expected username."},
					{Name: "{passwd}", Text: "Expected password."},
				},
			},
		},
	},
	{
		Title: "Status codes",
		Items: []Item{
			{
				Method: "GET", Path: "/httpbin/status/{code}", Href: "/httpbin/status/200",
				Description: "Returns the status code you put in the path.",
				Segments:    []Param{{Name: "{code}", Text: "A status from 100 to 599. Separate several with commas, and add :weight to prefer one, for example 200:2,500:1."}},
			},
		},
	},
	{
		Title: "Request inspection",
		Items: []Item{
			{Method: "GET", Path: "/httpbin/headers", Description: "Returns the request headers as JSON."},
			{
				Method: "GET", Path: "/httpbin/ip", Description: "Returns the client IP address.",
				Params: []Param{{Name: "format", Text: "text returns the IP as plain text. Any other value, or omitting it, returns JSON."}},
			},
			{Method: "GET", Path: "/httpbin/user-agent", Description: "Returns the User-Agent header."},
			{
				Method:      "GET",
				Path:        "/httpbin/anything",
				Description: "Accepts any method and returns the method, headers, query, and body.",
			},
			{
				Method: "GET", Path: "/httpbin/anything/{anything}", Href: "/httpbin/anything/example",
				Description: "Same as /anything, and includes the rest of the path.",
				Segments:    []Param{{Name: "{anything}", Text: "Extra path text. It is included in the response URL."}},
			},
			{Method: "GET", Path: "/httpbin/dump/request", Description: "Returns the request in an approximate HTTP/1.x wire form."},
		},
	},
	{
		Title: "Response inspection",
		Items: []Item{
			{Method: "GET", Path: "/httpbin/cache", Description: "Returns 200, or 304 when If-Modified-Since or If-None-Match is set."},
			{
				Method: "GET", Path: "/httpbin/cache/{seconds}", Href: "/httpbin/cache/30",
				Description: "Sets Cache-Control: public, max-age to the given number of seconds, then returns the request.",
				Segments:    []Param{{Name: "{seconds}", Text: "max-age in seconds."}},
			},
			{
				Method: "GET", Path: "/httpbin/etag/{etag}", Href: "/httpbin/etag/abc",
				Description: "Treats the path value as the ETag and honors If-None-Match and If-Match.",
				Segments:    []Param{{Name: "{etag}", Text: "Tag value. The response ETag is that value in quotes."}},
			},
			{
				Method: "GET", Path: "/httpbin/response-headers", Href: "/httpbin/response-headers?X-Example=hello",
				Description: "Copies each query parameter into a response header and returns those headers as JSON.",
				Params:      []Param{{Name: "(any)", Text: "Header name. The value becomes the header value. Content-Type also sets the response content type."}},
			},
			{
				Method: "GET", Path: "/httpbin/trailers", Href: "/httpbin/trailers?X-Trail=1",
				Description: "Adds each query parameter as an HTTP trailer on a chunked JSON response.",
				Params:      []Param{{Name: "(any)", Text: "Trailer name and value. Some names, including Content-Type and Authorization, are rejected."}},
			},
		},
	},
	{
		Title: "Response formats",
		Items: []Item{
			{Method: "GET", Path: "/httpbin/json", Description: "Returns a sample JSON document."},
			{Method: "GET", Path: "/httpbin/html", Description: "Returns a sample HTML page."},
			{Method: "GET", Path: "/httpbin/xml", Description: "Returns a sample XML document."},
			{Method: "GET", Path: "/httpbin/robots.txt", Description: "Returns robots.txt rules."},
			{Method: "GET", Path: "/httpbin/deny", Description: "Returns the page that robots.txt disallows."},
			{Method: "GET", Path: "/httpbin/encoding/utf8", Description: "Returns a page containing UTF-8 text."},
			{Method: "GET", Path: "/httpbin/gzip", Description: "Returns gzip-compressed JSON about the request."},
			{Method: "GET", Path: "/httpbin/deflate", Description: "Returns deflate-compressed JSON about the request."},
			{Method: "GET", Path: "/httpbin/brotli", Description: "Returns 501. Brotli encoding is not available on this server."},
			{
				Method: "GET", Path: "/httpbin/jsonl", Href: "/httpbin/jsonl?count=2&duration=1s",
				Description: "Streams one JSON object per line.",
				Params: []Param{
					{Name: "count", Text: "How many lines to send. Default 10. Values below 1 become 1."},
					{Name: "duration", Text: "How long to spend sending the lines, such as 5s or a number of seconds. Default 0."},
					{Name: "delay", Text: "How long to wait before the first line. Same time format. Default 0."},
					{Name: "jitter", Text: "A number from 0 to 1 that varies the pause between lines. 0.5 means plus or minus 50%."},
				},
			},
			{
				Method: "GET", Path: "/httpbin/sse", Href: "/httpbin/sse?count=1&duration=1s",
				Description: "Streams server-sent events.",
				Params: []Param{
					{Name: "count", Text: "How many events to send. Default 10."},
					{Name: "duration", Text: "How long to spend sending them, such as 5s or a number of seconds. Default 5s."},
					{Name: "delay", Text: "How long to wait before the first event. Same time format. Default 0."},
					{Name: "jitter", Text: "A number from 0 to 1 that varies the pause between events."},
				},
			},
		},
	},
	{
		Title: "Dynamic data",
		Items: []Item{
			{
				Method: "GET", Path: "/httpbin/bytes/{n}", Href: "/httpbin/bytes/16",
				Description: "Returns n random bytes.",
				Segments:    []Param{{Name: "{n}", Text: "How many bytes. From 0 up to 1 MiB."}},
				Params:      []Param{{Name: "seed", Text: "Integer that makes the same bytes come back again."}},
			},
			{
				Method: "GET", Path: "/httpbin/delay/{n}", Href: "/httpbin/delay/0",
				Description: "Waits, then returns the request as JSON.",
				Segments:    []Param{{Name: "{n}", Text: "How long to wait. Use a duration such as 1s, or a number of seconds. At most 10s."}},
			},
			{
				Method: "GET", Path: "/httpbin/drip", Href: "/httpbin/drip?duration=0&delay=0&numbytes=4",
				Description: "Sends the body slowly, one byte at a time, after an optional wait.",
				Params: []Param{
					{Name: "duration", Text: "How long to spend writing the body. A duration such as 1s, or a number of seconds. Default 2s."},
					{Name: "delay", Text: "How long to wait before the first byte. Same time format. Default 2s. duration plus delay must be at most 10s."},
					{Name: "numbytes", Text: "How many bytes to send. Default 10. From 1 up to 1 MiB."},
					{Name: "code", Text: "HTTP status to return. Default 200. From 100 to 599."},
				},
			},
			{
				Method: "GET", Path: "/httpbin/range/{n}", Href: "/httpbin/range/32",
				Description: "Returns n bytes and honors a Range header.",
				Segments:    []Param{{Name: "{n}", Text: "How many bytes, from 1 up to 1 MiB."}},
				Params:      []Param{{Name: "duration", Text: "Optional wait before the body is written. A duration such as 1s, or a number of seconds. At most 10s."}},
			},
			{
				Method: "GET", Path: "/httpbin/stream/{n}", Href: "/httpbin/stream/2",
				Description: "Streams n lines of JSON, one object per line.",
				Segments:    []Param{{Name: "{n}", Text: "How many lines. Fewer than 1 becomes 1. More than 100 becomes 100."}},
			},
			{
				Method: "GET", Path: "/httpbin/stream-bytes/{n}", Href: "/httpbin/stream-bytes/32",
				Description: "Streams n random bytes in chunks.",
				Segments:    []Param{{Name: "{n}", Text: "How many bytes, from 0 up to 1 MiB."}},
				Params: []Param{
					{Name: "seed", Text: "Integer that makes the same bytes come back again."},
					{Name: "chunk_size", Text: "Bytes per chunk. Default 10240."},
				},
			},
			{Method: "GET", Path: "/httpbin/uuid", Description: "Returns a new UUID version 4."},
			{
				Method: "GET", Path: "/httpbin/base64/{value}", Href: "/httpbin/base64/aGVsbG8=",
				Description: "Decodes a Base64 string given in the path.",
				Segments:    []Param{{Name: "{value}", Text: "Base64 text to decode."}},
				Params:      []Param{{Name: "content-type", Text: "Content-Type of the response. Default text/plain."}},
			},
			{
				Method: "GET", Path: "/httpbin/base64/{operation}/{value}", Href: "/httpbin/base64/encode/hi",
				Description: "Encodes or decodes the path value as Base64.",
				Segments: []Param{
					{Name: "{operation}", Text: "encode or decode."},
					{Name: "{value}", Text: "Text to encode, or Base64 text to decode."},
				},
				Params: []Param{{Name: "content-type", Text: "Content-Type of the response. Default text/plain."}},
			},
			{
				Method: "GET", Path: "/httpbin/links/{n}", Href: "/httpbin/links/3",
				Description: "Redirects to a page of n HTML links.",
				Segments:    []Param{{Name: "{n}", Text: "How many links, from 0 to 256. This path redirects to the same page with offset 0."}},
			},
			{
				Method: "GET", Path: "/httpbin/links/{n}/{offset}", Href: "/httpbin/links/3/0",
				Description: "Returns a page of n HTML links, with one index shown as plain text.",
				Segments: []Param{
					{Name: "{n}", Text: "How many links, from 0 to 256."},
					{Name: "{offset}", Text: "Which index to show as text instead of a link."},
				},
			},
			{
				Method: "GET", Path: "/httpbin/unstable", Href: "/httpbin/unstable?failure_rate=0",
				Description: "Returns 200 or 500 at random.",
				Params: []Param{
					{Name: "failure_rate", Text: "Chance of a 500, from 0 to 1. Default 0.5."},
					{Name: "seed", Text: "Integer that makes the same success or failure repeat."},
				},
			},
			{Method: "GET", Path: "/httpbin/hostname", Description: "Returns the hostname serving the request."},
			{Method: "GET", Path: "/httpbin/env", Description: "Returns environment variables whose names start with HTTPBIN_, when any were set for this server."},
			{Method: "GET", Path: "/httpbin/version", Description: "Returns the version of these HTTP tools."},
		},
	},
	{
		Title: "Cookies",
		Items: []Item{
			{Method: "GET", Path: "/httpbin/cookies", Description: "Returns the cookies sent with the request."},
			{
				Method: "GET", Path: "/httpbin/cookies/set", Href: "/httpbin/cookies/set?session=abc",
				Description: "Sets cookies from the query string, then redirects to /httpbin/cookies.",
				Params: []Param{
					{Name: "(cookie name)", Text: "Name and value of a cookie to set. Any name that is not attr[...] is a cookie."},
					{Name: "attr[Secure]", Text: "true or 1 marks the cookies Secure. Also attr[HttpOnly], attr[Path], attr[Domain], and attr[SameSite] (strict, lax, or none)."},
				},
			},
			{
				Method: "GET", Path: "/httpbin/cookies/delete", Href: "/httpbin/cookies/delete?session=",
				Description: "Deletes the named cookies, then redirects to /httpbin/cookies.",
				Params: []Param{
					{Name: "(cookie name)", Text: "Name of a cookie to delete. The value is ignored."},
					{Name: "attr[Path]", Text: "Must match the path used when the cookie was set. attr[Domain] and attr[Secure] work the same way."},
				},
			},
		},
	},
	{
		Title: "Images",
		Items: []Item{
			{Method: "GET", Path: "/httpbin/image", Description: "Returns a PNG, JPEG, WEBP, or SVG image based on the Accept header. PNG is the default."},
			{Method: "GET", Path: "/httpbin/image/png", Description: "Returns a PNG image."},
			{Method: "GET", Path: "/httpbin/image/jpeg", Description: "Returns a JPEG image."},
			{Method: "GET", Path: "/httpbin/image/webp", Description: "Returns a WEBP image."},
			{Method: "GET", Path: "/httpbin/image/svg", Description: "Returns an SVG image."},
		},
	},
	{
		Title: "Redirects",
		Items: []Item{
			{
				Method: "GET", Path: "/httpbin/redirect/{n}", Href: "/httpbin/redirect/1",
				Description: "Redirects with 302 the given number of times. Relative unless you ask for an absolute URL.",
				Segments:    []Param{{Name: "{n}", Text: "How many redirects. Must be at least 1. The last hop lands on /httpbin/get."}},
				Params:      []Param{{Name: "absolute", Text: "true makes each Location an absolute URL. Any other value, or omitting it, keeps the next hop relative."}},
			},
			{
				Method: "GET", Path: "/httpbin/redirect-to", Href: "/httpbin/redirect-to?url=/httpbin/get&status_code=302",
				Description: "Redirects to the URL you provide.",
				Params: []Param{
					{Name: "url", Text: "Required. An absolute URL such as https://example.com/page, or a path that starts with /."},
					{Name: "status_code", Text: "Redirect status from 300 to 399. Default 302."},
				},
			},
			{
				Method: "GET", Path: "/httpbin/absolute-redirect/{n}", Href: "/httpbin/absolute-redirect/1",
				Description: "Redirects with 302 the given number of times, using absolute URLs.",
				Segments:    []Param{{Name: "{n}", Text: "How many redirects. Must be at least 1. The last hop lands on /httpbin/get."}},
			},
			{
				Method: "GET", Path: "/httpbin/relative-redirect/{n}", Href: "/httpbin/relative-redirect/1",
				Description: "Redirects with 302 the given number of times, using relative URLs.",
				Segments:    []Param{{Name: "{n}", Text: "How many redirects. Must be at least 1. The last hop lands on /httpbin/get."}},
			},
		},
	},
	{
		Title: "WebSocket",
		Items: []Item{
			{Method: "GET", Path: "/httpbin/websocket/echo", Description: "WebSocket echo. Send a message and the same message comes back."},
		},
	},
}

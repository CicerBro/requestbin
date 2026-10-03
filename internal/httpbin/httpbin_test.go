package httpbin

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestMountHTTPBin(t *testing.T) {
	mux := http.NewServeMux()
	Mount(mux, "/httpbin")
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	client := srv.Client()
	noFollow := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	t.Run("get", func(t *testing.T) {
		resp := mustGet(t, client, srv.URL+"/httpbin/get")
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		body := mustJSON(t, resp)
		if _, ok := body["url"]; !ok {
			if _, ok := body["headers"]; !ok {
				t.Fatalf("response missing url and headers: %#v", body)
			}
		}
		if _, ok := body["headers"]; !ok {
			t.Fatal("response missing headers")
		}
	})

	t.Run("status 418", func(t *testing.T) {
		resp := mustGet(t, client, srv.URL+"/httpbin/status/418")
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusTeapot {
			t.Fatalf("status = %d, want 418", resp.StatusCode)
		}
	})

	t.Run("headers echo", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, srv.URL+"/httpbin/headers", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-Requestbin-Probe", "echo-me")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		body := mustJSON(t, resp)
		headers, ok := body["headers"].(map[string]any)
		if !ok {
			t.Fatalf("headers = %#v", body["headers"])
		}
		raw, ok := headers["X-Requestbin-Probe"]
		if !ok {
			t.Fatalf("custom header not echoed: %#v", headers)
		}
		vals, ok := raw.([]any)
		if !ok || len(vals) != 1 || vals[0] != "echo-me" {
			t.Fatalf("header value = %#v, want [echo-me]", raw)
		}
	})

	t.Run("uuid", func(t *testing.T) {
		resp := mustGet(t, client, srv.URL+"/httpbin/uuid")
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		body := mustJSON(t, resp)
		id, _ := body["uuid"].(string)
		if !uuidPattern.MatchString(id) {
			t.Fatalf("uuid = %q", id)
		}
	})

	t.Run("index with and without slash", func(t *testing.T) {
		for _, path := range []string{"/httpbin", "/httpbin/"} {
			resp := mustGet(t, client, srv.URL+path)
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("%s status = %d, want 200", path, resp.StatusCode)
			}
			b, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(b), "/httpbin/get") {
				t.Fatalf("%s index did not mention /httpbin/get", path)
			}
		}
	})

	t.Run("relative redirect stays under prefix", func(t *testing.T) {
		resp := mustGet(t, noFollow, srv.URL+"/httpbin/redirect/1")
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusFound {
			t.Fatalf("status = %d, want 302", resp.StatusCode)
		}
		if got := resp.Header.Get("Location"); got != "/httpbin/get" {
			t.Fatalf("Location = %q, want /httpbin/get", got)
		}
	})

	t.Run("absolute redirect stays under prefix", func(t *testing.T) {
		resp := mustGet(t, noFollow, srv.URL+"/httpbin/absolute-redirect/1")
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusFound {
			t.Fatalf("status = %d, want 302", resp.StatusCode)
		}
		want := srv.URL + "/httpbin/get"
		if got := resp.Header.Get("Location"); got != want {
			t.Fatalf("Location = %q, want %q", got, want)
		}
	})

	t.Run("redirect-to other host is unchanged", func(t *testing.T) {
		resp := mustGet(t, noFollow, srv.URL+"/httpbin/redirect-to?url=https://example.com/elsewhere")
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusFound {
			t.Fatalf("status = %d, want 302", resp.StatusCode)
		}
		if got := resp.Header.Get("Location"); got != "https://example.com/elsewhere" {
			t.Fatalf("Location = %q", got)
		}
	})

	t.Run("stream flushes", func(t *testing.T) {
		resp := mustGet(t, client, srv.URL+"/httpbin/stream/1")
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), "url") {
			t.Fatalf("stream body = %q", b)
		}
	})
}

func TestMountTrimsTrailingSlash(t *testing.T) {
	mux := http.NewServeMux()
	Mount(mux, "/httpbin/")
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	resp, err := srv.Client().Get(srv.URL + "/httpbin/get")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestCatalogPaths(t *testing.T) {
	groups := Catalog()
	if len(groups) == 0 {
		t.Fatal("empty catalog")
	}
	seen := map[string]bool{}
	for _, g := range groups {
		if g.Title == "" {
			t.Fatal("group missing title")
		}
		for _, item := range g.Items {
			if item.Method == "" || item.Description == "" {
				t.Fatalf("incomplete item: %#v", item)
			}
			if strings.Contains(item.Description, "go-httpbin") || strings.Contains(strings.ToLower(g.Title), "go-httpbin") {
				t.Fatalf("catalog names the library: %s %s", g.Title, item.Description)
			}
			for _, part := range append(append([]Param{}, item.Params...), item.Segments...) {
				if part.Name == "" || part.Text == "" {
					t.Fatalf("blank param on %s: %#v", item.Path, part)
				}
			}
			if !strings.HasPrefix(item.Path, "/httpbin/") && item.Path != "/httpbin" {
				t.Fatalf("path %q missing /httpbin prefix", item.Path)
			}
			seen[item.Path] = true
		}
	}
	required := []string{
		"/httpbin/get",
		"/httpbin/post",
		"/httpbin/put",
		"/httpbin/patch",
		"/httpbin/delete",
		"/httpbin/headers",
		"/httpbin/ip",
		"/httpbin/user-agent",
		"/httpbin/status/{code}",
		"/httpbin/delay/{n}",
		"/httpbin/bytes/{n}",
		"/httpbin/uuid",
		"/httpbin/base64/{value}",
		"/httpbin/anything",
		"/httpbin/anything/{anything}",
		"/httpbin/json",
		"/httpbin/html",
		"/httpbin/xml",
		"/httpbin/robots.txt",
		"/httpbin/deny",
		"/httpbin/cache",
		"/httpbin/etag/{etag}",
		"/httpbin/redirect/{n}",
		"/httpbin/redirect-to",
		"/httpbin/absolute-redirect/{n}",
		"/httpbin/relative-redirect/{n}",
		"/httpbin/cookies",
		"/httpbin/cookies/set",
		"/httpbin/cookies/delete",
		"/httpbin/basic-auth/{user}/{passwd}",
		"/httpbin/bearer",
		"/httpbin/gzip",
		"/httpbin/deflate",
		"/httpbin/brotli",
		"/httpbin/image",
		"/httpbin/image/png",
		"/httpbin/image/jpeg",
		"/httpbin/image/svg",
		"/httpbin/image/webp",
		"/httpbin/response-headers",
		"/httpbin/stream/{n}",
		"/httpbin/drip",
		"/httpbin/range/{n}",
		"/httpbin/links/{n}",
		"/httpbin/forms/post",
		"/httpbin/unstable",
		"/httpbin/encoding/utf8",
	}
	for _, path := range required {
		if !seen[path] {
			t.Errorf("catalog missing %s", path)
		}
	}
	if seen["/httpbin/"] || seen["/httpbin"] {
		t.Fatal("catalog still lists the HTML index")
	}

	byPath := map[string]Item{}
	for _, g := range groups {
		for _, item := range g.Items {
			byPath[item.Path] = item
		}
	}
	checks := []struct {
		path   string
		params []string
		segs   []string
	}{
		{path: "/httpbin/headers"},
		{path: "/httpbin/get"},
		{path: "/httpbin/post"},
		{path: "/httpbin/anything"},
		{path: "/httpbin/redirect-to", params: []string{"url", "status_code"}},
		{path: "/httpbin/drip", params: []string{"duration", "delay", "numbytes", "code"}},
		{path: "/httpbin/delay/{n}", segs: []string{"{n}"}},
		{path: "/httpbin/status/{code}", segs: []string{"{code}"}},
		{path: "/httpbin/bearer"},
		{path: "/httpbin/unstable", params: []string{"failure_rate", "seed"}},
		{path: "/httpbin/cache"},
		{path: "/httpbin/response-headers", params: []string{"(any)"}},
		{path: "/httpbin/cookies/set", params: []string{"(cookie name)", "attr[Secure]"}},
	}
	for _, check := range checks {
		item, ok := byPath[check.path]
		if !ok {
			t.Fatalf("missing %s", check.path)
		}
		if len(item.Params) != len(check.params) {
			t.Fatalf("%s params = %#v", check.path, item.Params)
		}
		for i, name := range check.params {
			if item.Params[i].Name != name {
				t.Fatalf("%s param %d = %q", check.path, i, item.Params[i].Name)
			}
		}
		if len(item.Segments) != len(check.segs) {
			t.Fatalf("%s segments = %#v", check.path, item.Segments)
		}
		for i, name := range check.segs {
			if item.Segments[i].Name != name {
				t.Fatalf("%s segment %d = %q", check.path, i, item.Segments[i].Name)
			}
		}
	}
}

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func mustGet(t *testing.T, client *http.Client, url string) *http.Response {
	t.Helper()
	resp, err := client.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func mustJSON(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body
}

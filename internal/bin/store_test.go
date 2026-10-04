package bin

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCreate(t *testing.T) {
	if MaxBins != 200 || MaxRequestsPerBin != 100 || MaxBodyBytes != 1<<20 {
		t.Fatalf("caps = bins %d reqs %d body %d", MaxBins, MaxRequestsPerBin, MaxBodyBytes)
	}

	s := NewStore()
	info, err := s.Create("")
	if err != nil {
		t.Fatal(err)
	}
	if len(info.ID) != 8 || !isHex(info.ID) {
		t.Fatalf("id = %q, want 8 hex chars", info.ID)
	}
	if len(info.Key) != 32 || !isHex(info.Key) || info.Key == info.ID {
		t.Fatalf("key = %q", info.Key)
	}
	if err := s.Authorize(info.ID, info.Key); err != nil {
		t.Fatal(err)
	}
	if err := s.Authorize(info.ID, "00000000000000000000000000000000"); err != ErrForbidden {
		t.Fatalf("wrong key = %v", err)
	}
	if time.Since(info.Created) > time.Minute || info.Created.Location() != time.UTC {
		t.Fatalf("created = %v", info.Created)
	}
	got, ok := s.Get(info.ID)
	if !ok || got.ID != info.ID || !got.Created.Equal(info.Created) {
		t.Fatalf("get = %+v ok=%v", got, ok)
	}

	reqs, ok := s.ListRequests(info.ID)
	if !ok || len(reqs) != 0 || reqs == nil {
		t.Fatalf("requests = %#v ok=%v", reqs, ok)
	}

	other, err := s.Create("")
	if err != nil {
		t.Fatal(err)
	}
	if other.ID == info.ID {
		t.Fatal("duplicate bin id")
	}
}

func TestCapture(t *testing.T) {
	s := NewStore()
	info, err := s.Create("")
	if err != nil {
		t.Fatal(err)
	}

	t.Run("root", func(t *testing.T) {
		rec := capture(t, s, info.ID, http.MethodGet, "/hooks/"+info.ID, "", nil)
		if rec.Method != http.MethodGet || rec.Path != "/" || rec.ContentLength != 0 || rec.Body != "" {
			t.Fatalf("root capture = %+v", rec)
		}
		if _, err := time.Parse(time.RFC3339, rec.Timestamp); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("subpath query headers and json body", func(t *testing.T) {
		body := `{"hello":"world"}`
		req := httptest.NewRequest(http.MethodPost, "/hooks/"+info.ID+"/foo?x=1&x=2&y=ok", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Add("X-Custom", "a")
		req.Header.Add("X-Custom", "b")
		req.RemoteAddr = "203.0.113.5:4242"
		req.RequestURI = "/hooks/" + info.ID + "/foo?x=1&x=2&y=ok"

		rec, err := s.Capture(info.ID, req)
		if err != nil {
			t.Fatal(err)
		}
		if rec.Method != http.MethodPost {
			t.Fatalf("method = %s", rec.Method)
		}
		if rec.Path != "/foo?x=1&x=2&y=ok" {
			t.Fatalf("path = %q", rec.Path)
		}
		if rec.RemoteAddr != "203.0.113.5:4242" {
			t.Fatalf("remote = %s", rec.RemoteAddr)
		}
		if rec.ContentType != "application/json" || rec.ContentLength != len(body) || rec.Body != body {
			t.Fatalf("body capture = %+v", rec)
		}
		if got := fieldValues(rec.Query, "x"); !equalStrings(got, []string{"1", "2"}) {
			t.Fatalf("query x = %#v", got)
		}
		if got := fieldValues(rec.Query, "y"); !equalStrings(got, []string{"ok"}) {
			t.Fatalf("query y = %#v", got)
		}
		if got := fieldValues(rec.Headers, "X-Custom"); !equalStrings(got, []string{"a", "b"}) {
			t.Fatalf("headers = %#v", got)
		}
		if len(rec.Form) != 0 {
			t.Fatalf("form = %#v", rec.Form)
		}
		if len(rec.ID) != 8 || !isHex(rec.ID) {
			t.Fatalf("request id = %q", rec.ID)
		}
	})

	t.Run("query only", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/hooks/"+info.ID+"?z=1", nil)
		req.RequestURI = "/hooks/" + info.ID + "?z=1"
		rec, err := s.Capture(info.ID, req)
		if err != nil {
			t.Fatal(err)
		}
		if rec.Path != "/?z=1" {
			t.Fatalf("path = %q", rec.Path)
		}
	})

	t.Run("urlencoded form", func(t *testing.T) {
		raw := "a=1&b=two+words&a=3"
		req := httptest.NewRequest(http.MethodPost, "/hooks/"+info.ID, strings.NewReader(raw))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec, err := s.Capture(info.ID, req)
		if err != nil {
			t.Fatal(err)
		}
		want := []Field{{Name: "a", Value: "1"}, {Name: "b", Value: "two words"}, {Name: "a", Value: "3"}}
		if !equalFields(rec.Form, want) {
			t.Fatalf("form = %#v", rec.Form)
		}
	})

	t.Run("multipart keeps filename only", func(t *testing.T) {
		var buf bytes.Buffer
		w := multipart.NewWriter(&buf)
		file, err := w.CreateFormFile("upload", "note.txt")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(file, "secret-bytes"); err != nil {
			t.Fatal(err)
		}
		if err := w.WriteField("msg", "hi"); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/hooks/"+info.ID+"/up", &buf)
		req.Header.Set("Content-Type", w.FormDataContentType())
		rec, err := s.Capture(info.ID, req)
		if err != nil {
			t.Fatal(err)
		}
		if got := fieldValues(rec.Form, "upload"); !equalStrings(got, []string{"filename: note.txt"}) {
			t.Fatalf("upload form = %#v", rec.Form)
		}
		if got := fieldValues(rec.Form, "msg"); !equalStrings(got, []string{"hi"}) {
			t.Fatalf("msg form = %#v", rec.Form)
		}
		for _, f := range rec.Form {
			if strings.Contains(f.Value, "secret-bytes") {
				t.Fatalf("file bytes stored in form: %#v", rec.Form)
			}
		}
	})

	t.Run("invalid utf-8", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/hooks/"+info.ID, bytes.NewReader([]byte{'h', 0xff, 'i'}))
		rec, err := s.Capture(info.ID, req)
		if err != nil {
			t.Fatal(err)
		}
		if rec.ContentLength != 3 || rec.Body != "h\uFFFDi" {
			t.Fatalf("body = %q len %d", rec.Body, rec.ContentLength)
		}
	})
}

func TestCaptureOrder(t *testing.T) {
	s := NewStore()
	s.maxReqs = 2
	info, err := s.Create("")
	if err != nil {
		t.Fatal(err)
	}
	first := capture(t, s, info.ID, http.MethodPut, "/hooks/"+info.ID+"/1", "", nil)
	second := capture(t, s, info.ID, http.MethodPut, "/hooks/"+info.ID+"/2", "", nil)
	third := capture(t, s, info.ID, http.MethodPut, "/hooks/"+info.ID+"/3", "", nil)

	reqs, ok := s.ListRequests(info.ID)
	if !ok {
		t.Fatal("missing bin")
	}
	if len(reqs) != 2 {
		t.Fatalf("len = %d, want 2 (oldest dropped)", len(reqs))
	}
	if reqs[0].ID != third.ID || reqs[1].ID != second.ID {
		t.Fatalf("order = %s, %s; want newest %s then %s", reqs[0].ID, reqs[1].ID, third.ID, second.ID)
	}
	if reqs[0].Path != "/3" || reqs[1].Path != "/2" {
		t.Fatalf("paths = %s %s", reqs[0].Path, reqs[1].Path)
	}
	for _, req := range reqs {
		if req.ID == first.ID {
			t.Fatal("oldest request was kept")
		}
	}
}

func TestBodyLimit(t *testing.T) {
	s := NewStore()
	info, err := s.Create("")
	if err != nil {
		t.Fatal(err)
	}

	exact := bytes.Repeat([]byte("B"), MaxBodyBytes)
	rec := capture(t, s, info.ID, http.MethodPost, "/hooks/"+info.ID, "text/plain", exact)
	if rec.ContentLength != MaxBodyBytes || rec.Body != string(exact) || strings.Contains(rec.Body, "truncated") {
		t.Fatalf("exact cap: len=%d truncated=%v", rec.ContentLength, strings.Contains(rec.Body, "truncated"))
	}

	over := append(bytes.Repeat([]byte("A"), MaxBodyBytes), []byte("EXTRA")...)
	rec = capture(t, s, info.ID, http.MethodPost, "/hooks/"+info.ID, "text/plain", over)
	if rec.ContentLength != MaxBodyBytes {
		t.Fatalf("content length = %d, want %d", rec.ContentLength, MaxBodyBytes)
	}
	if strings.Contains(rec.Body, "EXTRA") {
		t.Fatal("body kept bytes past the cap")
	}
	if !strings.HasPrefix(rec.Body, strings.Repeat("A", 64)) || !strings.HasSuffix(rec.Body, "\n[truncated to 1 MiB]") {
		t.Fatalf("truncation note missing: suffix %q", rec.Body[len(rec.Body)-40:])
	}
}

func TestList(t *testing.T) {
	s := NewStore()
	if got := s.List(); len(got) != 0 {
		t.Fatalf("empty list = %#v", got)
	}
	first, err := s.Create("")
	if err != nil {
		t.Fatal(err)
	}
	capture(t, s, first.ID, http.MethodPost, "/hooks/"+first.ID, "text/plain", []byte("hi"))
	second, err := s.Create("")
	if err != nil {
		t.Fatal(err)
	}
	got := s.List()
	if len(got) != 2 {
		t.Fatalf("list = %#v", got)
	}
	if got[0].ID != second.ID || got[0].Requests != 0 {
		t.Fatalf("newest = %+v", got[0])
	}
	if got[1].ID != first.ID || got[1].Requests != 1 || !got[1].Created.Equal(first.Created) {
		t.Fatalf("older = %+v", got[1])
	}
}

func TestUnknownBin(t *testing.T) {
	s := NewStore()
	if _, ok := s.Get("missing1"); ok {
		t.Fatal("get found a missing bin")
	}
	if _, ok := s.ListRequests("missing1"); ok {
		t.Fatal("list found a missing bin")
	}
	req := httptest.NewRequest(http.MethodPost, "/hooks/missing1", strings.NewReader("nope"))
	if _, err := s.Capture("missing1", req); err != ErrNotFound {
		t.Fatalf("capture err = %v", err)
	}
	if _, ok := s.Get("missing1"); ok {
		t.Fatal("capture created an unknown bin")
	}
}

func TestEvictOldestBin(t *testing.T) {
	s := NewStore()
	s.maxBins = 2
	first, err := s.Create("")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Create("")
	if err != nil {
		t.Fatal(err)
	}
	third, err := s.Create("")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get(first.ID); ok {
		t.Fatal("oldest bin was kept")
	}
	if _, ok := s.Get(second.ID); !ok {
		t.Fatal("second bin missing")
	}
	if _, ok := s.Get(third.ID); !ok {
		t.Fatal("newest bin missing")
	}
}

func TestConcurrentCapture(t *testing.T) {
	s := NewStore()
	info, err := s.Create("")
	if err != nil {
		t.Fatal(err)
	}
	const n = 40
	var wg sync.WaitGroup
	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/hooks/"+info.ID, strings.NewReader("x"))
			if _, err := s.Capture(info.ID, req); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	reqs, ok := s.ListRequests(info.ID)
	if !ok || len(reqs) != n {
		t.Fatalf("got %d ok=%v, want %d", len(reqs), ok, n)
	}
}

func TestReloadFromDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bins.json")
	s, err := Open(path, DefaultTTL)
	if err != nil {
		t.Fatal(err)
	}
	info, err := s.Create("")
	if err != nil {
		t.Fatal(err)
	}
	rec := capture(t, s, info.ID, http.MethodPost, "/hooks/"+info.ID+"/saved?x=1", "text/plain", []byte("hello"))

	s2, err := Open(path, DefaultTTL)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := s2.Get(info.ID)
	if !ok || got.ID != info.ID || !got.Created.Equal(info.Created) || got.Key != "" {
		t.Fatalf("reloaded = %+v ok=%v", got, ok)
	}
	if err := s2.Authorize(info.ID, info.Key); err != nil {
		t.Fatal(err)
	}
	if s2.ListAuthorized(map[string]string{info.ID: "nope"}) != nil && len(s2.ListAuthorized(map[string]string{info.ID: "nope"})) != 0 {
		t.Fatal("wrong key listed the bin")
	}
	if got := s2.ListAuthorized(map[string]string{info.ID: info.Key}); len(got) != 1 || got[0].ID != info.ID {
		t.Fatalf("authorized = %+v", got)
	}
	reqs, ok := s2.ListRequests(info.ID)
	if !ok || len(reqs) != 1 {
		t.Fatalf("reqs = %#v ok=%v", reqs, ok)
	}
	gotReq := reqs[0]
	if gotReq.ID != rec.ID || gotReq.Method != http.MethodPost || gotReq.Path != "/saved?x=1" || gotReq.Body != "hello" {
		t.Fatalf("request = %+v", gotReq)
	}
	if values := fieldValues(gotReq.Query, "x"); !equalStrings(values, []string{"1"}) {
		t.Fatalf("query = %#v", values)
	}
	if gotReq.ContentType != "text/plain" || gotReq.ContentLength != len("hello") {
		t.Fatalf("meta = %+v", gotReq)
	}
}

func TestExpiry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bins.json")
	s, err := Open(path, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return start }

	info, err := s.Create("")
	if err != nil {
		t.Fatal(err)
	}
	capture(t, s, info.ID, http.MethodGet, "/hooks/"+info.ID, "", nil)

	s.now = func() time.Time { return start.Add(time.Hour - time.Nanosecond) }
	if _, ok := s.Get(info.ID); !ok {
		t.Fatal("bin expired early")
	}
	reqs, ok := s.ListRequests(info.ID)
	if !ok || len(reqs) != 1 {
		t.Fatalf("requests before expiry = %#v ok=%v", reqs, ok)
	}

	s.now = func() time.Time { return start.Add(time.Hour) }
	if _, ok := s.Get(info.ID); ok {
		t.Fatal("expired bin still present")
	}
	if _, ok := s.ListRequests(info.ID); ok {
		t.Fatal("expired bin still lists requests")
	}
	if got := s.List(); len(got) != 0 {
		t.Fatalf("list = %+v", got)
	}
	req := httptest.NewRequest(http.MethodPost, "/hooks/"+info.ID, strings.NewReader("x"))
	if _, err := s.Capture(info.ID, req); err != ErrNotFound {
		t.Fatalf("capture err = %v", err)
	}

	reloaded, err := Open(path, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reloaded.Get(info.ID); ok {
		t.Fatal("expired bin reloaded from disk")
	}
}

func TestOpenDropsExpired(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bins.json")
	old := time.Now().UTC().Add(-72 * time.Hour).Truncate(time.Second)
	fresh := time.Now().UTC().Truncate(time.Second)
	payload := persistedFile{Bins: []persistedBin{
		{
			ID:      "oldbin01",
			Created: old,
			Requests: []Request{{
				ID: "req00001", Method: http.MethodPost, Path: "/gone", Body: "stale",
				Headers: []Field{}, Query: []Field{}, Form: []Field{},
			}},
		},
		{
			ID:      "freshbin",
			Created: fresh,
			Requests: []Request{{
				ID: "req00002", Method: http.MethodPut, Path: "/kept", Body: "alive", ContentLength: 5,
				Headers: []Field{{Name: "X-Test", Value: "1"}},
				Query:   []Field{}, Form: []Field{},
			}},
		},
	}}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path, DefaultTTL)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get("oldbin01"); ok {
		t.Fatal("expired bin loaded")
	}
	got, ok := s.Get("freshbin")
	if !ok || !got.Created.Equal(fresh) {
		t.Fatalf("fresh = %+v ok=%v", got, ok)
	}
	reqs, ok := s.ListRequests("freshbin")
	if !ok || len(reqs) != 1 || reqs[0].Body != "alive" || reqs[0].Method != http.MethodPut || reqs[0].Path != "/kept" {
		t.Fatalf("fresh requests = %#v ok=%v", reqs, ok)
	}
	if values := fieldValues(reqs[0].Headers, "X-Test"); !equalStrings(values, []string{"1"}) {
		t.Fatalf("headers = %#v", reqs[0].Headers)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "oldbin01") || strings.Contains(string(raw), "stale") {
		t.Fatalf("expired bin still on disk: %s", raw)
	}
	if !strings.Contains(string(raw), "freshbin") || !strings.Contains(string(raw), "alive") {
		t.Fatalf("fresh bin missing on disk: %s", raw)
	}
}

func TestClearAndDelete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bins.json")
	s, err := Open(path, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.Create("")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Create("")
	if err != nil {
		t.Fatal(err)
	}
	capture(t, s, first.ID, http.MethodPost, "/hooks/"+first.ID, "text/plain", []byte("one"))
	capture(t, s, first.ID, http.MethodPost, "/hooks/"+first.ID, "text/plain", []byte("two"))

	if err := s.Clear(first.ID); err != nil {
		t.Fatal(err)
	}
	reqs, ok := s.ListRequests(first.ID)
	if !ok || len(reqs) != 0 {
		t.Fatalf("cleared = %#v ok=%v", reqs, ok)
	}
	if _, ok := s.Get(first.ID); !ok {
		t.Fatal("clear removed the bin")
	}
	reloaded, err := Open(path, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	reqs, ok = reloaded.ListRequests(first.ID)
	if !ok || len(reqs) != 0 {
		t.Fatalf("reloaded clear = %#v ok=%v", reqs, ok)
	}
	if err := reloaded.Delete(first.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := reloaded.Get(first.ID); ok {
		t.Fatal("deleted bin still present")
	}
	if _, ok := reloaded.ListRequests(first.ID); ok {
		t.Fatal("deleted bin still lists requests")
	}
	list := reloaded.List()
	if len(list) != 1 || list[0].ID != second.ID {
		t.Fatalf("list after delete = %+v", list)
	}
	again, err := Open(path, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := again.Get(first.ID); ok {
		t.Fatal("deleted bin reloaded")
	}
	if _, ok := again.Get(second.ID); !ok {
		t.Fatal("remaining bin missing after reload")
	}
	if err := s.Clear("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("clear missing = %v", err)
	}
	if err := s.Delete("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete missing = %v", err)
	}
}

func TestBinDisplayName(t *testing.T) {
	s := NewStore()
	info, err := s.Create("  Stripe   webhooks ")
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "Stripe webhooks" {
		t.Fatalf("name = %q", info.Name)
	}
	got, ok := s.Get(info.ID)
	if !ok || got.Name != "Stripe webhooks" || got.Key != "" {
		t.Fatalf("get = %+v ok=%v", got, ok)
	}
	if _, err := s.Create(strings.Repeat("a", MaxNameRunes+1)); !errors.Is(err, ErrInvalidName) {
		t.Fatalf("long name = %v", err)
	}
	if _, err := s.SetName(info.ID, "bad\x00name"); !errors.Is(err, ErrInvalidName) {
		t.Fatalf("control name = %v", err)
	}
	cleared, err := s.SetName(info.ID, "   ")
	if err != nil || cleared != "" {
		t.Fatalf("clear name = %q %v", cleared, err)
	}
	if got, _ := s.Get(info.ID); got.Name != "" {
		t.Fatalf("cleared = %q", got.Name)
	}
}

func capture(t *testing.T, s *Store, id, method, target, contentType string, body []byte) Request {
	t.Helper()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if req.URL.RawQuery != "" {
		req.RequestURI = req.URL.Path + "?" + req.URL.RawQuery
	} else {
		req.RequestURI = req.URL.RequestURI()
	}
	rec, err := s.Capture(id, req)
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

func isHex(s string) bool {
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return s != ""
}

func fieldValues(fields []Field, name string) []string {
	var out []string
	for _, f := range fields {
		if f.Name == name {
			out = append(out, f.Value)
		}
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalFields(a, b []Field) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"requestbin/internal/bin"
)

func TestPublicSchemeHost(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/bins/abcd1234", nil)
	req.Host = "bin.example"
	req.Header.Set("X-Forwarded-Proto", "HTTPS, http")
	scheme, host := publicSchemeHost(req)
	if scheme != "https" || host != "bin.example" {
		t.Fatalf("got %s %s", scheme, host)
	}

	plain := httptest.NewRequest(http.MethodGet, "/", nil)
	plain.Host = "localhost:8080"
	scheme, host = publicSchemeHost(plain)
	if scheme != "http" || host != "localhost:8080" {
		t.Fatalf("plain got %s %s", scheme, host)
	}
}

func TestBinTTL(t *testing.T) {
	if bin.DefaultTTL != 48*time.Hour {
		t.Fatalf("default ttl = %s", bin.DefaultTTL)
	}
	t.Setenv("BIN_TTL", "")
	d, err := binTTL()
	if err != nil || d != 48*time.Hour {
		t.Fatalf("default = %s %v", d, err)
	}
	t.Setenv("BIN_TTL", "168h")
	d, err = binTTL()
	if err != nil || d != 168*time.Hour {
		t.Fatalf("168h = %s %v", d, err)
	}
	t.Setenv("BIN_TTL", "nope")
	if _, err := binTTL(); err == nil {
		t.Fatal("expected invalid BIN_TTL error")
	}
	t.Setenv("BIN_TTL", "0s")
	if _, err := binTTL(); err == nil {
		t.Fatal("expected non-positive BIN_TTL error")
	}
	t.Setenv("BIN_TTL", "-1h")
	if _, err := binTTL(); err == nil {
		t.Fatal("expected negative BIN_TTL error")
	}
}

func TestBinLimits(t *testing.T) {
	if bin.MaxBins != 200 || bin.MaxRequestsPerBin != 100 || bin.MaxBodyBytes != 1<<20 {
		t.Fatalf("defaults = %d %d %d", bin.MaxBins, bin.MaxRequestsPerBin, bin.MaxBodyBytes)
	}
	t.Setenv("MAX_BINS", "")
	t.Setenv("MAX_REQUESTS_PER_BIN", "")
	t.Setenv("MAX_BODY_BYTES", "")
	limits, err := binLimits()
	if err != nil || limits != bin.DefaultLimits() {
		t.Fatalf("defaults = %+v %v", limits, err)
	}

	t.Setenv("MAX_BINS", "3")
	t.Setenv("MAX_REQUESTS_PER_BIN", "4")
	t.Setenv("MAX_BODY_BYTES", "2048")
	limits, err = binLimits()
	if err != nil || limits.MaxBins != 3 || limits.MaxRequestsPerBin != 4 || limits.MaxBodyBytes != 2048 {
		t.Fatalf("override = %+v %v", limits, err)
	}

	t.Setenv("MAX_BINS", "nope")
	if _, err := binLimits(); err == nil {
		t.Fatal("expected invalid MAX_BINS error")
	}
	t.Setenv("MAX_BINS", "3")
	t.Setenv("MAX_REQUESTS_PER_BIN", "0")
	if _, err := binLimits(); err == nil {
		t.Fatal("expected non-positive MAX_REQUESTS_PER_BIN error")
	}
	t.Setenv("MAX_REQUESTS_PER_BIN", "4")
	t.Setenv("MAX_BODY_BYTES", "-5")
	if _, err := binLimits(); err == nil {
		t.Fatal("expected negative MAX_BODY_BYTES error")
	}
}

func TestExpiredBinNotFound(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bins.json")
	raw := []byte(`{"bins":[{"id":"deadbeef","created":"2020-01-01T00:00:00Z","requests":[{"ID":"req12345","Method":"GET","Path":"/","Timestamp":"2020-01-01T00:00:01Z","RemoteAddr":"","ContentType":"","ContentLength":0,"Headers":[],"Query":[],"Body":"secret","Form":[]}]}]}`)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := bin.Open(path, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(newMux(store))
	defer srv.Close()

	page, err := http.Get(srv.URL + "/bins/deadbeef")
	if err != nil {
		t.Fatal(err)
	}
	if page.StatusCode != http.StatusNotFound {
		t.Fatalf("inspect = %d", page.StatusCode)
	}
	page.Body.Close()

	api, err := http.Get(srv.URL + "/api/bins/deadbeef/requests")
	if err != nil {
		t.Fatal(err)
	}
	apiBody := mustRead(t, api)
	if api.StatusCode != http.StatusNotFound || !strings.Contains(apiBody, "bin not found") {
		t.Fatalf("api = %d %s", api.StatusCode, apiBody)
	}

	hook, err := http.Post(srv.URL+"/hooks/deadbeef", "text/plain", strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	hookBody := mustRead(t, hook)
	if hook.StatusCode != http.StatusNotFound || !strings.Contains(hookBody, "bin not found") {
		t.Fatalf("hook = %d %s", hook.StatusCode, hookBody)
	}

	list, err := http.Get(srv.URL + "/api/bins")
	if err != nil {
		t.Fatal(err)
	}
	listBody := mustRead(t, list)
	if list.StatusCode != http.StatusOK || strings.Contains(listBody, "deadbeef") || strings.Contains(listBody, "secret") {
		t.Fatalf("list = %d %s", list.StatusCode, listBody)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(saved), "deadbeef") || strings.Contains(string(saved), "secret") {
		t.Fatalf("expired bin still stored: %s", saved)
	}
}

func TestListenAddr(t *testing.T) {
	cases := []struct {
		name string
		args []string
		env  string
		want string
		err  string
	}{
		{name: "default", want: ":8080"},
		{name: "port env", env: "9090", want: ":9090"},
		{name: "host env", env: "127.0.0.1:9091", want: "127.0.0.1:9091"},
		{name: "addr flag beats env", args: []string{"-addr", "127.0.0.1:8082"}, env: "9090", want: "127.0.0.1:8082"},
		{name: "addr all interfaces", args: []string{"-addr", ":9090"}, want: ":9090"},
		{name: "port flag beats env", args: []string{"-port", "9090"}, env: "127.0.0.1:1111", want: ":9090"},
		{name: "ip flag", args: []string{"-ip", "127.0.0.1"}, env: "9090", want: "127.0.0.1:8080"},
		{name: "ip and port", args: []string{"-ip", "10.0.0.5", "-port", "9090"}, want: "10.0.0.5:9090"},
		{name: "empty ip", args: []string{"-ip", "", "-port", "9090"}, env: "1111", want: ":9090"},
		{name: "ipv6", args: []string{"-ip", "::1", "-port", "9090"}, want: "[::1]:9090"},
		{name: "port min", args: []string{"-port", "1"}, want: ":1"},
		{name: "port max", args: []string{"-port", "65535"}, want: ":65535"},
		{name: "addr with port", args: []string{"-addr", ":9090", "-port", "8080"}, err: "-addr cannot be combined with -ip or -port"},
		{name: "addr with ip", args: []string{"-addr", ":9090", "-ip", "127.0.0.1"}, err: "-addr cannot be combined with -ip or -port"},
		{name: "bad port", args: []string{"-port", "abc"}, err: "must be a number from 1 to 65535"},
		{name: "port zero", args: []string{"-port", "0"}, err: "must be a number from 1 to 65535"},
		{name: "port high", args: []string{"-port", "65536"}, err: "must be a number from 1 to 65535"},
		{name: "empty addr", args: []string{"-addr", ""}, err: "-addr requires a host:port listen address"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveListenAddr(tc.args, tc.env, io.Discard)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("err = %v, want %q", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestListenUsage(t *testing.T) {
	var buf bytes.Buffer
	_, err := resolveListenAddr([]string{"-h"}, "", &buf)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("err = %v", err)
	}
	text := buf.String()
	for _, want := range []string{"-addr", "-ip", "-port", "PORT", ":8080", "127.0.0.1:9090"} {
		if !strings.Contains(text, want) {
			t.Fatalf("usage missing %q\n%s", want, text)
		}
	}
}

func TestRoutes(t *testing.T) {
	srv := httptest.NewServer(newMux(bin.NewStore()))
	defer srv.Close()

	noFollow := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	home, err := noFollow.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	homeBody := mustRead(t, home)
	if home.StatusCode != http.StatusSeeOther || home.Header.Get("Location") != "/bins" {
		t.Fatalf("home = %d location %q %s", home.StatusCode, home.Header.Get("Location"), homeBody)
	}
	if strings.Contains(homeBody, "Inspect") && strings.Contains(homeBody, "Create a Bin") {
		t.Fatalf("home still rendered the landing page: %s", homeBody)
	}

	emptyBins, err := http.Get(srv.URL + "/bins")
	if err != nil {
		t.Fatal(err)
	}
	emptyBody := mustRead(t, emptyBins)
	if emptyBins.StatusCode != http.StatusOK || !strings.Contains(emptyBody, "No bins yet") || !strings.Contains(emptyBody, "New bin") {
		t.Fatalf("empty bins = %d %s", emptyBins.StatusCode, emptyBody)
	}

	id, key := createBin(t, noFollow, srv.URL)

	hookReq, err := http.NewRequest(http.MethodPost, srv.URL+"/hooks/"+id+"/foo?x=1", strings.NewReader(`{"n":1}`))
	if err != nil {
		t.Fatal(err)
	}
	hookReq.Header.Set("Content-Type", "application/json")
	hookReq.Header.Add("X-Trace", "alpha")
	hookReq.Header.Add("X-Trace", "beta")
	hookRes, err := http.DefaultClient.Do(hookReq)
	if err != nil {
		t.Fatal(err)
	}
	hookBody := mustRead(t, hookRes)
	if hookRes.StatusCode != http.StatusOK {
		t.Fatalf("hook = %d %s", hookRes.StatusCode, hookBody)
	}
	var hook hookOK
	if err := json.Unmarshal([]byte(hookBody), &hook); err != nil {
		t.Fatal(err)
	}
	if !hook.OK || hook.Bin != id || len(hook.Request) != 8 {
		t.Fatalf("hook json = %+v", hook)
	}

	delReq, err := http.NewRequest(http.MethodDelete, srv.URL+"/hooks/"+id, nil)
	if err != nil {
		t.Fatal(err)
	}
	delRes, err := http.DefaultClient.Do(delReq)
	if err != nil {
		t.Fatal(err)
	}
	if delRes.StatusCode != http.StatusOK {
		t.Fatalf("delete hook = %d", delRes.StatusCode)
	}
	delRes.Body.Close()

	lockedAPI, err := http.Get(srv.URL + "/api/bins/" + id + "/requests")
	if err != nil {
		t.Fatal(err)
	}
	lockedBody := mustRead(t, lockedAPI)
	if lockedAPI.StatusCode != http.StatusUnauthorized || strings.Contains(lockedBody, `{"n":1}`) || strings.Contains(lockedBody, "alpha") {
		t.Fatalf("locked api = %d %s", lockedAPI.StatusCode, lockedBody)
	}

	apiReq, err := http.NewRequest(http.MethodGet, srv.URL+"/api/bins/"+id+"/requests", nil)
	if err != nil {
		t.Fatal(err)
	}
	apiReq.Header.Set("X-Bin-Key", key)
	apiRes, err := http.DefaultClient.Do(apiReq)
	if err != nil {
		t.Fatal(err)
	}
	apiBody := mustRead(t, apiRes)
	if apiRes.StatusCode != http.StatusOK {
		t.Fatalf("api = %d %s", apiRes.StatusCode, apiBody)
	}
	var payload struct {
		Requests []bin.Request `json:"requests"`
	}
	if err := json.Unmarshal([]byte(apiBody), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Requests) != 2 {
		t.Fatalf("requests = %#v", payload.Requests)
	}
	newest := payload.Requests[0]
	if newest.Method != http.MethodDelete || newest.Path != "/" {
		t.Fatalf("newest = %+v", newest)
	}
	older := payload.Requests[1]
	if older.Method != http.MethodPost || older.Path != "/foo?x=1" || older.Body != `{"n":1}` {
		t.Fatalf("older = %+v", older)
	}
	if older.ContentType != "application/json" || older.ContentLength != len(`{"n":1}`) {
		t.Fatalf("older meta = %+v", older)
	}
	if got := valuesNamed(older.Headers, "X-Trace"); len(got) != 2 || got[0] != "alpha" || got[1] != "beta" {
		t.Fatalf("trace headers = %#v", older.Headers)
	}
	if got := valuesNamed(older.Query, "x"); len(got) != 1 || got[0] != "1" {
		t.Fatalf("query = %#v", older.Query)
	}

	lockedPage, err := http.Get(srv.URL + "/bins/" + id)
	if err != nil {
		t.Fatal(err)
	}
	lockedPageBody := mustRead(t, lockedPage)
	if lockedPage.StatusCode != http.StatusOK || !strings.Contains(lockedPageBody, "Private bin") || strings.Contains(lockedPageBody, "&#34;n&#34;: 1") || strings.Contains(lockedPageBody, `{"n":1}`) {
		t.Fatalf("locked page = %d %s", lockedPage.StatusCode, lockedPageBody)
	}

	pageReq, err := http.NewRequest(http.MethodGet, srv.URL+"/bins/"+id, nil)
	pageReq.Host = "bin.example"
	pageReq.Header.Set("X-Forwarded-Proto", "https")
	pageReq.Header.Set("X-Bin-Key", key)
	if err != nil {
		t.Fatal(err)
	}
	pageRes, err := http.DefaultClient.Do(pageReq)
	if err != nil {
		t.Fatal(err)
	}
	pageBody := mustRead(t, pageRes)
	if pageRes.StatusCode != http.StatusOK {
		t.Fatalf("bin page = %d %s", pageRes.StatusCode, pageBody)
	}
	for _, want := range []string{id, "DELETE", "POST", "/foo?x=1", "https://bin.example/hooks/" + id, `href="/bins"`, "sidebar", "Pretty JSON", "Refresh", "Clear", "Delete", "local"} {
		if !strings.Contains(pageBody, want) {
			t.Fatalf("bin page missing %q\n%s", want, pageBody)
		}
	}
	for _, banned := range []string{"Sign in", "Replay", "Export", "Create Rule", "Save as Mock", "go-httpbin"} {
		if strings.Contains(pageBody, banned) {
			t.Fatalf("bin page includes %q", banned)
		}
	}
	if !strings.Contains(pageBody, "&#34;n&#34;: 1") {
		t.Fatalf("bin page missing pretty JSON\n%s", pageBody)
	}

	secondID, secondKey := createBin(t, noFollow, srv.URL)

	openList, err := http.Get(srv.URL + "/api/bins")
	if err != nil {
		t.Fatal(err)
	}
	openListBody := mustRead(t, openList)
	if openList.StatusCode != http.StatusOK || strings.Contains(openListBody, id) || strings.Contains(openListBody, secondID) {
		t.Fatalf("open bins api = %d %s", openList.StatusCode, openListBody)
	}

	listReq, err := http.NewRequest(http.MethodGet, srv.URL+"/api/bins", nil)
	if err != nil {
		t.Fatal(err)
	}
	listReq.Header.Set("X-Bin-Keys", `{"`+id+`":"`+key+`","`+secondID+`":"`+secondKey+`"}`)
	listRes, err := http.DefaultClient.Do(listReq)
	if err != nil {
		t.Fatal(err)
	}
	listBody := mustRead(t, listRes)
	if listRes.StatusCode != http.StatusOK {
		t.Fatalf("bins api = %d %s", listRes.StatusCode, listBody)
	}
	var listed struct {
		Bins []struct {
			ID       string `json:"id"`
			Requests int    `json:"requests"`
		} `json:"bins"`
	}
	if err := json.Unmarshal([]byte(listBody), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Bins) != 2 || listed.Bins[0].ID != secondID || listed.Bins[0].Requests != 0 {
		t.Fatalf("bins = %+v want newest %s", listed.Bins, secondID)
	}
	if listed.Bins[1].ID != id || listed.Bins[1].Requests != 2 {
		t.Fatalf("older bin = %+v", listed.Bins[1])
	}

	binsPage, err := http.Get(srv.URL + "/bins")
	if err != nil {
		t.Fatal(err)
	}
	binsPageBody := mustRead(t, binsPage)
	if binsPage.StatusCode != http.StatusOK || strings.Contains(binsPageBody, id) || strings.Contains(binsPageBody, secondID) {
		t.Fatalf("bins page leaked ids = %d %s", binsPage.StatusCode, binsPageBody)
	}

	ownedReq, err := http.NewRequest(http.MethodGet, srv.URL+"/bins", nil)
	if err != nil {
		t.Fatal(err)
	}
	ownedReq.Header.Set("X-Bin-Keys", `{"`+id+`":"`+key+`","`+secondID+`":"`+secondKey+`"}`)
	ownedPage, err := http.DefaultClient.Do(ownedReq)
	if err != nil {
		t.Fatal(err)
	}
	ownedBody := mustRead(t, ownedPage)
	if ownedPage.StatusCode != http.StatusOK || !strings.Contains(ownedBody, id) || !strings.Contains(ownedBody, secondID) {
		t.Fatalf("owned bins page = %d %s", ownedPage.StatusCode, ownedBody)
	}

	missing, err := http.Get(srv.URL + "/api/bins/deadbeef/requests")
	if err != nil {
		t.Fatal(err)
	}
	missingBody := mustRead(t, missing)
	if missing.StatusCode != http.StatusNotFound || !strings.Contains(missing.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("missing api = %d %s", missing.StatusCode, missingBody)
	}

	unknownHook, err := http.Post(srv.URL+"/hooks/deadbeef", "text/plain", strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	unknownBody := mustRead(t, unknownHook)
	if unknownHook.StatusCode != http.StatusNotFound || !strings.Contains(unknownBody, "bin not found") {
		t.Fatalf("unknown hook = %d %s", unknownHook.StatusCode, unknownBody)
	}

	missingPage, err := http.Get(srv.URL + "/bins/deadbeef")
	if err != nil {
		t.Fatal(err)
	}
	if missingPage.StatusCode != http.StatusNotFound {
		t.Fatalf("missing page = %d", missingPage.StatusCode)
	}
	missingPage.Body.Close()

	toolsPage, err := http.Get(srv.URL + "/tools")
	if err != nil {
		t.Fatal(err)
	}
	toolsBody := mustRead(t, toolsPage)
	if toolsPage.StatusCode != http.StatusOK {
		t.Fatalf("/tools = %d %s", toolsPage.StatusCode, toolsBody)
	}
	for _, want := range []string{
		"HTTP tools",
		`href="/httpbin/headers"`,
		"Status codes",
		"<code>{code}</code>",
		"<code>url</code>",
		"<code>status_code</code>",
		"<code>duration</code>",
		"<code>numbytes</code>",
		"<code>failure_rate</code>",
		`href="/bins"`,
	} {
		if !strings.Contains(toolsBody, want) {
			t.Fatalf("/tools missing %q", want)
		}
	}
	for _, banned := range []string{
		"No query parameters.",
		"Query (any)",
		"No name is special.",
		"<code>(any)</code> Sent back",
	} {
		if strings.Contains(toolsBody, banned) {
			t.Fatalf("/tools still contains %q", banned)
		}
	}
	if strings.Contains(toolsBody, "go-httpbin") || strings.Contains(toolsBody, "<h1>httpbin</h1>") || strings.Contains(toolsBody, "<title>httpbin") {
		t.Fatalf("/tools mentions the engine index or library name")
	}

	for _, path := range []string{"/httpbin", "/httpbin/"} {
		index, err := noFollow.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		indexBody := mustRead(t, index)
		if index.StatusCode != http.StatusSeeOther || index.Header.Get("Location") != "/tools" {
			t.Fatalf("%s = %d location %q %s", path, index.StatusCode, index.Header.Get("Location"), indexBody)
		}
		if strings.Contains(indexBody, "go-httpbin") || strings.Contains(indexBody, "<h1>httpbin") {
			t.Fatalf("%s still served the engine index", path)
		}
	}

	getRes, err := http.Get(srv.URL + "/httpbin/get")
	if err != nil {
		t.Fatal(err)
	}
	getBody := mustRead(t, getRes)
	if getRes.StatusCode != http.StatusOK {
		t.Fatalf("/httpbin/get = %d %s", getRes.StatusCode, getBody)
	}

	headersRes, err := http.Get(srv.URL + "/httpbin/headers")
	if err != nil {
		t.Fatal(err)
	}
	headersBody := mustRead(t, headersRes)
	if headersRes.StatusCode != http.StatusOK || !strings.Contains(headersBody, "headers") {
		t.Fatalf("/httpbin/headers = %d %s", headersRes.StatusCode, headersBody)
	}

	statusRes, err := http.Get(srv.URL + "/httpbin/status/418")
	if err != nil {
		t.Fatal(err)
	}
	statusBody := mustRead(t, statusRes)
	if statusRes.StatusCode != http.StatusTeapot {
		t.Fatalf("/httpbin/status/418 = %d %s", statusRes.StatusCode, statusBody)
	}

	_ = time.RFC3339
}

func TestPrettyAndHookResponse(t *testing.T) {
	got := prettyJSON("application/json; charset=utf-8", `{"hello":"world","n":1,"ok":true}`)
	want := "{\n  \"hello\": \"world\",\n  \"n\": 1,\n  \"ok\": true\n}"
	if got != want {
		t.Fatalf("pretty = %q", got)
	}
	if prettyJSON("text/plain", "hello") != "" {
		t.Fatal("plain text was pretty-printed")
	}
	if prettyJSON("application/json", "{") != "" {
		t.Fatal("invalid json was pretty-printed")
	}
	if prettyJSON("text/plain", "true") != "true" {
		t.Fatalf("json bool = %q", prettyJSON("text/plain", "true"))
	}
	head := rawHead("POST", "/v1", []bin.Field{{Name: "Content-Type", Value: "application/json"}})
	if head != "POST /v1 HTTP/1.1\nContent-Type: application/json\n\n" {
		t.Fatalf("head = %q", head)
	}
	var buf strings.Builder
	if err := json.NewEncoder(&buf).Encode(hookOK{OK: true, Bin: "abcd1234", Request: "deadbeef"}); err != nil {
		t.Fatal(err)
	}
	if hookBody("abcd1234", "deadbeef") != buf.String() {
		t.Fatalf("hook body = %q want %q", hookBody("abcd1234", "deadbeef"), buf.String())
	}
	if hookHead() != "HTTP/1.1 200 OK\nContent-Type: application/json; charset=utf-8\n\n" {
		t.Fatalf("hook head = %q", hookHead())
	}
}

func TestClearAndDeleteBin(t *testing.T) {
	srv := httptest.NewServer(newMux(bin.NewStore()))
	defer srv.Close()
	noFollow := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	id, key := createBin(t, noFollow, srv.URL)

	hook, err := http.Post(srv.URL+"/hooks/"+id, "text/plain", strings.NewReader("hello"))
	if err != nil {
		t.Fatal(err)
	}
	hook.Body.Close()

	denied, err := noFollow.Post(srv.URL+"/bins/"+id+"/clear", "application/x-www-form-urlencoded", nil)
	if err != nil {
		t.Fatal(err)
	}
	denied.Body.Close()
	if denied.StatusCode != http.StatusForbidden {
		t.Fatalf("clear without key = %d", denied.StatusCode)
	}

	clearRes, err := noFollow.Post(srv.URL+"/bins/"+id+"/clear", "application/x-www-form-urlencoded", strings.NewReader("key="+key))
	if err != nil {
		t.Fatal(err)
	}
	clearRes.Body.Close()
	if clearRes.StatusCode != http.StatusSeeOther || clearRes.Header.Get("Location") != "/bins/"+id {
		t.Fatalf("clear = %d %s", clearRes.StatusCode, clearRes.Header.Get("Location"))
	}
	apiReq, err := http.NewRequest(http.MethodGet, srv.URL+"/api/bins/"+id+"/requests", nil)
	if err != nil {
		t.Fatal(err)
	}
	apiReq.Header.Set("X-Bin-Key", key)
	api, err := http.DefaultClient.Do(apiReq)
	if err != nil {
		t.Fatal(err)
	}
	apiBody := mustRead(t, api)
	if api.StatusCode != http.StatusOK || !strings.Contains(apiBody, `"requests":[]`) && !strings.Contains(apiBody, `"requests": []`) {
		t.Fatalf("cleared = %d %s", api.StatusCode, apiBody)
	}

	delRes, err := noFollow.Post(srv.URL+"/bins/"+id+"/delete", "application/x-www-form-urlencoded", strings.NewReader("key="+key))
	if err != nil {
		t.Fatal(err)
	}
	delRes.Body.Close()
	if delRes.StatusCode != http.StatusSeeOther || delRes.Header.Get("Location") != "/bins" {
		t.Fatalf("delete = %d %s", delRes.StatusCode, delRes.Header.Get("Location"))
	}
	gone, err := http.Get(srv.URL + "/bins/" + id)
	if err != nil {
		t.Fatal(err)
	}
	gone.Body.Close()
	if gone.StatusCode != http.StatusNotFound {
		t.Fatalf("deleted page = %d", gone.StatusCode)
	}
	missing, err := noFollow.Post(srv.URL+"/bins/deadbeef/clear", "application/x-www-form-urlencoded", nil)
	if err != nil {
		t.Fatal(err)
	}
	missing.Body.Close()
	if missing.StatusCode != http.StatusNotFound {
		t.Fatalf("missing clear = %d", missing.StatusCode)
	}
}

func TestBinName(t *testing.T) {
	srv := httptest.NewServer(newMux(bin.NewStore()))
	defer srv.Close()
	noFollow := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	create, err := noFollow.Post(srv.URL+"/bins", "application/x-www-form-urlencoded", strings.NewReader("name=Stripe+webhooks"))
	if err != nil {
		t.Fatal(err)
	}
	loc := create.Header.Get("Location")
	create.Body.Close()
	if create.StatusCode != http.StatusSeeOther {
		t.Fatalf("create = %d %s", create.StatusCode, loc)
	}
	path, frag, _ := strings.Cut(loc, "#")
	id := strings.TrimPrefix(path, "/bins/")
	key := strings.TrimPrefix(frag, "k=")

	locked, err := http.Get(srv.URL + "/bins/" + id)
	if err != nil {
		t.Fatal(err)
	}
	lockedBody := mustRead(t, locked)
	if strings.Contains(lockedBody, "Stripe webhooks") {
		t.Fatalf("locked page includes the name\n%s", lockedBody)
	}

	pageReq, err := http.NewRequest(http.MethodGet, srv.URL+"/bins/"+id, nil)
	if err != nil {
		t.Fatal(err)
	}
	pageReq.Header.Set("X-Bin-Key", key)
	page, err := http.DefaultClient.Do(pageReq)
	if err != nil {
		t.Fatal(err)
	}
	pageBody := mustRead(t, page)
	if page.StatusCode != http.StatusOK || !strings.Contains(pageBody, "Stripe webhooks") || !strings.Contains(pageBody, `placeholder="Name this bin"`) {
		t.Fatalf("named page = %d %s", page.StatusCode, pageBody)
	}

	denied, err := noFollow.Post(srv.URL+"/bins/"+id+"/name", "application/x-www-form-urlencoded", strings.NewReader("name=Other"))
	if err != nil {
		t.Fatal(err)
	}
	denied.Body.Close()
	if denied.StatusCode != http.StatusForbidden {
		t.Fatalf("rename without key = %d", denied.StatusCode)
	}

	renameReq, err := http.NewRequest(http.MethodPost, srv.URL+"/bins/"+id+"/name", strings.NewReader("key="+key+"&name=Billing+hooks"))
	if err != nil {
		t.Fatal(err)
	}
	renameReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	renameReq.Header.Set("Accept", "application/json")
	rename, err := http.DefaultClient.Do(renameReq)
	if err != nil {
		t.Fatal(err)
	}
	renameBody := mustRead(t, rename)
	if rename.StatusCode != http.StatusOK || !strings.Contains(renameBody, `"name":"Billing hooks"`) {
		t.Fatalf("rename = %d %s", rename.StatusCode, renameBody)
	}

	listReq, err := http.NewRequest(http.MethodGet, srv.URL+"/api/bins", nil)
	if err != nil {
		t.Fatal(err)
	}
	listReq.Header.Set("X-Bin-Keys", `{"`+id+`":"`+key+`"}`)
	list, err := http.DefaultClient.Do(listReq)
	if err != nil {
		t.Fatal(err)
	}
	listBody := mustRead(t, list)
	if !strings.Contains(listBody, `"name":"Billing hooks"`) || !strings.Contains(listBody, id) {
		t.Fatalf("list = %s", listBody)
	}

	long, err := noFollow.Post(srv.URL+"/bins/"+id+"/name", "application/x-www-form-urlencoded", strings.NewReader("key="+key+"&name="+strings.Repeat("a", 41)))
	if err != nil {
		t.Fatal(err)
	}
	long.Body.Close()
	if long.StatusCode != http.StatusBadRequest {
		t.Fatalf("long name = %d", long.StatusCode)
	}
}

func createBin(t *testing.T, client *http.Client, srvURL string) (id, key string) {
	t.Helper()
	res, err := client.Post(srvURL+"/bins", "application/x-www-form-urlencoded", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	loc := res.Header.Get("Location")
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("create = %d location %q", res.StatusCode, loc)
	}
	path, frag, ok := strings.Cut(loc, "#")
	id = strings.TrimPrefix(path, "/bins/")
	key = strings.TrimPrefix(frag, "k=")
	if !ok || len(id) != 8 || len(key) != 32 {
		t.Fatalf("location %q", loc)
	}
	return id, key
}

func valuesNamed(fields []bin.Field, name string) []string {
	var out []string
	for _, f := range fields {
		if f.Name == name {
			out = append(out, f.Value)
		}
	}
	return out
}

func mustRead(t *testing.T, res *http.Response) string {
	t.Helper()
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

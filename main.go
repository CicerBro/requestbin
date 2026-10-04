package main

import (
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"requestbin/internal/bin"
	"requestbin/internal/httpbin"
)

//go:embed all:web
var embeddedWeb embed.FS

const binDataFile = "data/bins.json"

func main() {
	addr, err := resolveListenAddr(os.Args[1:], os.Getenv("PORT"), os.Stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		var cli *cliError
		if errors.As(err, &cli) {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(2)
	}
	ttl, err := binTTL()
	if err != nil {
		log.Fatal(err)
	}
	limits, err := binLimits()
	if err != nil {
		log.Fatal(err)
	}
	store, err := bin.OpenWithLimits(binDataFile, ttl, limits)
	if err != nil {
		log.Fatal(err)
	}
	srv := &http.Server{
		Addr:              addr,
		Handler:           newMux(store),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("requestbin ready on %s (bin ttl %s, max bins %d, requests per bin %d, body %d bytes, data %s)", addr, ttl, limits.MaxBins, limits.MaxRequestsPerBin, limits.MaxBodyBytes, binDataFile)
	log.Fatal(srv.ListenAndServe())
}

func binTTL() (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv("BIN_TTL"))
	if raw == "" {
		return bin.DefaultTTL, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("BIN_TTL: %w", err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("BIN_TTL must be positive, got %s", raw)
	}
	return d, nil
}

func binLimits() (bin.Limits, error) {
	limits := bin.DefaultLimits()
	var err error
	if limits.MaxBins, err = positiveIntEnv("MAX_BINS", limits.MaxBins); err != nil {
		return bin.Limits{}, err
	}
	if limits.MaxRequestsPerBin, err = positiveIntEnv("MAX_REQUESTS_PER_BIN", limits.MaxRequestsPerBin); err != nil {
		return bin.Limits{}, err
	}
	if limits.MaxBodyBytes, err = positiveIntEnv("MAX_BODY_BYTES", limits.MaxBodyBytes); err != nil {
		return bin.Limits{}, err
	}
	return limits, nil
}

func positiveIntEnv(name string, fallback int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	if n <= 0 {
		return 0, fmt.Errorf("%s must be positive, got %s", name, raw)
	}
	return n, nil
}

// cliError is a listen-address mistake. Flag parse failures are already
// printed by the flag package, so main prints this type only.
type cliError struct{ msg string }

func (e *cliError) Error() string { return e.msg }

// resolveListenAddr picks the listen address. Highest priority first:
// -addr, then -ip and/or -port when either was passed, then PORT, then :8080.
func resolveListenAddr(args []string, envPort string, out io.Writer) (string, error) {
	if out == nil {
		out = os.Stderr
	}
	fs := flag.NewFlagSet("requestbin", flag.ContinueOnError)
	fs.SetOutput(out)
	addrFlag := fs.String("addr", "", "listen address as host:port (for example 127.0.0.1:8082 or :9090)")
	ipFlag := fs.String("ip", "", "bind IP only; empty means all interfaces")
	portFlag := fs.String("port", "8080", "port number from 1 to 65535")
	fs.Usage = func() {
		fmt.Fprintf(out, `Usage of requestbin:

The listen address is chosen in this order:
  1. -addr, when that flag is set
  2. -ip and/or -port, when either flag is set
  3. the PORT environment variable, when set
  4. :8080

PORT may be a port number (PORT=9090 listens on :9090) or host:port
(PORT=127.0.0.1:9090). -addr cannot be combined with -ip or -port.

`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return "", err
	}

	addrSet := flagVisited(fs, "addr")
	ipSet := flagVisited(fs, "ip")
	portSet := flagVisited(fs, "port")
	if addrSet && (ipSet || portSet) {
		return "", &cliError{msg: "-addr cannot be combined with -ip or -port"}
	}
	if addrSet {
		addr := strings.TrimSpace(*addrFlag)
		if addr == "" {
			return "", &cliError{msg: "-addr requires a host:port listen address"}
		}
		return addr, nil
	}
	if ipSet || portSet {
		return joinListen(*ipFlag, *portFlag)
	}
	return listenAddrFromEnv(envPort), nil
}

func flagVisited(fs *flag.FlagSet, name string) bool {
	seen := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			seen = true
		}
	})
	return seen
}

func joinListen(ip, port string) (string, error) {
	p, err := parseListenPort(port)
	if err != nil {
		return "", err
	}
	ip = strings.TrimSpace(ip)
	if strings.HasPrefix(ip, "[") && strings.HasSuffix(ip, "]") && len(ip) >= 2 {
		ip = ip[1 : len(ip)-1]
	}
	return net.JoinHostPort(ip, p), nil
}

func parseListenPort(port string) (string, error) {
	port = strings.TrimSpace(port)
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 || !digitsOnly(port) {
		return "", &cliError{msg: fmt.Sprintf("invalid -port %q: must be a number from 1 to 65535", port)}
	}
	return strconv.Itoa(n), nil
}

func digitsOnly(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func listenAddrFromEnv(port string) string {
	if port == "" {
		return ":8080"
	}
	if strings.Contains(port, ":") {
		return port
	}
	return ":" + port
}

type binView struct {
	ID         string
	Name       string
	Created    string
	HookURL    string
	InspectURL string
}

type binSummaryView struct {
	ID       string
	Name     string
	Created  string
	Requests int
	Active   bool
}

type pageData struct {
	Shell    bool
	Bins     []binSummaryView
	HasBin   bool
	Locked   bool
	Bin      binView
	Requests []bin.Request
	Groups   []httpbin.Group
}

func newMux(store *bin.Store) http.Handler {
	mux := http.NewServeMux()
	// Mount httpbin before our /httpbin page so a subtree registration wins
	// only where it is more specific, and an exact duplicate panics early.
	httpbin.Mount(mux, "/httpbin")

	mux.Handle("GET /static/", staticServer{})
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/bins", http.StatusSeeOther)
	})
	mux.HandleFunc("POST /bins", func(w http.ResponseWriter, r *http.Request) {
		info, err := store.Create(r.FormValue("name"))
		if err != nil {
			if errors.Is(err, bin.ErrInvalidName) {
				http.Error(w, "name must be 40 characters or fewer", http.StatusBadRequest)
				return
			}
			log.Printf("create bin: %v", err)
			http.Error(w, "could not create bin", http.StatusInternalServerError)
			return
		}
		// The fragment stays in the browser and is not sent back on the next request.
		http.Redirect(w, r, "/bins/"+info.ID+"#k="+info.Key, http.StatusSeeOther)
	})
	mux.HandleFunc("GET /bins", func(w http.ResponseWriter, r *http.Request) {
		render(w, "bin.html", pageData{
			Shell: true,
			Bins:  binSummaries(store, r, ""),
		})
	})
	mux.HandleFunc("GET /bins/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		info, ok := store.Get(id)
		if !ok {
			http.Error(w, "bin not found", http.StatusNotFound)
			return
		}
		if err := store.Authorize(id, presentedKey(r, id)); err != nil {
			if errors.Is(err, bin.ErrNotFound) {
				http.Error(w, "bin not found", http.StatusNotFound)
				return
			}
			info.Name = ""
			data := shellData(store, r, info, nil)
			data.Locked = true
			data.Bin.Name = ""
			data.Requests = []bin.Request{}
			render(w, "bin.html", data)
			return
		}
		reqs, _ := store.ListRequests(id)
		render(w, "bin.html", shellData(store, r, info, reqs))
	})
	mux.HandleFunc("POST /bins/{id}/clear", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if err := store.Authorize(id, presentedKey(r, id)); err != nil {
			writeBinAccessError(w, err)
			return
		}
		if err := store.Clear(id); err != nil {
			if errors.Is(err, bin.ErrNotFound) {
				http.Error(w, "bin not found", http.StatusNotFound)
				return
			}
			log.Printf("clear bin %s: %v", id, err)
			http.Error(w, "could not clear bin", http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/bins/"+id, http.StatusSeeOther)
	})
	mux.HandleFunc("POST /bins/{id}/delete", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if err := store.Authorize(id, presentedKey(r, id)); err != nil {
			writeBinAccessError(w, err)
			return
		}
		if err := store.Delete(id); err != nil {
			if errors.Is(err, bin.ErrNotFound) {
				http.Error(w, "bin not found", http.StatusNotFound)
				return
			}
			log.Printf("delete bin %s: %v", id, err)
			http.Error(w, "could not delete bin", http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/bins", http.StatusSeeOther)
	})
	mux.HandleFunc("POST /bins/{id}/name", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if err := store.Authorize(id, presentedKey(r, id)); err != nil {
			writeBinAccessError(w, err)
			return
		}
		name, err := store.SetName(id, r.PostForm.Get("name"))
		if err != nil {
			if errors.Is(err, bin.ErrNotFound) {
				http.Error(w, "bin not found", http.StatusNotFound)
				return
			}
			if errors.Is(err, bin.ErrInvalidName) {
				http.Error(w, "name must be 40 characters or fewer", http.StatusBadRequest)
				return
			}
			log.Printf("rename bin %s: %v", id, err)
			http.Error(w, "could not rename bin", http.StatusInternalServerError)
			return
		}
		if wantsJSON(r) {
			writeJSON(w, http.StatusOK, nameBody{Name: name})
			return
		}
		http.Redirect(w, r, "/bins/"+id, http.StatusSeeOther)
	})
	mux.HandleFunc("GET /api/bins", func(w http.ResponseWriter, r *http.Request) {
		list := store.ListAuthorized(presentedKeys(r))
		body := binsBody{Bins: make([]binSummaryJSON, 0, len(list))}
		for _, item := range list {
			body.Bins = append(body.Bins, binSummaryJSON{
				ID:       item.ID,
				Name:     item.Name,
				Created:  item.Created.UTC().Format(time.RFC3339),
				Requests: item.Requests,
			})
		}
		writeJSON(w, http.StatusOK, body)
	})
	mux.HandleFunc("GET /api/bins/{id}/requests", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if err := store.Authorize(id, presentedKey(r, id)); err != nil {
			writeBinAccessJSON(w, err)
			return
		}
		reqs, ok := store.ListRequests(id)
		if !ok {
			writeJSON(w, http.StatusNotFound, errorBody{Error: "bin not found"})
			return
		}
		writeJSON(w, http.StatusOK, requestsBody{Requests: reqs})
	})
	mux.HandleFunc("/hooks/{id}", handleHook(store))
	mux.HandleFunc("/hooks/{id}/{rest...}", handleHook(store))
	tools := func(w http.ResponseWriter, r *http.Request) {
		render(w, "tools.html", pageData{
			Shell:  true,
			Bins:   binSummaries(store, r, ""),
			Groups: toolsGroups(),
		})
	}
	redirectTools := func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/tools", http.StatusSeeOther)
	}
	mux.HandleFunc("GET /tools", tools)
	mux.HandleFunc("GET /httpbin", redirectTools)
	mux.HandleFunc("GET /httpbin/{$}", redirectTools)
	return mux
}

func binSummaries(store *bin.Store, r *http.Request, active string) []binSummaryView {
	list := store.ListAuthorized(presentedKeys(r))
	out := make([]binSummaryView, 0, len(list))
	for _, item := range list {
		out = append(out, binSummaryView{
			ID:       item.ID,
			Name:     item.Name,
			Created:  item.Created.UTC().Format(time.RFC3339),
			Requests: item.Requests,
			Active:   item.ID == active,
		})
	}
	return out
}

func shellData(store *bin.Store, r *http.Request, info bin.Info, reqs []bin.Request) pageData {
	if reqs == nil {
		reqs = []bin.Request{}
	}
	scheme, host := publicSchemeHost(r)
	return pageData{
		Shell:    true,
		Bins:     binSummaries(store, r, info.ID),
		HasBin:   true,
		Requests: reqs,
		Bin: binView{
			ID:         info.ID,
			Name:       info.Name,
			Created:    info.Created.UTC().Format(time.RFC3339),
			HookURL:    fmt.Sprintf("%s://%s/hooks/%s", scheme, host, info.ID),
			InspectURL: fmt.Sprintf("%s://%s/bins/%s", scheme, host, info.ID),
		},
	}
}

func toolsGroups() []httpbin.Group {
	groups := httpbin.Catalog()
	for i := range groups {
		for j := range groups[i].Items {
			d := groups[i].Items[j].Description
			d = strings.ReplaceAll(d, "the go-httpbin service", "the service")
			d = strings.ReplaceAll(d, "go-httpbin", "this server")
			d = strings.ReplaceAll(d, "httpbin.org", "these HTTP tools")
			groups[i].Items[j].Description = d
		}
	}
	return groups
}

type requestsBody struct {
	Requests []bin.Request `json:"requests"`
}

type binsBody struct {
	Bins []binSummaryJSON `json:"bins"`
}

type binSummaryJSON struct {
	ID       string `json:"id"`
	Name     string `json:"name,omitempty"`
	Created  string `json:"created"`
	Requests int    `json:"requests"`
}

type nameBody struct {
	Name string `json:"name"`
}

type errorBody struct {
	Error string `json:"error"`
}

type hookOK struct {
	OK      bool   `json:"ok"`
	Bin     string `json:"bin"`
	Request string `json:"request"`
}

type hookErr struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
}

func handleHook(store *bin.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		rec, err := store.Capture(id, r)
		if errors.Is(err, bin.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, hookErr{OK: false, Error: "bin not found"})
			return
		}
		if err != nil {
			log.Printf("capture %s: %v", id, err)
			writeJSON(w, http.StatusInternalServerError, hookErr{OK: false, Error: "capture failed"})
			return
		}
		writeJSON(w, http.StatusOK, hookOK{OK: true, Bin: id, Request: rec.ID})
	}
}

func publicSchemeHost(r *http.Request) (string, string) {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if proto := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); proto != "" {
		scheme = strings.ToLower(strings.TrimSpace(strings.Split(proto, ",")[0]))
	}
	host := r.Host
	if host == "" {
		host = "localhost"
	}
	return scheme, host
}

func wantsJSON(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "application/json")
}

func presentedKeys(r *http.Request) map[string]string {
	out := make(map[string]string)
	if raw := strings.TrimSpace(r.Header.Get("X-Bin-Keys")); raw != "" {
		var parsed map[string]string
		if err := json.Unmarshal([]byte(raw), &parsed); err == nil {
			for id, key := range parsed {
				id = strings.TrimSpace(id)
				key = strings.TrimSpace(key)
				if id != "" && key != "" {
					out[id] = key
				}
			}
		}
	}
	if id := strings.TrimSpace(r.PathValue("id")); id != "" {
		if key := strings.TrimSpace(r.Header.Get("X-Bin-Key")); key != "" {
			out[id] = key
		}
	}
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err == nil {
			if id := strings.TrimSpace(r.PathValue("id")); id != "" {
				if key := strings.TrimSpace(r.PostForm.Get("key")); key != "" {
					out[id] = key
				}
			}
		}
	}
	return out
}

func presentedKey(r *http.Request, id string) string {
	return presentedKeys(r)[id]
}

func writeBinAccessError(w http.ResponseWriter, err error) {
	if errors.Is(err, bin.ErrNotFound) {
		http.Error(w, "bin not found", http.StatusNotFound)
		return
	}
	http.Error(w, "bin key required", http.StatusForbidden)
}

func writeBinAccessJSON(w http.ResponseWriter, err error) {
	if errors.Is(err, bin.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, errorBody{Error: "bin not found"})
		return
	}
	writeJSON(w, http.StatusUnauthorized, errorBody{Error: "bin key required"})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write json: %v", err)
	}
}

func render(w http.ResponseWriter, page string, data any) {
	fsys, err := webFilesystem()
	if err != nil {
		log.Printf("web fs: %v", err)
		http.Error(w, "web assets unavailable", http.StatusInternalServerError)
		return
	}
	if data == nil {
		data = pageData{}
	}
	root := template.New("root").Funcs(viewFuncs())
	t, err := root.ParseFS(fsys, "templates/layout.html", "templates/"+page)
	if err != nil {
		log.Printf("template %s: %v", page, err)
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "layout", data); err != nil {
		log.Printf("execute %s: %v", page, err)
	}
}

type staticServer struct{}

func (staticServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	fsys, err := webFilesystem()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	sub, err := fs.Sub(fsys, "static")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	http.StripPrefix("/static/", http.FileServer(http.FS(sub))).ServeHTTP(w, r)
}

// webFilesystem serves the on-disk web directory when templates are present
// so files written after the binary was built are picked up. The embedded
// copy is the fallback for a working directory that does not contain web/.
func webFilesystem() (fs.FS, error) {
	info, err := os.Stat("web/templates/layout.html")
	if err == nil && info.Mode().IsRegular() && info.Size() > 0 {
		return os.DirFS("web"), nil
	}
	return fs.Sub(embeddedWeb, "web")
}

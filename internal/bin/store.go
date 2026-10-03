// Package bin stores request bins in memory and, when opened with a path,
// mirrors them to a JSON file. Bins expire DefaultTTL after creation
// (override with BIN_TTL). The store keeps at most MaxBins bins and
// MaxRequestsPerBin captured requests on each bin, dropping the oldest.
package bin

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	// MaxBins is the maximum number of bins kept in memory.
	MaxBins = 200
	// MaxRequestsPerBin is the maximum number of captured requests kept per bin.
	MaxRequestsPerBin = 100
	// MaxBodyBytes is the maximum captured request body size (1 MiB).
	MaxBodyBytes = 1 << 20
	// DefaultTTL is how long a bin is kept after it is created.
	DefaultTTL = 48 * time.Hour
)

// ErrNotFound is returned when a bin id does not exist.
var ErrNotFound = errors.New("bin not found")

// Field is one header, query parameter, or form field.
// Duplicate names are preserved as separate entries.
type Field struct {
	Name  string `json:"Name"`
	Value string `json:"Value"`
}

// Request is one captured HTTP call, newest-friendly view for templates and JSON.
type Request struct {
	ID            string  `json:"ID"`
	Method        string  `json:"Method"`
	Path          string  `json:"Path"`
	Timestamp     string  `json:"Timestamp"`
	RemoteAddr    string  `json:"RemoteAddr"`
	ContentType   string  `json:"ContentType"`
	ContentLength int     `json:"ContentLength"`
	Headers       []Field `json:"Headers"`
	Query         []Field `json:"Query"`
	Body          string  `json:"Body"`
	Form          []Field `json:"Form"`
}

// Info identifies a bin.
type Info struct {
	ID      string
	Created time.Time
}

// Summary is a bin plus how many requests it currently holds.
type Summary struct {
	ID       string
	Created  time.Time
	Requests int
}

// Store holds bins in process memory and optionally a JSON file.
type Store struct {
	mu      sync.Mutex
	bins    map[string]*storedBin
	order   []string // oldest first
	maxBins int
	maxReqs int
	path    string
	ttl     time.Duration
	now     func() time.Time
}

type persistedFile struct {
	Bins []persistedBin `json:"bins"`
}

type persistedBin struct {
	ID       string    `json:"id"`
	Created  time.Time `json:"created"`
	Requests []Request `json:"requests"`
}

type storedBin struct {
	id       string
	created  time.Time
	requests []Request // oldest first
}

// NewStore returns an empty in-memory store with the default caps and TTL.
func NewStore() *Store {
	return &Store{
		bins:    make(map[string]*storedBin),
		maxBins: MaxBins,
		maxReqs: MaxRequestsPerBin,
		ttl:     DefaultTTL,
		now:     time.Now,
	}
}

// Open loads bins from path, drops those older than ttl, and saves on later changes.
// A missing file starts an empty store. ttl must be positive.
func Open(path string, ttl time.Duration) (*Store, error) {
	if ttl <= 0 {
		return nil, errors.New("bin ttl must be positive")
	}
	s := NewStore()
	s.path = path
	s.ttl = ttl
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

// Create allocates a new bin id (8 lowercase hex characters).
func (s *Store) Create() (Info, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.sweepLocked(); err != nil {
		return Info{}, err
	}

	for range 8 {
		id, err := newID()
		if err != nil {
			return Info{}, err
		}
		if _, exists := s.bins[id]; exists {
			continue
		}
		b := &storedBin{id: id, created: s.clock().UTC()}
		s.bins[id] = b
		s.order = append(s.order, id)
		s.evictBinsLocked()
		if err := s.saveLocked(); err != nil {
			if n := len(s.order); n > 0 && s.order[n-1] == id {
				s.order = s.order[:n-1]
			}
			delete(s.bins, id)
			return Info{}, err
		}
		return Info{ID: b.id, Created: b.created}, nil
	}
	return Info{}, errors.New("allocate bin id")
}

// List returns every bin, newest first, with its current request count.
func (s *Store) List() []Summary {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.sweepLocked()
	out := make([]Summary, 0, len(s.order))
	for i := len(s.order) - 1; i >= 0; i-- {
		b := s.bins[s.order[i]]
		if b == nil {
			continue
		}
		out = append(out, Summary{ID: b.id, Created: b.created, Requests: len(b.requests)})
	}
	return out
}

// Get returns a bin if it exists.
func (s *Store) Get(id string) (Info, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.sweepLocked()
	b, ok := s.bins[id]
	if !ok {
		return Info{}, false
	}
	return Info{ID: b.id, Created: b.created}, true
}

// ListRequests returns captured requests newest-first.
// The second result is false when the bin does not exist.
func (s *Store) ListRequests(id string) ([]Request, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.sweepLocked()
	b, ok := s.bins[id]
	if !ok {
		return nil, false
	}
	if len(b.requests) == 0 {
		return []Request{}, true
	}
	out := make([]Request, len(b.requests))
	for i := range b.requests {
		out[len(out)-1-i] = b.requests[i]
	}
	return out, true
}

// Capture records r against an existing bin.
// The stored path is the request URI with the /hooks/{id} prefix removed.
func (s *Store) Capture(id string, r *http.Request) (Request, error) {
	if _, ok := s.Get(id); !ok {
		return Request{}, ErrNotFound
	}
	rec, err := buildRequest(id, r)
	if err != nil {
		return Request{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.sweepLocked()
	b, ok := s.bins[id]
	if !ok {
		return Request{}, ErrNotFound
	}
	b.requests = append(b.requests, rec)
	if extra := len(b.requests) - s.maxReqs; extra > 0 {
		b.requests = append([]Request(nil), b.requests[extra:]...)
	}
	if err := s.saveLocked(); err != nil {
		return Request{}, err
	}
	return rec, nil
}

// Clear drops every captured request on a bin. The bin itself stays.
func (s *Store) Clear(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.sweepLocked(); err != nil {
		return err
	}
	b, ok := s.bins[id]
	if !ok {
		return ErrNotFound
	}
	prev := append([]Request(nil), b.requests...)
	b.requests = []Request{}
	if err := s.saveLocked(); err != nil {
		b.requests = prev
		return err
	}
	return nil
}

// Delete removes a bin and its captured requests.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.sweepLocked(); err != nil {
		return err
	}
	b, ok := s.bins[id]
	if !ok {
		return ErrNotFound
	}
	prevOrder := append([]string(nil), s.order...)
	next := make([]string, 0, len(s.order))
	for _, item := range s.order {
		if item != id {
			next = append(next, item)
		}
	}
	s.order = next
	delete(s.bins, id)
	if err := s.saveLocked(); err != nil {
		s.bins[id] = b
		s.order = prevOrder
		return err
	}
	return nil
}

func (s *Store) evictBinsLocked() {
	for len(s.order) > s.maxBins {
		oldest := s.order[0]
		s.order = s.order[1:]
		delete(s.bins, oldest)
	}
}

func (s *Store) clock() time.Time {
	if s.now == nil {
		return time.Now()
	}
	return s.now()
}

func (s *Store) expiredLocked(b *storedBin) bool {
	if s.ttl <= 0 || b == nil {
		return false
	}
	return !s.clock().Before(b.created.Add(s.ttl))
}

// sweepLocked drops expired bins and saves when the file changed.
func (s *Store) sweepLocked() error {
	kept := make([]string, 0, len(s.order))
	changed := false
	for _, id := range s.order {
		b := s.bins[id]
		if b == nil || s.expiredLocked(b) {
			delete(s.bins, id)
			changed = true
			continue
		}
		kept = append(kept, id)
	}
	if !changed {
		return nil
	}
	s.order = kept
	return s.saveLocked()
}

func (s *Store) load() error {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	var payload persistedFile
	if err := json.Unmarshal(data, &payload); err != nil {
		return fmt.Errorf("load bins: %w", err)
	}
	dirty := false
	for _, item := range payload.Bins {
		if item.ID == "" {
			dirty = true
			continue
		}
		if _, exists := s.bins[item.ID]; exists {
			dirty = true
			continue
		}
		reqs := item.Requests
		if reqs == nil {
			reqs = []Request{}
		}
		if len(reqs) > s.maxReqs {
			reqs = append([]Request(nil), reqs[len(reqs)-s.maxReqs:]...)
			dirty = true
		}
		for i := range reqs {
			reqs[i] = normalizeRequest(reqs[i])
		}
		b := &storedBin{id: item.ID, created: item.Created.UTC(), requests: reqs}
		if s.expiredLocked(b) {
			dirty = true
			continue
		}
		s.bins[item.ID] = b
		s.order = append(s.order, item.ID)
	}
	if len(s.order) > s.maxBins {
		dirty = true
	}
	s.evictBinsLocked()
	if dirty {
		if err := s.saveLocked(); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) saveLocked() error {
	if s.path == "" {
		return nil
	}
	payload := persistedFile{Bins: make([]persistedBin, 0, len(s.order))}
	for _, id := range s.order {
		b := s.bins[id]
		if b == nil {
			continue
		}
		reqs := b.requests
		if reqs == nil {
			reqs = []Request{}
		}
		payload.Bins = append(payload.Bins, persistedBin{
			ID:       b.id,
			Created:  b.created.UTC(),
			Requests: reqs,
		})
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "bins-*.json.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func normalizeRequest(r Request) Request {
	if r.Headers == nil {
		r.Headers = []Field{}
	}
	if r.Query == nil {
		r.Query = []Field{}
	}
	if r.Form == nil {
		r.Form = []Field{}
	}
	return r
}

func newID() (string, error) {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}

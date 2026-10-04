// Package bin stores request bins in memory and, when opened with a path,
// mirrors them to a JSON file. Bins expire DefaultTTL after creation
// (override with BIN_TTL). The store keeps at most MaxBins bins
// (MAX_BINS) and MaxRequestsPerBin captured requests on each bin
// (MAX_REQUESTS_PER_BIN), dropping the oldest. Captured bodies are
// limited to MaxBodyBytes (MAX_BODY_BYTES).
package bin

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
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
	// MaxNameRunes is the longest display name a bin can have.
	MaxNameRunes = 40
)

var (
	// ErrNotFound is returned when a bin id does not exist.
	ErrNotFound = errors.New("bin not found")
	// ErrForbidden is returned when a bin key does not match.
	ErrForbidden = errors.New("bin key rejected")
	// ErrInvalidName is returned when a display name is too long or has control characters.
	ErrInvalidName = errors.New("invalid bin name")
)

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

// Info identifies a bin. Key is set only by Create; it is the secret
// handed to the browser that made the bin.
type Info struct {
	ID      string
	Name    string
	Created time.Time
	Key     string
}

// Summary is a bin plus how many requests it currently holds.
type Summary struct {
	ID       string
	Name     string
	Created  time.Time
	Requests int
}

// Limits is how many bins and requests are kept, and how large a captured body may be.
// Every field must be positive. DefaultLimits matches the unset environment variables.
type Limits struct {
	MaxBins           int
	MaxRequestsPerBin int
	MaxBodyBytes      int
}

// DefaultLimits returns the built-in caps used when MAX_BINS, MAX_REQUESTS_PER_BIN,
// and MAX_BODY_BYTES are unset.
func DefaultLimits() Limits {
	return Limits{
		MaxBins:           MaxBins,
		MaxRequestsPerBin: MaxRequestsPerBin,
		MaxBodyBytes:      MaxBodyBytes,
	}
}

// Store holds bins in process memory and optionally a JSON file.
type Store struct {
	mu      sync.Mutex
	bins    map[string]*storedBin
	order   []string // oldest first
	maxBins int
	maxReqs int
	maxBody int
	path    string
	ttl     time.Duration
	now     func() time.Time
}

type persistedFile struct {
	Bins []persistedBin `json:"bins"`
}

type persistedBin struct {
	ID       string    `json:"id"`
	Key      string    `json:"key,omitempty"`
	Name     string    `json:"name,omitempty"`
	Created  time.Time `json:"created"`
	Requests []Request `json:"requests"`
}

type storedBin struct {
	id       string
	key      string
	name     string
	created  time.Time
	requests []Request // oldest first
}

// NewStore returns an empty in-memory store with the default caps and TTL.
func NewStore() *Store {
	limits := DefaultLimits()
	return &Store{
		bins:    make(map[string]*storedBin),
		maxBins: limits.MaxBins,
		maxReqs: limits.MaxRequestsPerBin,
		maxBody: limits.MaxBodyBytes,
		ttl:     DefaultTTL,
		now:     time.Now,
	}
}

// Open loads bins from path, drops those older than ttl, and saves on later changes.
// A missing file starts an empty store. ttl must be positive. Caps are DefaultLimits.
func Open(path string, ttl time.Duration) (*Store, error) {
	return OpenWithLimits(path, ttl, DefaultLimits())
}

// OpenWithLimits is Open with explicit caps. ttl and every limit must be positive.
func OpenWithLimits(path string, ttl time.Duration, limits Limits) (*Store, error) {
	if ttl <= 0 {
		return nil, errors.New("bin ttl must be positive")
	}
	if limits.MaxBins <= 0 || limits.MaxRequestsPerBin <= 0 || limits.MaxBodyBytes <= 0 {
		return nil, errors.New("bin limits must be positive")
	}
	s := NewStore()
	s.path = path
	s.ttl = ttl
	s.maxBins = limits.MaxBins
	s.maxReqs = limits.MaxRequestsPerBin
	s.maxBody = limits.MaxBodyBytes
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

// Create allocates a new bin id (8 lowercase hex characters) and a secret key.
// name may be empty. It is trimmed and limited to MaxNameRunes.
func (s *Store) Create(name string) (Info, error) {
	cleaned, err := CleanName(name)
	if err != nil {
		return Info{}, err
	}
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
		key, err := newKey()
		if err != nil {
			return Info{}, err
		}
		b := &storedBin{id: id, key: key, name: cleaned, created: s.clock().UTC()}
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
		return Info{ID: b.id, Name: b.name, Created: b.created, Key: key}, nil
	}
	return Info{}, errors.New("allocate bin id")
}

// Authorize reports whether key belongs to id.
func (s *Store) Authorize(id, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.sweepLocked(); err != nil {
		return err
	}
	b, ok := s.bins[id]
	if !ok {
		return ErrNotFound
	}
	if !keyMatch(b.key, key) {
		return ErrForbidden
	}
	return nil
}

// ListAuthorized returns bins whose keys match, newest first.
// Keys for unknown ids are ignored. Bins with no matching key are omitted.
func (s *Store) ListAuthorized(keys map[string]string) []Summary {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.sweepLocked()
	out := make([]Summary, 0, len(keys))
	for i := len(s.order) - 1; i >= 0; i-- {
		b := s.bins[s.order[i]]
		if b == nil {
			continue
		}
		if !keyMatch(b.key, keys[b.id]) {
			continue
		}
		out = append(out, Summary{ID: b.id, Name: b.name, Created: b.created, Requests: len(b.requests)})
	}
	return out
}

// List returns every bin, newest first, with its current request count.
// HTTP handlers must use ListAuthorized so a visitor only sees bins they hold keys for.
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
		out = append(out, Summary{ID: b.id, Name: b.name, Created: b.created, Requests: len(b.requests)})
	}
	return out
}

// Get returns a bin if it exists. The key is not included.
func (s *Store) Get(id string) (Info, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.sweepLocked()
	b, ok := s.bins[id]
	if !ok {
		return Info{}, false
	}
	return Info{ID: b.id, Name: b.name, Created: b.created}, true
}

// SetName changes the display name. An empty name clears it.
// The caller must already have authorized the key.
func (s *Store) SetName(id, name string) (string, error) {
	cleaned, err := CleanName(name)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.sweepLocked(); err != nil {
		return "", err
	}
	b, ok := s.bins[id]
	if !ok {
		return "", ErrNotFound
	}
	if b.name == cleaned {
		return cleaned, nil
	}
	prev := b.name
	b.name = cleaned
	if err := s.saveLocked(); err != nil {
		b.name = prev
		return "", err
	}
	return cleaned, nil
}

// CleanName trims a display name. Empty is valid and means unnamed.
func CleanName(name string) (string, error) {
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		return "", nil
	}
	if utf8.RuneCountInString(name) > MaxNameRunes {
		return "", ErrInvalidName
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", ErrInvalidName
		}
	}
	return name, nil
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
	rec, err := buildRequest(id, r, s.maxBody)
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
		key := item.Key
		if key == "" {
			var err error
			key, err = newKey()
			if err != nil {
				return err
			}
			dirty = true
		}
		b := &storedBin{id: item.ID, key: key, name: strings.TrimSpace(item.Name), created: item.Created.UTC(), requests: reqs}
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
			Key:      b.key,
			Name:     b.name,
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

func newKey() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}

func keyMatch(stored, given string) bool {
	if stored == "" || len(stored) != len(given) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(stored), []byte(given)) == 1
}

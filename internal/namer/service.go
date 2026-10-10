package namer

import (
	"context"
	"encoding/json"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// DefaultInterval is the least time between two names asked for one session.
// A session that keeps being prompted is renamed at most this often.
const DefaultInterval = 2 * time.Minute

// forgetAfter is how long a session's name is kept once it was last chosen,
// so the file does not grow with every session ever opened.
const forgetAfter = 7 * 24 * time.Hour

// backoff bounds for a session whose name could not be had.
const (
	firstBackoff = 30 * time.Second
	maxBackoff   = 10 * time.Minute
)

// queueSize is how many sessions may wait for a name. A full queue drops the
// request; the next poll asks again.
const queueSize = 64

// Options configures a Service.
type Options struct {
	Backend Backend
	// Interval is the least time between two names for one session.
	Interval time.Duration
	// Path is where names are remembered across restarts. Empty keeps them in
	// memory only.
	Path string
	Log  *slog.Logger
}

// Service hands out each session's current name at once and asks the model
// for a new one in the background when one is due. Only one request runs at a
// time, so a burst of sessions costs time, not parallel calls.
type Service struct {
	backend  Backend
	interval time.Duration
	path     string
	log      *slog.Logger
	now      func() time.Time

	mu      sync.Mutex
	entries map[string]*entry
	pending map[string]struct{}
	queue   chan job
}

// entry is what is known about one session's name.
type entry struct {
	Name string `json:"name"`
	// Turns is how many prompts the session had when the name was chosen, so a
	// new prompt is noticed without comparing text.
	Turns int       `json:"turns"`
	At    time.Time `json:"at"`

	failures int
	retryAt  time.Time
}

type job struct {
	session string
	turns   int
	req     Request
}

// New builds a service and loads the names an earlier run chose. Nothing
// unreadable stops it: remembering names is a convenience.
func New(opts Options) *Service {
	interval := opts.Interval
	if interval <= 0 {
		interval = DefaultInterval
	}

	log := opts.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	s := &Service{
		backend:  opts.Backend,
		interval: interval,
		path:     opts.Path,
		log:      log,
		now:      time.Now,
		entries:  make(map[string]*entry),
		pending:  make(map[string]struct{}),
		queue:    make(chan job, queueSize),
	}
	s.load()

	return s
}

// Name returns the session's current name, "" while it has none, and queues
// a request for a new one when it is due. It never blocks on the model.
// turns is how many prompts the session has had; req.Current and req.Taken
// are filled in by the caller.
func (s *Service) Name(session string, turns int, req Request) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	e := s.entries[session]
	if s.due(e, turns, req) {
		s.enqueue(session, turns, req)
	}

	if e == nil {
		return ""
	}

	return e.Name
}

// Backend names the backend in use, for logs.
func (s *Service) Backend() string { return s.backend.String() }

// Remembered returns the name chosen for a session, without asking for one.
func (s *Service) Remembered(session string) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if e := s.entries[session]; e != nil {
		return e.Name
	}

	return ""
}

// Run asks the model for the names queued, one at a time, until ctx ends.
func (s *Service) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case j := <-s.queue:
			s.serve(ctx, j)
		}
	}
}

// due decides whether a session should be named now. The caller holds mu.
func (s *Service) due(e *entry, turns int, req Request) bool {
	// Nothing to name it from yet: no prompt typed and no title chosen.
	if turns == 0 && req.Title == "" {
		return false
	}

	now := s.now()

	switch {
	case e == nil:
		return true
	case now.Before(e.retryAt):
		return false
	case e.Name == "":
		// Every earlier attempt failed; the backoff above has run out.
		return true
	default:
		return turns > e.Turns && now.Sub(e.At) >= s.interval
	}
}

func (s *Service) enqueue(session string, turns int, req Request) {
	if _, waiting := s.pending[session]; waiting {
		return
	}

	select {
	case s.queue <- job{session: session, turns: turns, req: req}:
		s.pending[session] = struct{}{}
	default:
		// Full: the next poll asks again.
	}
}

func (s *Service) serve(ctx context.Context, j job) {
	started := s.now()
	raw, err := s.backend.Name(ctx, j.req)
	name := Slug(raw)

	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.pending, j.session)

	e := s.entries[j.session]
	if e == nil {
		e = &entry{}
		s.entries[j.session] = e
	}

	if err == nil && name == "" {
		err = emptyNameError{raw: raw}
	}

	if err != nil {
		if ctx.Err() != nil {
			return
		}

		e.failures++
		e.retryAt = s.now().Add(backoffFor(e.failures))
		s.log.Warn("could not name an agent",
			"session", j.session, "backend", s.backend.String(),
			"failures", e.failures, "retry_in", backoffFor(e.failures).String(), "error", err)

		return
	}

	if name != e.Name {
		s.log.Info(
			"agent name chosen",
			"session",
			j.session,
			"old",
			e.Name,
			"new",
			name,
			"backend",
			s.backend.String(),
			"took",
			s.now().Sub(started).Round(time.Millisecond).String(),
		)
	}

	e.Name, e.Turns, e.At = name, j.turns, s.now()
	e.failures, e.retryAt = 0, time.Time{}

	s.saveLocked()
}

type emptyNameError struct{ raw string }

func (e emptyNameError) Error() string { return "the model's answer slugs to nothing: " + e.raw }

func backoffFor(failures int) time.Duration {
	d := firstBackoff
	for i := 1; i < failures && d < maxBackoff; i++ {
		d *= 2
	}

	return min(d, maxBackoff)
}

// load reads the names an earlier run chose.
func (s *Service) load() {
	if s.path == "" {
		return
	}

	raw, err := os.ReadFile(s.path)
	if err != nil {
		return
	}

	var stored map[string]*entry
	if json.Unmarshal(raw, &stored) != nil {
		return
	}

	for session, e := range stored {
		if e != nil && e.Name != "" {
			s.entries[session] = e
		}
	}
}

// saveLocked writes the names out through a temporary file, dropping those not
// chosen for a week. The caller holds mu; failure is silent.
func (s *Service) saveLocked() {
	if s.path == "" {
		return
	}

	cutoff := s.now().Add(-forgetAfter)
	kept := make(map[string]*entry, len(s.entries))

	for session, e := range maps.All(s.entries) {
		if e.Name != "" && e.At.After(cutoff) {
			kept[session] = e
		}
	}

	raw, err := json.MarshalIndent(kept, "", "  ")
	if err != nil {
		return
	}

	if os.MkdirAll(filepath.Dir(s.path), 0o700) != nil {
		return
	}

	tmp := s.path + ".tmp"
	if os.WriteFile(tmp, raw, 0o600) != nil {
		return
	}

	if os.Rename(tmp, s.path) != nil {
		_ = os.Remove(tmp)
	}
}

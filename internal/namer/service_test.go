package namer

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// fakeBackend answers with names in turn and counts the calls.
type fakeBackend struct {
	mu    sync.Mutex
	names []string
	err   error
	calls []Request
}

func (f *fakeBackend) String() string { return "fake" }

func (f *fakeBackend) Name(_ context.Context, req Request) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, req)
	if f.err != nil {
		return "", f.err
	}

	name := f.names[0]
	if len(f.names) > 1 {
		f.names = f.names[1:]
	}

	return name, nil
}

func (f *fakeBackend) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return len(f.calls)
}

// clock is a time the test moves by hand.
type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newService(backend Backend, path string) (*Service, *clock) {
	c := &clock{t: time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)}
	s := New(Options{Backend: backend, Interval: 2 * time.Minute, Path: path})
	s.now = c.now

	return s, c
}

// drain serves whatever is queued, as Run would.
func drain(s *Service) {
	for {
		select {
		case j := <-s.queue:
			s.serve(context.Background(), j)
		default:
			return
		}
	}
}

const sess = "8852bfe0-8b24-4a23-a35e-7521d04da061"

func TestASessionWithNothingTypedIsNotNamed(t *testing.T) {
	t.Parallel()

	b := &fakeBackend{names: []string{"x"}}
	s, _ := newService(b, "")

	s.Name(sess, "", Request{})
	drain(s)

	if b.count() != 0 {
		t.Errorf("calls = %d, want 0", b.count())
	}
}

func TestANameIsAskedForOnceAndReturnedWithoutBlocking(t *testing.T) {
	t.Parallel()

	b := &fakeBackend{names: []string{"Fix Search"}}
	s, _ := newService(b, "")

	if got := s.Name(sess, "1", Request{}); got != "" {
		t.Errorf("before the model answered: %q", got)
	}

	s.Name(sess, "1", Request{}) // a second poll before the answer queues nothing more
	drain(s)

	if got := s.Name(sess, "1", Request{}); got != "fix-search" {
		t.Errorf("after: %q, want the slug", got)
	}

	drain(s)

	if b.count() != 1 {
		t.Errorf("calls = %d, want 1", b.count())
	}
}

func TestANewPromptRenamesOnlyAfterTheInterval(t *testing.T) {
	t.Parallel()

	b := &fakeBackend{names: []string{"first", "second"}}
	s, c := newService(b, "")

	s.Name(sess, "1", Request{})
	drain(s)

	c.t = c.t.Add(time.Minute)

	s.Name(sess, "2", Request{})
	drain(s)

	if b.count() != 1 {
		t.Fatalf("renamed inside the interval: calls = %d", b.count())
	}

	c.t = c.t.Add(2 * time.Minute)

	s.Name(sess, "2", Request{})
	drain(s)

	if got := s.Name(sess, "2", Request{}); got != "second" {
		t.Errorf("got %q, want second", got)
	}

	c.t = c.t.Add(10 * time.Minute)

	s.Name(sess, "2", Request{}) // no new prompt: no new name
	drain(s)

	if b.count() != 2 {
		t.Errorf("calls = %d, want 2", b.count())
	}
}

func TestAFailureBacksOffAndKeepsTheOldName(t *testing.T) {
	t.Parallel()

	b := &fakeBackend{names: []string{"first"}}
	s, c := newService(b, "")
	s.Name(sess, "1", Request{})
	drain(s)

	b.err = errors.New("rate limited")
	c.t = c.t.Add(3 * time.Minute)

	s.Name(sess, "2", Request{})
	drain(s)

	c.t = c.t.Add(10 * time.Second)
	if got := s.Name(sess, "2", Request{}); got != "first" {
		t.Errorf("got %q, want the old name kept", got)
	}

	drain(s)

	if b.count() != 2 {
		t.Errorf("retried inside the backoff: calls = %d", b.count())
	}

	b.err = nil
	b.names = []string{"second"}
	c.t = c.t.Add(time.Minute)

	s.Name(sess, "2", Request{})
	drain(s)

	if got := s.Remembered(sess); got != "second" {
		t.Errorf("after the backoff: %q", got)
	}
}

func TestNamesSurviveARestart(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "agent-names.json")

	b := &fakeBackend{names: []string{"fix-search"}}
	first, _ := newService(b, path)
	first.Name(sess, "1", Request{})
	drain(first)

	second, _ := newService(b, path)
	if got := second.Remembered(sess); got != "fix-search" {
		t.Errorf("after restart: %q", got)
	}

	second.Name(sess, "1", Request{})
	drain(second)

	if b.count() != 1 {
		t.Errorf("a restart asked again: calls = %d", b.count())
	}
}

func TestAForcedNameIsAskedForAtOnceThroughIntervalAndBackoff(t *testing.T) {
	t.Parallel()

	b := &fakeBackend{names: []string{"first", "second", "third"}}
	s, c := newService(b, "")
	s.Name(sess, "1", Request{})
	drain(s)

	// Same inputs, same minute: nothing would be asked without the force.
	s.Name(sess, "1", Request{})
	drain(s)

	if b.count() != 1 {
		t.Fatalf("calls = %d before the force, want 1", b.count())
	}

	s.Force(sess)
	s.Name(sess, "1", Request{})
	drain(s)

	if got := s.Remembered(sess); got != "second" {
		t.Errorf("after the force: %q, want second", got)
	}

	// A failure puts the key in backoff, which a force also overrides, once.
	b.err = errors.New("rate limited")
	c.t = c.t.Add(3 * time.Minute)

	s.Name(sess, "2", Request{})
	drain(s)

	b.err = nil

	s.Force(sess)
	s.Name(sess, "2", Request{})
	drain(s)

	if got := s.Remembered(sess); got != "third" {
		t.Errorf("after the force in backoff: %q, want third", got)
	}

	s.Name(sess, "2", Request{})
	drain(s)

	if b.count() != 4 {
		t.Errorf("calls = %d, want 4: a force is spent once asked", b.count())
	}
}

func TestAForcedNameSurvivesARestartThatCutsItShort(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "agent-names.json")

	b := &fakeBackend{names: []string{"first", "second"}}
	first, _ := newService(b, path)
	first.Name(sess, "1", Request{})
	drain(first)

	// Asked for, then the instance is replaced before the model answers.
	first.Force(sess)
	first.Name(sess, "1", Request{})

	second, _ := newService(b, path)
	second.Name(sess, "1", Request{})
	drain(second)

	if got := second.Remembered(sess); got != "second" {
		t.Errorf("after the restart: %q, want the forced name asked for again", got)
	}

	second.Name(sess, "1", Request{})
	drain(second)

	if b.count() != 2 {
		t.Errorf("calls = %d, want 2: the force is spent once answered", b.count())
	}
}

func TestAForcedNameThatFailsIsAskedAgainAfterTheBackoff(t *testing.T) {
	t.Parallel()

	b := &fakeBackend{names: []string{"first"}}
	s, c := newService(b, "")
	s.Name(sess, "1", Request{})
	drain(s)

	b.err = errors.New("rate limited")

	s.Force(sess)
	s.Name(sess, "1", Request{})
	drain(s)

	s.Name(sess, "1", Request{})
	drain(s)

	if b.count() != 2 {
		t.Fatalf("calls = %d, want 2: no retry inside the backoff", b.count())
	}

	b.err = nil
	b.names = []string{"second"}
	c.t = c.t.Add(time.Minute)

	s.Name(sess, "1", Request{})
	drain(s)

	if got := s.Remembered(sess); got != "second" {
		t.Errorf("after the backoff: %q, want the forced name asked for again", got)
	}
}

func TestForcingAKeyNeverNamedChangesNothing(t *testing.T) {
	t.Parallel()

	b := &fakeBackend{names: []string{"x"}}
	s, _ := newService(b, "")
	s.Force(sess)
	s.Name(sess, "", Request{})
	drain(s)

	if b.count() != 0 {
		t.Errorf("calls = %d, want 0: there is nothing to name from", b.count())
	}
}

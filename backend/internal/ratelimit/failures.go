// Package ratelimit throttles repeated failures from one source.
//
// It exists for the login endpoint. The per-account hold in the service layer
// stops somebody guessing at one person's password; it does nothing about one
// guess each against a hundred accounts, because no single account ever
// reaches its own limit. That is what this counts.
//
// Only failures are counted. A successful sign-in costs nothing, so a shift
// change where forty people sign in at once is unaffected, while somebody
// working through a password list is not.
package ratelimit

import (
	"sync"
	"time"
)

// Failures counts failed attempts per key inside a fixed window.
//
// The window is fixed rather than sliding: it is a few lines instead of a ring
// buffer per key, and the difference — that an attacker gets their full
// allowance again the instant a window rolls over — does not matter at these
// rates.
type Failures struct {
	mu      sync.Mutex
	max     int
	window  time.Duration
	maxKeys int
	entries map[string]*entry

	// now is injectable so the tests do not have to wait fifteen minutes.
	now func() time.Time
}

type entry struct {
	count int
	// until is when this key's window closes.
	until time.Time
}

// NewFailures builds a limiter allowing max failures per key per window.
//
// maxKeys bounds memory. Reaching it means either a distributed attack or a
// misconfiguration; see Allow for what happens then.
func NewFailures(max int, window time.Duration, maxKeys int) *Failures {
	if max < 1 {
		max = 1
	}
	if maxKeys < 1 {
		maxKeys = 10000
	}
	return &Failures{
		max:     max,
		window:  window,
		maxKeys: maxKeys,
		entries: make(map[string]*entry),
		now:     time.Now,
	}
}

// Allow reports whether a key may attempt again, and if not, how long until it
// may. It does not count the attempt; call Record when one fails.
func (f *Failures) Allow(key string) (bool, time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()

	e, ok := f.entries[key]
	if !ok {
		return true, 0
	}

	now := f.now()
	if !now.Before(e.until) {
		delete(f.entries, key)
		return true, 0
	}
	if e.count < f.max {
		return true, 0
	}
	return false, e.until.Sub(now)
}

// Record counts one failure against a key.
func (f *Failures) Record(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	now := f.now()
	if e, ok := f.entries[key]; ok && now.Before(e.until) {
		e.count++
		return
	}

	if len(f.entries) >= f.maxKeys {
		f.sweep(now)
		if len(f.entries) >= f.maxKeys {
			// Every slot is held by a live window. Refusing to track this key
			// lets it through unthrottled, which is the lesser harm: the
			// alternative is evicting somebody else's counter, which is
			// exactly what an attacker would flood the map to achieve.
			// Tracked() surfaces this so a deployment can be sized for it.
			return
		}
	}
	f.entries[key] = &entry{count: 1, until: now.Add(f.window)}
}

// Forget clears a key's counter. The login handler calls it on success, so a
// person who mistyped their password twice and then got it right is not
// carrying those two failures around for the rest of the window.
func (f *Failures) Forget(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.entries, key)
}

// Tracked returns how many keys are currently held, and the cap. A deployment
// sitting at the cap is not limiting anybody new.
func (f *Failures) Tracked() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sweep(f.now())
	return len(f.entries), f.maxKeys
}

// sweep drops closed windows. The caller holds the lock.
func (f *Failures) sweep(now time.Time) {
	for key, e := range f.entries {
		if !now.Before(e.until) {
			delete(f.entries, key)
		}
	}
}

package ratelimit

import (
	"strconv"
	"sync"
	"testing"
	"time"
)

// at pins the limiter's clock so the tests do not wait fifteen minutes.
func at(f *Failures, t time.Time) { f.now = func() time.Time { return t } }

func TestAllowsUntilTheLimitThenRefuses(t *testing.T) {
	start := time.Date(2027, 2, 15, 8, 0, 0, 0, time.UTC)
	f := NewFailures(3, 15*time.Minute, 100)
	at(f, start)

	for i := 0; i < 3; i++ {
		if ok, _ := f.Allow("10.0.0.5"); !ok {
			t.Fatalf("refused on attempt %d, before the limit was reached", i+1)
		}
		f.Record("10.0.0.5")
	}

	ok, retryAfter := f.Allow("10.0.0.5")
	if ok {
		t.Fatal("a fourth attempt was allowed after three failures")
	}
	if retryAfter <= 0 || retryAfter > 15*time.Minute {
		t.Fatalf("retry-after of %s is not inside the window", retryAfter)
	}
}

func TestOneClientDoesNotBlockAnother(t *testing.T) {
	// The whole point of keying on the client: an attacker must not be able to
	// lock the factory out by failing repeatedly from their own machine.
	f := NewFailures(2, time.Minute, 100)
	at(f, time.Now())

	f.Record("10.0.0.5")
	f.Record("10.0.0.5")

	if ok, _ := f.Allow("10.0.0.5"); ok {
		t.Fatal("the offending client was not throttled")
	}
	if ok, _ := f.Allow("10.0.0.9"); !ok {
		t.Fatal("a different client was throttled by somebody else's failures")
	}
}

func TestTheWindowCloses(t *testing.T) {
	start := time.Date(2027, 2, 15, 8, 0, 0, 0, time.UTC)
	f := NewFailures(2, 15*time.Minute, 100)
	at(f, start)

	f.Record("10.0.0.5")
	f.Record("10.0.0.5")
	if ok, _ := f.Allow("10.0.0.5"); ok {
		t.Fatal("not throttled after reaching the limit")
	}

	// A hold that never lifts is a lockout, not a throttle.
	at(f, start.Add(15*time.Minute+time.Second))
	if ok, _ := f.Allow("10.0.0.5"); !ok {
		t.Fatal("still throttled after the window closed")
	}
}

func TestForgetClearsTheCount(t *testing.T) {
	// Signing in correctly clears the slate, so two fumbled attempts before a
	// correct one are not carried around for the rest of the window.
	f := NewFailures(3, time.Minute, 100)
	at(f, time.Now())

	f.Record("10.0.0.5")
	f.Record("10.0.0.5")
	f.Forget("10.0.0.5")

	for i := 0; i < 3; i++ {
		if ok, _ := f.Allow("10.0.0.5"); !ok {
			t.Fatalf("throttled on attempt %d after a successful sign-in cleared the count", i+1)
		}
		f.Record("10.0.0.5")
	}
}

func TestMemoryIsBounded(t *testing.T) {
	start := time.Date(2027, 2, 15, 8, 0, 0, 0, time.UTC)
	f := NewFailures(1, time.Minute, 50)
	at(f, start)

	for i := 0; i < 500; i++ {
		f.Record("10.0.0." + strconv.Itoa(i))
	}

	held, cap := f.Tracked()
	if held > cap {
		t.Fatalf("holding %d keys against a cap of %d; a flood would grow without bound", held, cap)
	}

	// The keys already held keep their counters — evicting them is exactly
	// what an attacker would flood the map to achieve.
	if ok, _ := f.Allow("10.0.0.0"); ok {
		t.Fatal("the first offender's counter was evicted by later traffic")
	}

	// Once the windows close the map drains, rather than staying full forever.
	at(f, start.Add(2*time.Minute))
	if held, _ := f.Tracked(); held != 0 {
		t.Fatalf("%d keys survived their windows", held)
	}
}

func TestConcurrentUseIsSafe(t *testing.T) {
	// The limiter is shared by every request in flight, so this is the shape
	// it actually runs in. Meaningful under -race, which CI uses.
	f := NewFailures(5, time.Minute, 1000)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			key := "10.0.0." + strconv.Itoa(n%7)
			for j := 0; j < 20; j++ {
				f.Allow(key)
				f.Record(key)
				f.Tracked()
			}
		}(i)
	}
	wg.Wait()
}

func TestAZeroWindowStillRefuses(t *testing.T) {
	// NewFailures clamps a nonsensical maximum rather than letting a
	// misconfiguration disable the limiter silently.
	f := NewFailures(0, time.Minute, 10)
	at(f, time.Now())

	f.Record("10.0.0.5")
	if ok, _ := f.Allow("10.0.0.5"); ok {
		t.Fatal("a maximum of zero was treated as unlimited")
	}
}

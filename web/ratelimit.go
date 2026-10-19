package web

import (
	"sync"
	"time"
)

// RateLimits guard the public upload page. Requests count per minute; wrong
// codes per failWindow; uploads are chunks being received at the same time.
// A zero field takes its value from DefaultRateLimits.
type RateLimits struct {
	FailsPerIP    int // wrong codes from one address before it is blocked
	FailsTotal    int // wrong codes from everyone before codes are refused for all
	RequestsPerIP int // per minute
	RequestsTotal int // per minute
	UploadsPerIP  int
	UploadsTotal  int
}

// DefaultRateLimits are generous for people uploading a recording or two and
// make guessing an 80-bit code hopeless.
var DefaultRateLimits = RateLimits{
	FailsPerIP: 5, FailsTotal: 30,
	RequestsPerIP: 120, RequestsTotal: 1200,
	UploadsPerIP: 3, UploadsTotal: 10,
}

const (
	failWindow   = 10 * time.Minute
	ipBlockFor   = time.Hour
	allBlockFor  = 10 * time.Minute
	limiterIdle  = 2 * time.Hour // forget an address after this long
	requestEvery = time.Minute
)

func (l RateLimits) withDefaults() RateLimits {
	d := DefaultRateLimits
	for _, f := range []struct{ v, d *int }{
		{&l.FailsPerIP, &d.FailsPerIP}, {&l.FailsTotal, &d.FailsTotal},
		{&l.RequestsPerIP, &d.RequestsPerIP}, {&l.RequestsTotal, &d.RequestsTotal},
		{&l.UploadsPerIP, &d.UploadsPerIP}, {&l.UploadsTotal, &d.UploadsTotal},
	} {
		if *f.v <= 0 {
			*f.v = *f.d
		}
	}
	return l
}

// window counts events in fixed windows.
type window struct {
	start time.Time
	n     int
}

// add counts one event and reports the count in the current window.
func (w *window) add(now time.Time, length time.Duration) int {
	if now.Sub(w.start) >= length {
		w.start, w.n = now, 0
	}
	w.n++
	return w.n
}

// retry is how long until the window ends.
func (w *window) retry(now time.Time, length time.Duration) time.Duration {
	return max(time.Second, w.start.Add(length).Sub(now))
}

type ipState struct {
	reqs, fails  window
	blockedUntil time.Time
	uploads      int
	seen         time.Time
}

// limiter keeps the counts in memory: there is one replica, and a restart
// forgetting them is fine.
type limiter struct {
	cfg RateLimits
	now func() time.Time

	mu          sync.Mutex
	ips         map[string]*ipState
	reqs, fails window
	closedUntil time.Time // wrong codes from everyone: codes refused until then
	uploads     int
}

func newLimiter(cfg RateLimits) *limiter {
	return &limiter{cfg: cfg.withDefaults(), now: time.Now, ips: map[string]*ipState{}}
}

func (l *limiter) ip(addr string, now time.Time) *ipState {
	st := l.ips[addr]
	if st == nil {
		st = &ipState{}
		l.ips[addr] = st
	}
	st.seen = now
	return st
}

// request counts a request and reports how long to wait if over the limit.
func (l *limiter) request(addr string) (time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	st := l.ip(addr, now)
	if st.reqs.add(now, requestEvery) > l.cfg.RequestsPerIP {
		return st.reqs.retry(now, requestEvery), false
	}
	if l.reqs.add(now, requestEvery) > l.cfg.RequestsTotal {
		return l.reqs.retry(now, requestEvery), false
	}
	return 0, true
}

// blocked reports how long codes are refused from addr (0 = not blocked):
// after too many wrong codes from it, or from everyone.
func (l *limiter) blocked(addr string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	wait := l.closedUntil.Sub(now)
	if st := l.ips[addr]; st != nil {
		wait = max(wait, st.blockedUntil.Sub(now))
	}
	return max(0, wait)
}

// failed counts a wrong code from addr.
func (l *limiter) failed(addr string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if l.ip(addr, now).fails.add(now, failWindow) >= l.cfg.FailsPerIP {
		l.ips[addr].blockedUntil = now.Add(ipBlockFor)
	}
	if l.fails.add(now, failWindow) >= l.cfg.FailsTotal {
		l.closedUntil = now.Add(allBlockFor)
	}
}

// acquire takes an upload slot for addr; release gives it back.
func (l *limiter) acquire(addr string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	st := l.ip(addr, l.now())
	if st.uploads >= l.cfg.UploadsPerIP || l.uploads >= l.cfg.UploadsTotal {
		return false
	}
	st.uploads++
	l.uploads++
	return true
}

func (l *limiter) release(addr string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if st := l.ips[addr]; st != nil && st.uploads > 0 {
		st.uploads--
	}
	l.uploads = max(0, l.uploads-1)
}

// prune forgets addresses that have been quiet for a while.
func (l *limiter) prune() {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	for addr, st := range l.ips {
		if st.uploads == 0 && now.Sub(st.seen) > limiterIdle && now.After(st.blockedUntil) {
			delete(l.ips, addr)
		}
	}
}

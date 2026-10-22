package desktop

import (
	"bytes"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// aliveScript is added to the page: while a window is open it holds a connection to /_desktop/alive. A held
// connection and not a timer, since browsers slow timers down in a minimised window.
const aliveScript = `// Sermon Studio desktop: keeps the program running while this window is open.
new EventSource('/_desktop/alive');
`

// Presence knows whether the studio is in use: windows open (a connection to /_desktop/alive each) and
// requests being answered.
type Presence struct {
	mu       sync.Mutex
	windows  int
	requests int
	// since is when the studio was last in use (a window open or a request answered); idle is measured
	// from it.
	since time.Time
	now   func() time.Time
}

func NewPresence() *Presence {
	return &Presence{since: time.Now(), now: time.Now}
}

func (p *Presence) add(windows, requests int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.windows += windows
	p.requests += requests
	p.since = p.now()
}

// IdleFor is how long no window has been open and no request answered; 0 while either is going on.
func (p *Presence) IdleFor() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.windows > 0 || p.requests > 0 {
		return 0
	}
	return p.now().Sub(p.since)
}

// Handler serves the studio (next) with what the desktop adds: /_desktop/alive and its script in the page,
// and /_desktop/hello for a second start of the program. Every request counts as use.
func (p *Presence) Handler(token string, next http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /_desktop/alive", p.alive)
	mux.HandleFunc("GET /_desktop/alive.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		fmt.Fprint(w, aliveScript)
	})
	mux.HandleFunc("GET /_desktop/hello", hello(token))
	mux.Handle("/", withScript(next))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/_desktop/alive" {
			p.add(0, 1)
			defer p.add(0, -1)
		}
		mux.ServeHTTP(w, r)
	})
}

// alive is an event stream held open while a window is open, with a comment now and then so that nothing on
// the way closes it.
func (p *Presence) alive(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	p.add(1, 0)
	defer p.add(-1, 0)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	fmt.Fprint(w, ": open\n\n")
	flusher.Flush()
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			fmt.Fprint(w, ": open\n\n")
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

// withScript adds the alive script to the page (index.html), before </head>. Only a whole page is changed (a
// 200 HTML answer); everything else passes through as it is.
func withScript(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || (r.URL.Path != "/" && r.URL.Path != "/index.html") {
			next.ServeHTTP(w, r)
			return
		}
		r.Header.Del("Range")
		rec := &pageRecorder{header: http.Header{}, code: http.StatusOK}
		next.ServeHTTP(rec, r)
		body := rec.body.Bytes()
		if rec.code == http.StatusOK && strings.HasPrefix(rec.header.Get("Content-Type"), "text/html") {
			body = bytes.Replace(body, []byte("</head>"), []byte(`  <script src="/_desktop/alive.js"></script>`+"\n</head>"), 1)
			rec.header.Set("Content-Length", strconv.Itoa(len(body)))
		}
		for k, v := range rec.header {
			w.Header()[k] = v
		}
		w.WriteHeader(rec.code)
		w.Write(body)
	})
}

// pageRecorder keeps an answer to change it before it is sent.
type pageRecorder struct {
	header http.Header
	code   int
	body   bytes.Buffer
}

func (r *pageRecorder) Header() http.Header         { return r.header }
func (r *pageRecorder) WriteHeader(code int)        { r.code = code }
func (r *pageRecorder) Write(b []byte) (int, error) { return r.body.Write(b) }

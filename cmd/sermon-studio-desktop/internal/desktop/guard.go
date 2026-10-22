package desktop

import (
	"crypto/subtle"
	"net/http"
	"strconv"
)

// tokenHeader carries Settings.Token to /_desktop/hello.
const tokenHeader = "X-Sermon-Studio-Token"

// Guard lets through only requests to this machine's address: Host must be 127.0.0.1:<port> or
// localhost:<port>. A web page can point a name of its own at 127.0.0.1 (DNS rebinding), but its requests
// then carry that name. Posts from other sites are refused by the studio itself (cross-origin protection).
func Guard(port int, next http.Handler) http.Handler {
	p := strconv.Itoa(port)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "127.0.0.1:"+p && r.Host != "localhost:"+p {
			http.Error(w, "Sermon Studio only answers at http://127.0.0.1:"+p+"/", http.StatusMisdirectedRequest)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// hello answers a second start of the program that it is running, when it knows the token.
func hello(token string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get(tokenHeader)), []byte(token)) != 1 {
			http.Error(w, "wrong token", http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

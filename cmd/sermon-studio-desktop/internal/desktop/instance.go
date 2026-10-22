package desktop

import (
	"net/http"
	"strconv"
	"time"
)

// URL is the studio's address. Always 127.0.0.1, never localhost: they are different sites to the browser,
// which keeps language, theme and open years per site.
func URL(port int) string {
	return "http://127.0.0.1:" + strconv.Itoa(port) + "/"
}

// Running reports whether this program, started by this user, answers on the port.
func Running(port int, token string) bool {
	req, err := http.NewRequest(http.MethodGet, URL(port)+"_desktop/hello", nil)
	if err != nil {
		return false
	}
	req.Header.Set(tokenHeader, token)
	c := &http.Client{Timeout: 2 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusNoContent
}

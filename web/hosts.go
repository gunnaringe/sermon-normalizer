package web

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// A church on a host of its own (AppConfig.ChurchHost,
// "{church}.sermon-studio.app"): the church is the host's, never the one a
// request names, and the user must still be one of its members. The login
// (callbacks, invite links, the church picker) is on the host of the public
// URL (app.sermon-studio.app), and the session cookie is set on the parent
// domain, so one login covers every church host.

// churchHosts matches a church's host: one label, a church id, before suffix.
type churchHosts struct {
	suffix string // ".sermon-studio.app"
}

func parseChurchHost(pattern string) (*churchHosts, error) {
	rest, ok := strings.CutPrefix(pattern, "{church}.")
	if !ok || rest == "" || strings.ContainsAny(rest, "{}/:@ ") || rest != strings.ToLower(rest) || strings.HasSuffix(rest, ".") {
		return nil, fmt.Errorf("church host %q: write it as {church}.example.org", pattern)
	}
	return &churchHosts{suffix: "." + rest}, nil
}

// church is the church id of a host ("" and false when it is not a church host).
func (h *churchHosts) church(host string) (string, bool) {
	if hp, _, err := net.SplitHostPort(host); err == nil {
		host = hp
	}
	label, ok := strings.CutSuffix(host, h.suffix)
	if !ok || !reChurchID.MatchString(label) || reservedID(label) { // app., share.: the site's own hosts
		return "", false
	}
	return label, true
}

// host is a church's host.
func (h *churchHosts) host(id string) string { return id + h.suffix }

// domain is the domain the church hosts are under ("sermon-studio.app").
func (h *churchHosts) domain() string { return strings.TrimPrefix(h.suffix, ".") }

// churchURL is the address of a church's studio ("" without church hosts).
func (a *App) churchURL(id string) string {
	if a.hosts == nil || a.auth == nil {
		return ""
	}
	return a.auth.base.Scheme + "://" + a.hosts.host(id) + "/"
}

// hostChurch is the church of the request's host: ok is false when church
// hosts are off or the host is not a church's.
func (a *App) hostChurch(r *http.Request) (id string, ok bool) {
	if a.hosts == nil {
		return "", false
	}
	return a.hosts.church(r.Host)
}

// checkHosts checks that church hosts, the login and the cookie fit together.
func checkHosts(h *churchHosts, opt *OIDCOptions) error {
	if opt == nil {
		return errors.New("church hosts need the login (--oidc-issuer): it is what lets one login cover them")
	}
	base, err := url.Parse(opt.PublicURL)
	if err != nil {
		return err
	}
	d := strings.TrimPrefix(opt.CookieDomain, ".")
	under := func(host string) bool { return host == d || strings.HasSuffix(host, "."+d) }
	if d == "" || !under(base.Hostname()) || !under(h.domain()) {
		return fmt.Errorf("church hosts need --cookie-domain set to a domain both the public URL's host (%s) and the church hosts (*.%s) are under",
			base.Hostname(), h.domain())
	}
	if _, isChurch := h.church(base.Host); isChurch {
		return fmt.Errorf("the public URL's host (%s) would be a church's host: give the login a reserved name, like app", base.Host)
	}
	return nil
}

// sameSiteURL reports whether u is an address the login may send the browser
// back to: the login host, or a church host, with the public URL's scheme
// and port, and nothing else (no user info).
func (au *auth) sameSiteURL(u *url.URL) bool {
	if u.Scheme != au.base.Scheme || u.User != nil || u.Opaque != "" || u.Port() != au.base.Port() {
		return false
	}
	host := u.Hostname()
	if host != strings.ToLower(host) || strings.HasSuffix(host, ".") {
		return false
	}
	if host == au.base.Hostname() {
		return true
	}
	if au.hosts == nil {
		return false
	}
	_, ok := au.hosts.church(host)
	return ok
}

// hostGate is the church hosts' part of every request (with church hosts on):
// the login host serves the login, invites and the church picker, and sends
// its start page to the picker; a church host serves its church, and an
// unknown church gets a page that says so.
func (a *App) hostGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.hosts == nil {
			next.ServeHTTP(w, r)
			return
		}
		if id, ok := a.hostChurch(r); ok {
			if !a.hasChurch(id) && (r.URL.Path == "/" || r.URL.Path == "/index.html") {
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				w.WriteHeader(http.StatusNotFound)
				fmt.Fprintf(w, "No such church: %s.\nYour churches: %s\n", id, a.auth.url("churches.html"))
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			http.Redirect(w, r, a.auth.url("churches.html"), http.StatusFound)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// hasChurch reports whether a church with this id runs here.
func (a *App) hasChurch(id string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, t := range a.tenants {
		if t.id == id {
			return true
		}
	}
	return false
}

// hostReserved are names a church cannot have when it has a host of its own:
// they are, or may become, the site's own hosts.
var hostReserved = map[string]bool{"app": true, "share": true, "www": true, "id": true, "api": true, "auth": true, "login": true,
	"mail": true, "admin": true, "static": true, "status": true, "docs": true, "help": true}

// infoOf is a church as the studio lists it: with church hosts, with its address.
func (a *App) infoOf(t *tenant) TenantInfo {
	i := t.info()
	i.URL = a.churchURL(t.id)
	return i
}

package web

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// The site administrator's API (`sermon-studio admin`), served only on a Unix
// socket in the data dir (AdminSocket), never on the network: whoever can
// reach the socket can already read every file of the studio. Every change
// goes through the running studio, so it takes effect at once, with the same
// checks as in the studio.

// AdminSocket is where the admin API listens: <data-dir>/.admin.sock.
func AdminSocket(dataDir string) string { return filepath.Join(dataDir, ".admin.sock") }

// statusError is an error with the HTTP status it answers with.
type statusError struct {
	status int
	err    error
}

func (e statusError) Error() string { return e.err.Error() }
func (e statusError) Unwrap() error { return e.err }

func failWith(status int, format string, args ...any) error {
	return statusError{status, fmt.Errorf(format, args...)}
}

// writeStatusError answers with err's status, else 500.
func writeStatusError(w http.ResponseWriter, err error) {
	var se statusError
	if errors.As(err, &se) {
		writeError(w, se.status, se.err)
		return
	}
	writeError(w, http.StatusInternalServerError, err)
}

var reChurchID = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// addChurch makes a church in the data dir and starts it. id "" = made from the name.
func (a *App) addChurch(id, name string) (*tenant, error) {
	if a.cfg.DataDir == "" {
		return nil, failWith(http.StatusNotFound, "adding churches needs --data-dir")
	}
	a.createMu.Lock()
	defer a.createMu.Unlock()
	name, err := churchName(name)
	if err != nil {
		return nil, statusError{http.StatusBadRequest, err}
	}
	if id == "" {
		if id = tenantID(name); id == "" {
			return nil, failWith(http.StatusBadRequest, "the name needs letters or digits")
		}
	} else if !reChurchID.MatchString(id) || len(id) > 63 {
		return nil, failWith(http.StatusBadRequest, "the id %q is not valid: lowercase letters, digits and single hyphens, up to 63", id)
	}
	if reservedID(id) {
		return nil, failWith(http.StatusBadRequest, "the name %q is reserved: pick another", id)
	}
	if taken, ok := a.nameTaken(nil, name); ok {
		return nil, failWith(http.StatusConflict, "there is already a church called %q", taken)
	}
	dir := filepath.Join(a.cfg.DataDir, id)
	if _, err := os.Stat(dir); err == nil {
		return nil, failWith(http.StatusConflict, "the folder %s already exists", id)
	}
	cc := churchConfig{Name: name}
	t, err := a.start(id, dir, cc) // makes the folders
	if err == nil {
		err = writeChurch(dir, cc)
	}
	if err != nil {
		return nil, err
	}
	a.cfg.Base.Logger.Printf("added church %s (%s)", name, id)
	return t, nil
}

// renameChurch changes a church's display name.
func (a *App) renameChurch(t *tenant, name string) error {
	name, err := churchName(name)
	if err != nil {
		return statusError{http.StatusBadRequest, err}
	}
	if taken, ok := a.nameTaken(t, name); ok {
		return failWith(http.StatusConflict, "there is already a church called %q", taken)
	}
	return a.update(t, func(c *churchConfig) { c.Name = name })
}

// church is the church of an admin request's {church}.
func (a *App) church(r *http.Request) (*tenant, error) {
	id := r.PathValue("church")
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, t := range a.tenants {
		if t.id == id && t.dir != "" {
			return t, nil
		}
	}
	return nil, failWith(http.StatusNotFound, "no church %q", id)
}

// AdminHandler is the admin API, for AdminSocket only.
func (a *App) AdminHandler() http.Handler {
	mux := http.NewServeMux()
	handle := func(pattern string, h func(r *http.Request) (int, any, error)) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			status, v, err := h(r)
			if err != nil {
				writeStatusError(w, err)
				return
			}
			writeJSON(w, status, v)
		})
	}
	handle("GET /churches", a.adminChurches)
	handle("POST /churches", a.adminChurchCreate)
	handle("POST /churches/{church}/rename", a.adminChurchRename)
	handle("GET /churches/{church}/members", a.adminMembers)
	handle("POST /churches/{church}/invites", a.adminInvite)
	handle("POST /churches/{church}/members/{member}/remove", a.adminMemberRemove)
	return mux
}

// AdminChurch is a church as `sermon-studio admin church list` shows it.
type AdminChurch struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Members int    `json:"members"`
	Admins  int    `json:"admins"`
}

func (a *App) adminChurches(r *http.Request) (int, any, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := []AdminChurch{}
	for _, t := range a.tenants {
		c := AdminChurch{ID: t.id, Name: t.info().Name, Members: len(t.members)}
		for _, m := range t.members {
			if m.Role == RoleAdmin {
				c.Admins++
			}
		}
		out = append(out, c)
	}
	return http.StatusOK, out, nil
}

// AdminInvite is a new invite link.
type AdminInvite struct {
	Church  string    `json:"church"`
	Role    string    `json:"role"`
	Link    string    `json:"link"`
	Expires time.Time `json:"expires"`
}

// adminChurchCreate makes a church: POST /churches {"id", "name"}. With the
// login it also makes an invite for its first admin, valid for 7 days.
func (a *App) adminChurchCreate(r *http.Request) (int, any, error) {
	var q struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := decodeRequest(r, &q); err != nil {
		return 0, nil, statusError{http.StatusBadRequest, err}
	}
	t, err := a.addChurch(q.ID, q.Name)
	if err != nil {
		return 0, nil, err
	}
	out := map[string]any{"church": t.info()}
	if a.auth != nil {
		inv, err := a.newInvite(t, RoleAdmin, 7*24*time.Hour)
		if err != nil {
			return 0, nil, err
		}
		out["invite"] = inv
	}
	return http.StatusCreated, out, nil
}

func (a *App) adminChurchRename(r *http.Request) (int, any, error) {
	t, err := a.church(r)
	if err != nil {
		return 0, nil, err
	}
	var q struct {
		Name string `json:"name"`
	}
	if err := decodeRequest(r, &q); err != nil {
		return 0, nil, statusError{http.StatusBadRequest, err}
	}
	if err := a.renameChurch(t, q.Name); err != nil {
		return 0, nil, err
	}
	return http.StatusOK, t.info(), nil
}

func (a *App) adminMembers(r *http.Request) (int, any, error) {
	t, err := a.church(r)
	if err != nil {
		return 0, nil, err
	}
	if a.auth == nil {
		return 0, nil, errNoLogin
	}
	a.mu.Lock()
	members := slices.Clone(t.members)
	a.mu.Unlock()
	out := []memberView{}
	for _, m := range members {
		name, email := a.auth.user(m.userID)
		out = append(out, memberView{ID: memberID(m.userID), Name: name, Email: email, Role: m.Role, Joined: m.Joined, InvitedBy: m.InvitedBy})
	}
	return http.StatusOK, out, nil
}

var errNoLogin = failWith(http.StatusConflict, "the studio runs with --no-login: it has no members or invites")

// adminInvite makes an invite link: POST /churches/{church}/invites {"role", "seconds"}.
func (a *App) adminInvite(r *http.Request) (int, any, error) {
	t, err := a.church(r)
	if err != nil {
		return 0, nil, err
	}
	if a.auth == nil {
		return 0, nil, errNoLogin
	}
	var q struct {
		Role    string `json:"role"`
		Seconds int64  `json:"seconds"`
	}
	if err := decodeRequest(r, &q); err != nil {
		return 0, nil, statusError{http.StatusBadRequest, err}
	}
	inv, err := a.newInvite(t, q.Role, time.Duration(q.Seconds)*time.Second)
	if err != nil {
		return 0, nil, err
	}
	return http.StatusCreated, inv, nil
}

func (a *App) newInvite(t *tenant, role string, valid time.Duration) (AdminInvite, error) {
	token, iv, err := addInvite(t.dir, t.id, role, valid, "admin cli")
	if err != nil {
		return AdminInvite{}, statusError{http.StatusBadRequest, err}
	}
	return AdminInvite{Church: t.id, Role: iv.Role, Link: a.auth.url("invite.html") + "#" + token, Expires: iv.Expires}, nil
}

// adminMemberRemove takes a member out of a church: POST
// /churches/{church}/members/{member}/remove. {member} is the id `admin
// members` shows, or the member's email. A church keeps its last admin.
func (a *App) adminMemberRemove(r *http.Request) (int, any, error) {
	t, err := a.church(r)
	if err != nil {
		return 0, nil, err
	}
	if a.auth == nil {
		return 0, nil, errNoLogin
	}
	who := r.PathValue("member")
	var removed member
	err = a.changeMembers(t, func(list []member) ([]member, error) {
		var hits []int
		for i, m := range list {
			if _, email := a.auth.user(m.userID); memberID(m.userID) == who || (email != "" && strings.EqualFold(email, who)) {
				hits = append(hits, i)
			}
		}
		switch len(hits) {
		case 0:
			return nil, failWith(http.StatusNotFound, "no member %q in %s", who, t.id)
		case 1:
		default:
			return nil, failWith(http.StatusConflict, "%q is more than one member of %s: use the id", who, t.id)
		}
		removed = list[hits[0]]
		return slices.Delete(list, hits[0], hits[0]+1), nil
	})
	if err != nil {
		return 0, nil, err
	}
	name, _ := a.auth.user(removed.userID)
	a.cfg.Base.Logger.Printf("church %s: %s removed by the site administrator", t.id, name)
	return http.StatusOK, map[string]string{"removed": name}, nil
}

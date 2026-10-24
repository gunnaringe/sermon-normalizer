package web

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// A church's members are who may use it, with the login (login.go):
// <church>/members.json. An admin also changes the church's settings and who
// its members are (Settings → Members); a church always keeps one admin. A
// member joins by an invite link, made there or with `sermon-studio admin
// invite` (CreateInvite): <church>/invites.json keeps each invite's hash,
// role and expiry until it is used or revoked.

const (
	membersFile = "members.json"
	invitesFile = "invites.json"
	// RoleAdmin may also change who the church's members are; RoleMember uses the church.
	RoleAdmin  = "admin"
	RoleMember = "member"
)

type member struct {
	userID
	Role      string    `json:"role"`
	Joined    time.Time `json:"joined"`
	InvitedBy string    `json:"invitedBy,omitempty"` // who made the invite: a user's name, or "admin cli"
}

type invite struct {
	Hash      string    `json:"hash"` // of the token's secret part
	Role      string    `json:"role"`
	Created   time.Time `json:"created"`
	Expires   time.Time `json:"expires"`
	CreatedBy string    `json:"createdBy"`
}

// inviteMu keeps the server's own reads and writes of invites.json in turn;
// `sermon-studio admin` writes the file from another process, atomically.
var inviteMu sync.Mutex

func readMembers(dir string) ([]member, error) {
	var list []member
	return list, readJSONFile(filepath.Join(dir, membersFile), &list)
}

// isMember reports whether the user is one of the church's members; call
// with a.mu held.
func (t *tenant) isMember(id userID) bool {
	return slices.ContainsFunc(t.members, func(m member) bool { return m.userID == id })
}

// addInvite adds an invite to the church in dir and returns its token.
func addInvite(dir, church, role string, valid time.Duration, by string) (string, invite, error) {
	if role != RoleAdmin && role != RoleMember {
		return "", invite{}, fmt.Errorf("the role is %s or %s", RoleAdmin, RoleMember)
	}
	if valid <= 0 {
		return "", invite{}, errors.New("an invite must be valid for some time")
	}
	inviteMu.Lock()
	defer inviteMu.Unlock()
	list, err := readInvites(dir)
	if err != nil {
		return "", invite{}, err
	}
	now := time.Now().UTC()
	secret := randomToken()
	iv := invite{Hash: hashToken(secret), Role: role, Created: now, Expires: now.Add(valid), CreatedBy: by}
	if err := writeJSONFile(filepath.Join(dir, invitesFile), append(list, iv)); err != nil {
		return "", invite{}, err
	}
	return church + "." + secret, iv, nil
}

// readInvites reads a church's invites, without the expired ones; call with
// inviteMu held.
func readInvites(dir string) ([]invite, error) {
	var list []invite
	if err := readJSONFile(filepath.Join(dir, invitesFile), &list); err != nil {
		return nil, err
	}
	now := time.Now()
	return slices.DeleteFunc(list, func(iv invite) bool { return now.After(iv.Expires) }), nil
}

// id is how the studio names an invite: the start of its hash, which says
// nothing of the token.
func (iv invite) id() string { return iv.Hash[:16] }

// findInvite looks up a token: its church and invite. With take, the invite
// is used up (removed from invites.json).
func (a *App) findInvite(token string, take bool) (*tenant, invite, error) {
	errBad := errors.New("this invite link is not valid: it may have been used, or have expired. Ask for a new one")
	church, secret, ok := strings.Cut(token, ".")
	if !ok || secret == "" {
		return nil, invite{}, errBad
	}
	var t *tenant
	a.mu.Lock()
	for _, c := range a.tenants {
		if c.id == church && c.dir != "" {
			t = c
		}
	}
	a.mu.Unlock()
	if t == nil {
		return nil, invite{}, errBad
	}
	inviteMu.Lock()
	defer inviteMu.Unlock()
	list, err := readInvites(t.dir)
	if err != nil {
		return nil, invite{}, err
	}
	hash := hashToken(secret)
	for i, iv := range list {
		if subtle.ConstantTimeCompare([]byte(iv.Hash), []byte(hash)) != 1 {
			continue
		}
		if take {
			list = slices.Delete(list, i, i+1)
			if err := writeJSONFile(filepath.Join(t.dir, invitesFile), list); err != nil {
				return nil, invite{}, err
			}
		}
		return t, iv, nil
	}
	return nil, invite{}, errBad
}

// handleInvite tells the logged-in user what an invite is for: GET api/invite?token=.
func (a *App) handleInvite(w http.ResponseWriter, r *http.Request) {
	t, iv, err := a.findInvite(r.URL.Query().Get("token"), false)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	u := a.userOf(r)
	a.mu.Lock()
	already := t.isMember(u.id)
	a.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"church": t.info(), "role": iv.Role, "member": already})
}

// handleInviteAccept makes the logged-in user a member: POST api/invite {"token"}.
// A member who already is one keeps the higher of the two roles.
func (a *App) handleInviteAccept(w http.ResponseWriter, r *http.Request) {
	var q struct {
		Token string `json:"token"`
	}
	if err := decodeRequest(r, &q); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	u := a.userOf(r)
	if u.id == (userID{}) {
		writeError(w, http.StatusUnauthorized, errors.New("log in first"))
		return
	}
	t, iv, err := a.findInvite(q.Token, true)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	a.mu.Lock()
	list := slices.Clone(t.members)
	i := slices.IndexFunc(list, func(m member) bool { return m.userID == u.id })
	if i < 0 {
		list = append(list, member{userID: u.id, Role: iv.Role, Joined: time.Now().UTC(), InvitedBy: iv.CreatedBy})
	} else if iv.Role == RoleAdmin {
		list[i].Role = RoleAdmin
	}
	err = writeJSONFile(filepath.Join(t.dir, membersFile), list)
	if err == nil {
		t.members = list
	}
	a.mu.Unlock()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	a.cfg.Base.Logger.Printf("church %s: %s joined as %s", t.id, a.auth.userName(u.id), iv.Role)
	writeJSON(w, http.StatusOK, map[string]any{"church": t.info()})
}

// memberID is how the studio names a member: the start of a hash of who
// they are.
func memberID(id userID) string { return hashToken(id.key())[:16] }

// roleOf is the user's role in a church: admin without the login, else their
// role as a member ("" for none).
func (a *App) roleOf(u user, t *tenant) string {
	if u.open {
		return RoleAdmin
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, m := range t.members {
		if m.userID == u.id {
			return m.Role
		}
	}
	return ""
}

// adminOnly are the requests only a church's admins may make: its settings,
// and who its members are.
var adminOnly = func() *http.ServeMux {
	m := http.NewServeMux()
	for _, p := range []string{
		"PUT /api/church", "PUT /api/logo", "DELETE /api/logo", "PUT /api/settings", "PUT /api/processing",
		"POST /api/clips", "PUT /api/clips/defaults", "PUT /api/clips/{id}", "POST /api/clips/{id}/replace",
		"POST /api/clips/{id}/delete", "POST /api/sermon/clip/add", "PUT /api/shares/ended-days",
		"GET /api/members", "PUT /api/members/{id}", "POST /api/members/{id}/remove",
		"POST /api/invites", "POST /api/invites/{id}/revoke",
	} {
		m.HandleFunc(p, func(http.ResponseWriter, *http.Request) {})
	}
	return m
}()

// requireAdmin lets a member's request to change the church's settings or
// members go no further.
func (a *App) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, pattern := adminOnly.Handler(r); pattern != "" {
			if t, ok := a.tenantFor(r); ok && a.roleOf(a.userOf(r), t) != RoleAdmin {
				writeError(w, http.StatusForbidden, errors.New("only the church's admins can change this"))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// changeMembers changes a church's members and saves them; a church always
// keeps an admin.
func (a *App) changeMembers(t *tenant, change func([]member) ([]member, error)) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	list, err := change(slices.Clone(t.members))
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(list, func(m member) bool { return m.Role == RoleAdmin }) {
		return failWith(http.StatusConflict, "a church needs an admin: make someone else an admin first")
	}
	if err := writeJSONFile(filepath.Join(t.dir, membersFile), list); err != nil {
		return err
	}
	t.members = list
	return nil
}

type memberView struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Email     string    `json:"email,omitempty"`
	Role      string    `json:"role"`
	Joined    time.Time `json:"joined"`
	InvitedBy string    `json:"invitedBy,omitempty"`
	You       bool      `json:"you,omitempty"`
}

type inviteView struct {
	ID        string    `json:"id"`
	Role      string    `json:"role"`
	CreatedBy string    `json:"createdBy"`
	Created   time.Time `json:"created"`
	Expires   time.Time `json:"expires"`
}

// handleMembers lists a church's members and open invites: GET api/members.
func (a *App) handleMembers(w http.ResponseWriter, r *http.Request) {
	t, ok := a.memberTenant(w, r)
	if !ok {
		return
	}
	u := a.userOf(r)
	a.mu.Lock()
	members := slices.Clone(t.members)
	a.mu.Unlock()
	out := []memberView{}
	for _, m := range members {
		name, email := a.auth.user(m.userID)
		out = append(out, memberView{ID: memberID(m.userID), Name: name, Email: email, Role: m.Role, Joined: m.Joined,
			InvitedBy: m.InvitedBy, You: m.userID == u.id})
	}
	inviteMu.Lock()
	list, err := readInvites(t.dir)
	inviteMu.Unlock()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	invites := []inviteView{}
	for _, iv := range list {
		invites = append(invites, inviteView{ID: iv.id(), Role: iv.Role, CreatedBy: iv.CreatedBy, Created: iv.Created, Expires: iv.Expires})
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": out, "invites": invites})
}

// memberTenant is the church of a request about its members (always one with a folder).
func (a *App) memberTenant(w http.ResponseWriter, r *http.Request) (*tenant, bool) {
	t, ok := a.tenantFor(r)
	if !ok || t.dir == "" {
		writeError(w, http.StatusNotFound, errors.New("no such church"))
		return nil, false
	}
	return t, true
}

// handleMemberRole changes a member's role: PUT api/members/{id} {"role"}.
func (a *App) handleMemberRole(w http.ResponseWriter, r *http.Request) {
	t, ok := a.memberTenant(w, r)
	if !ok {
		return
	}
	var q struct {
		Role string `json:"role"`
	}
	if err := decodeRequest(r, &q); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if q.Role != RoleAdmin && q.Role != RoleMember {
		writeError(w, http.StatusBadRequest, fmt.Errorf("the role is %s or %s", RoleAdmin, RoleMember))
		return
	}
	a.memberChange(w, t, r.PathValue("id"), func(list []member, i int) []member {
		list[i].Role = q.Role
		return list
	})
}

// handleMemberRemove takes a member out of the church: POST api/members/{id}/remove.
func (a *App) handleMemberRemove(w http.ResponseWriter, r *http.Request) {
	t, ok := a.memberTenant(w, r)
	if !ok {
		return
	}
	a.memberChange(w, t, r.PathValue("id"), func(list []member, i int) []member { return slices.Delete(list, i, i+1) })
}

// handleLeave takes the user out of the church: POST api/members/leave.
func (a *App) handleLeave(w http.ResponseWriter, r *http.Request) {
	t, ok := a.memberTenant(w, r)
	if !ok {
		return
	}
	a.memberChange(w, t, memberID(a.userOf(r).id), func(list []member, i int) []member { return slices.Delete(list, i, i+1) })
}

func (a *App) memberChange(w http.ResponseWriter, t *tenant, id string, change func([]member, int) []member) {
	err := a.changeMembers(t, func(list []member) ([]member, error) {
		i := slices.IndexFunc(list, func(m member) bool { return memberID(m.userID) == id })
		if i < 0 {
			return nil, errNoMember
		}
		return change(list, i), nil
	})
	if err != nil {
		writeStatusError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

var errNoMember = failWith(http.StatusNotFound, "no such member")

// handleInviteCreate makes an invite link: POST api/invites {"role", "days"}.
// The token is in the answer, and never again.
func (a *App) handleInviteCreate(w http.ResponseWriter, r *http.Request) {
	t, ok := a.memberTenant(w, r)
	if !ok {
		return
	}
	var q struct {
		Role string `json:"role"`
		Days int    `json:"days"`
	}
	if err := decodeRequest(r, &q); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if q.Days < 1 || q.Days > 30 {
		writeError(w, http.StatusBadRequest, errors.New("an invite lasts from 1 to 30 days"))
		return
	}
	token, iv, err := addInvite(t.dir, t.id, q.Role, time.Duration(q.Days)*24*time.Hour, a.auth.userName(a.userOf(r).id))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"token": token,
		"invite": inviteView{ID: iv.id(), Role: iv.Role, CreatedBy: iv.CreatedBy, Created: iv.Created, Expires: iv.Expires}})
}

// handleInviteRevoke ends an invite before it is used: POST api/invites/{id}/revoke.
func (a *App) handleInviteRevoke(w http.ResponseWriter, r *http.Request) {
	t, ok := a.memberTenant(w, r)
	if !ok {
		return
	}
	inviteMu.Lock()
	defer inviteMu.Unlock()
	list, err := readInvites(t.dir)
	if err == nil {
		n := len(list)
		list = slices.DeleteFunc(list, func(iv invite) bool { return iv.id() == r.PathValue("id") })
		if len(list) == n {
			writeError(w, http.StatusNotFound, errors.New("no such invite: it may have been used, or have expired"))
			return
		}
		err = writeJSONFile(filepath.Join(t.dir, invitesFile), list)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

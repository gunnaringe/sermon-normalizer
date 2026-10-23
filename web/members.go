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
// <church>/members.json. A member joins by an invite link, made with
// `sermon-studio admin invite` (CreateInvite): <church>/invites.json keeps
// each invite's hash, role and expiry until it is used.

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

// CreateInvite makes an invite to a church in dataDir and returns its token
// (the church's id, a dot, and 256 random bits). It is for `sermon-studio
// admin`: the server sees it the moment someone opens the link.
func CreateInvite(dataDir, church, role string, valid time.Duration, by string) (string, error) {
	if role != RoleAdmin && role != RoleMember {
		return "", fmt.Errorf("the role is %s or %s", RoleAdmin, RoleMember)
	}
	if valid <= 0 {
		return "", errors.New("an invite must be valid for some time")
	}
	dir := filepath.Join(dataDir, church)
	if church == "" || church != filepath.Base(church) || strings.HasPrefix(church, ".") {
		return "", fmt.Errorf("no church %q", church)
	}
	if ids, err := churchFolders(dataDir); err != nil {
		return "", err
	} else if !slices.Contains(ids, church) {
		return "", fmt.Errorf("no church %q in %s", church, dataDir)
	}
	inviteMu.Lock()
	defer inviteMu.Unlock()
	var list []invite
	if err := readJSONFile(filepath.Join(dir, invitesFile), &list); err != nil {
		return "", err
	}
	now := time.Now().UTC()
	list = slices.DeleteFunc(list, func(iv invite) bool { return now.After(iv.Expires) })
	secret := randomToken()
	list = append(list, invite{Hash: hashToken(secret), Role: role, Created: now, Expires: now.Add(valid), CreatedBy: by})
	if err := writeJSONFile(filepath.Join(dir, invitesFile), list); err != nil {
		return "", err
	}
	return church + "." + secret, nil
}

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
	var list []invite
	if err := readJSONFile(filepath.Join(t.dir, invitesFile), &list); err != nil {
		return nil, invite{}, err
	}
	hash, now := hashToken(secret), time.Now()
	for i, iv := range list {
		if subtle.ConstantTimeCompare([]byte(iv.Hash), []byte(hash)) != 1 || now.After(iv.Expires) {
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

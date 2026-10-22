package web

import (
	"fmt"
	"net/http"
	"strings"
)

// Access control. The login proxy (Pocket ID's forward-auth) puts the user's
// groups in a request header (AppConfig.GroupsHeader), space-separated. A
// church is for the members of its group, groupOf(id): "sermon_studio_" and
// the id with underscores for hyphens, like Pocket ID's own group names;
// members of sermon_studio_admin see every church and can add churches. Without
// GroupsHeader there is no access control: everyone sees everything.
const (
	// GroupPrefix starts the name of every group the app looks at.
	GroupPrefix = "sermon_studio_"
	// AdminID is the group name (after the prefix) of the administrators.
	// No church may have this id, or its members would be administrators.
	AdminID = "admin"
)

// reservedID reports ids that cannot be churches: "default" is the church
// of --root, no church may have the administrators' group, and the first
// words of the page addresses (#/<church>/folder/...; old ones had no church)
// would be mistaken for a church. Nor can a church have a name Windows keeps for a device (nul).
func reservedID(id string) bool {
	return id == RootTenant || groupOf(id) == groupOf(AdminID) || reservedPages[id] || windowsDevice(id)
}

var reservedPages = map[string]bool{"folder": true, "folders": true, "recording": true, "recordings": true, "settings": true, "upload": true, "shares": true}

// groupOf is the group of a church: norkirken-melhus is for
// sermon_studio_norkirken_melhus.
func groupOf(id string) string { return GroupPrefix + strings.ReplaceAll(id, "-", "_") }

// user is who a request is from.
type user struct {
	open   bool // no access control
	admin  bool
	groups map[string]bool
}

// userOf reads the groups the login proxy sent with the request. The proxy
// must replace the header (Traefik removes the headers listed in
// authResponseHeaders before copying them from the auth response), so a
// client cannot send its own.
func (a *App) userOf(r *http.Request) user {
	if a.cfg.GroupsHeader == "" {
		return user{open: true, admin: true}
	}
	u := user{groups: map[string]bool{}}
	for _, g := range strings.Fields(r.Header.Get(a.cfg.GroupsHeader)) {
		u.groups[g] = true
	}
	u.admin = u.groups[groupOf(AdminID)]
	return u
}

// sees reports whether the user may use the church.
func (u user) sees(t *tenant) bool {
	return u.open || u.admin || u.groups[groupOf(t.id)]
}

// visible lists the churches the user sees, in the order of a.tenants.
func (a *App) visible(u user) []*tenant {
	a.mu.Lock()
	defer a.mu.Unlock()
	var list []*tenant
	for _, t := range a.tenants {
		if u.sees(t) {
			list = append(list, t)
		}
	}
	return list
}

// errNoChurch is what a user without any church is told.
func errNoChurch() error {
	return fmt.Errorf("you do not have access to any church: ask an administrator to add you to a %s<church> group", GroupPrefix)
}

package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func (l *loginApp) admin(method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://admin"+path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	l.app.AdminHandler().ServeHTTP(w, r)
	return w
}

// The site administrator adds a church and gets its first admin's invite;
// lists, invites and removes members; a church keeps its last admin.
func TestAdminAPI(t *testing.T) {
	l := newLoginApp(t)
	w := l.admin("POST", "/churches", `{"id":"kirke-c","name":"Kirke C"}`)
	made := decode[struct {
		Church TenantInfo
		Invite AdminInvite
	}](t, w)
	if w.Code != http.StatusCreated || made.Church.ID != "kirke-c" || made.Invite.Role != RoleAdmin ||
		!strings.HasPrefix(made.Invite.Link, "https://studio.example/invite.html#kirke-c.") {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	if d := time.Until(made.Invite.Expires); d < 6*24*time.Hour || d > 7*24*time.Hour {
		t.Errorf("first invite lasts %v", d)
	}
	admin := l.loginAs("admin-1")
	l.join(admin, made.Invite.Link[strings.Index(made.Invite.Link, "#")+1:])
	if w := l.do("GET", "/api/members?tenant=kirke-c", "", admin); w.Code != http.StatusOK {
		t.Errorf("the new church's admin: %d", w.Code)
	}

	w = l.admin("POST", "/churches/kirke-c/invites", `{"role":"member","seconds":3600}`)
	inv := decode[AdminInvite](t, w)
	if w.Code != http.StatusCreated || inv.Role != RoleMember {
		t.Fatalf("invite: %d %s", w.Code, w.Body.String())
	}
	member := l.loginAs("member-1")
	l.join(member, inv.Link[strings.Index(inv.Link, "#")+1:])

	list := decode[[]memberView](t, l.admin("GET", "/churches/kirke-c/members", ""))
	if len(list) != 2 || list[0].Email != "kari@example.org" {
		t.Errorf("members: %+v", list)
	}
	churches := decode[[]AdminChurch](t, l.admin("GET", "/churches", ""))
	if c := churches[len(churches)-1]; c.ID != "kirke-c" || c.Members != 2 || c.Admins != 1 {
		t.Errorf("churches: %+v", churches)
	}

	// By email when it is one member, else by id; the last admin stays.
	if w := l.admin("POST", "/churches/kirke-c/members/kari@example.org/remove", ""); w.Code != http.StatusConflict {
		t.Errorf("email of two members: %d %s", w.Code, w.Body.String())
	}
	adminID := memberID(userID{l.prov.srv.URL, "admin-1"})
	if w := l.admin("POST", "/churches/kirke-c/members/"+adminID+"/remove", ""); w.Code != http.StatusConflict ||
		!strings.Contains(w.Body.String(), "needs an admin") {
		t.Errorf("last admin: %d %s", w.Code, w.Body.String())
	}
	memberIDs := memberID(userID{l.prov.srv.URL, "member-1"})
	if w := l.admin("POST", "/churches/kirke-c/members/"+memberIDs+"/remove", ""); w.Code != http.StatusOK {
		t.Errorf("remove: %d %s", w.Code, w.Body.String())
	}
	if w := l.do("GET", "/api/config?tenant=kirke-c", "", member); w.Code != http.StatusNotFound {
		t.Errorf("removed member: %d", w.Code)
	}
	if w := l.admin("POST", "/churches/kirke-c/members/nobody/remove", ""); w.Code != http.StatusNotFound {
		t.Errorf("no such member: %d", w.Code)
	}
	if w := l.admin("POST", "/churches/nope/invites", `{"role":"member","seconds":60}`); w.Code != http.StatusNotFound {
		t.Errorf("no such church: %d", w.Code)
	}
}

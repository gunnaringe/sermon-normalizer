package web

import (
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// loginAs logs in as another user of the provider.
func (l *loginApp) loginAs(sub string) *http.Cookie {
	l.prov.sub = sub
	return l.session()
}

func (l *loginApp) join(c *http.Cookie, token string) {
	l.t.Helper()
	if w := l.do("POST", "/api/invite", `{"token":"`+token+`"}`, c); w.Code != http.StatusOK {
		l.t.Fatalf("join: %d %s", w.Code, w.Body.String())
	}
}

type membersList struct {
	Members []memberView
	Invites []inviteView
}

func TestMembersAndRoles(t *testing.T) {
	l := newLoginApp(t)
	admin := l.loginAs("admin-1")
	token, _ := CreateInvite(l.data, "kirke-a", RoleAdmin, time.Hour, "admin cli")
	l.join(admin, token)

	// The admin invites someone as a member, in the studio.
	w := l.do("POST", "/api/invites?tenant=kirke-a", `{"role":"member","days":7}`, admin)
	made := decode[struct {
		Token  string
		Invite inviteView
	}](t, w)
	if w.Code != http.StatusCreated || !strings.HasPrefix(made.Token, "kirke-a.") || made.Invite.Role != RoleMember || made.Invite.CreatedBy != "Kari Nordmann" {
		t.Fatalf("invite: %d %s", w.Code, w.Body.String())
	}
	if d := time.Until(made.Invite.Expires); d < 6*24*time.Hour || d > 7*24*time.Hour {
		t.Errorf("expires in %v", d)
	}
	m := decode[membersList](t, l.do("GET", "/api/members?tenant=kirke-a", "", admin))
	if len(m.Members) != 1 || !m.Members[0].You || len(m.Invites) != 1 || m.Invites[0].ID != made.Invite.ID {
		t.Errorf("members: %+v", m)
	}
	member := l.loginAs("member-1")
	l.join(member, made.Token)
	got := decode[struct{ Tenants []TenantInfo }](t, l.do("GET", "/api/tenants", "", member))
	if len(got.Tenants) != 1 || got.Tenants[0].Role != RoleMember {
		t.Errorf("member's churches: %+v", got)
	}

	// A member uses the church, but cannot change its settings or members.
	if w := l.do("GET", "/api/config?tenant=kirke-a", "", member); w.Code != http.StatusOK {
		t.Errorf("member using the church: %d", w.Code)
	}
	for _, req := range [][3]string{
		{"PUT", "/api/church", `{"name":"Mine"}`}, {"PUT", "/api/settings", `{}`}, {"PUT", "/api/processing", `null`},
		{"DELETE", "/api/logo", ""}, {"PUT", "/api/clips/defaults", `{}`}, {"POST", "/api/clips/x/delete", ""},
		{"GET", "/api/members", ""}, {"POST", "/api/invites", `{"role":"admin","days":1}`},
		{"PUT", "/api/members/" + memberID(userID{l.prov.srv.URL, "admin-1"}), `{"role":"member"}`},
	} {
		if w := l.do(req[0], req[1]+"?tenant=kirke-a", req[2], member); w.Code != http.StatusForbidden {
			t.Errorf("member %s %s: %d", req[0], req[1], w.Code)
		}
	}
	if w := l.do("PUT", "/api/church?tenant=kirke-a", `{"name":"Kirke A"}`, admin); w.Code != http.StatusOK {
		t.Errorf("admin renaming: %d %s", w.Code, w.Body.String())
	}

	// A church always keeps an admin.
	adminID := memberID(userID{l.prov.srv.URL, "admin-1"})
	memberIDs := memberID(userID{l.prov.srv.URL, "member-1"})
	for _, req := range [][2]string{{"PUT", "/api/members/" + adminID}, {"POST", "/api/members/" + adminID + "/remove"}, {"POST", "/api/members/leave"}} {
		if w := l.do(req[0], req[1]+"?tenant=kirke-a", `{"role":"member"}`, admin); w.Code != http.StatusConflict {
			t.Errorf("last admin %s %s: %d %s", req[0], req[1], w.Code, w.Body.String())
		}
	}
	// ... so make the member an admin first; then the first may step down.
	if w := l.do("PUT", "/api/members/"+memberIDs+"?tenant=kirke-a", `{"role":"admin"}`, admin); w.Code != http.StatusOK {
		t.Fatalf("making an admin: %d %s", w.Code, w.Body.String())
	}
	if w := l.do("PUT", "/api/members/"+adminID+"?tenant=kirke-a", `{"role":"member"}`, admin); w.Code != http.StatusOK {
		t.Errorf("stepping down: %d %s", w.Code, w.Body.String())
	}
	if w := l.do("GET", "/api/members?tenant=kirke-a", "", admin); w.Code != http.StatusForbidden {
		t.Errorf("a member again: %d", w.Code)
	}
	// The new admin removes the old one, who then has no church.
	if w := l.do("POST", "/api/members/"+adminID+"/remove?tenant=kirke-a", "", member); w.Code != http.StatusOK {
		t.Errorf("removing: %d %s", w.Code, w.Body.String())
	}
	if w := l.do("GET", "/api/config?tenant=kirke-a", "", admin); w.Code != http.StatusNotFound {
		t.Errorf("removed member: %d", w.Code)
	}
	if w := l.do("PUT", "/api/members/nobody?tenant=kirke-a", `{"role":"admin"}`, member); w.Code != http.StatusNotFound {
		t.Errorf("no such member: %d", w.Code)
	}
	if w := l.do("PUT", "/api/members/"+memberIDs+"?tenant=kirke-a", `{"role":"owner"}`, member); w.Code != http.StatusBadRequest {
		t.Errorf("no such role: %d", w.Code)
	}
}

func TestInvitesRevokeAndLeave(t *testing.T) {
	l := newLoginApp(t)
	admin := l.loginAs("admin-1")
	token, _ := CreateInvite(l.data, "kirke-a", RoleAdmin, time.Hour, "admin cli")
	l.join(admin, token)
	for _, days := range []string{"0", "31", "-1"} {
		if w := l.do("POST", "/api/invites?tenant=kirke-a", `{"role":"member","days":`+days+`}`, admin); w.Code != http.StatusBadRequest {
			t.Errorf("%s days: %d", days, w.Code)
		}
	}
	made := decode[struct {
		Token  string
		Invite inviteView
	}](t, l.do("POST", "/api/invites?tenant=kirke-a", `{"role":"member","days":1}`, admin))
	if w := l.do("POST", "/api/invites/"+made.Invite.ID+"/revoke?tenant=kirke-a", "", admin); w.Code != http.StatusOK {
		t.Fatalf("revoke: %d %s", w.Code, w.Body.String())
	}
	if w := l.do("POST", "/api/invites/"+made.Invite.ID+"/revoke?tenant=kirke-a", "", admin); w.Code != http.StatusNotFound {
		t.Errorf("revoke twice: %d", w.Code)
	}
	other := l.loginAs("other")
	if w := l.do("POST", "/api/invite", `{"token":"`+made.Token+`"}`, other); w.Code != http.StatusNotFound {
		t.Errorf("revoked invite used: %d", w.Code)
	}
	if m := decode[membersList](t, l.do("GET", "/api/members?tenant=kirke-a", "", admin)); len(m.Invites) != 0 {
		t.Errorf("invites left: %+v", m.Invites)
	}

	// A member leaves by themselves.
	made = decode[struct {
		Token  string
		Invite inviteView
	}](t, l.do("POST", "/api/invites?tenant=kirke-a", `{"role":"member","days":1}`, admin))
	l.join(other, made.Token)
	if w := l.do("POST", "/api/members/leave?tenant=kirke-a", "", other); w.Code != http.StatusOK {
		t.Errorf("leave: %d %s", w.Code, w.Body.String())
	}
	if w := l.do("GET", "/api/tenants", "", other); w.Code != http.StatusForbidden {
		t.Errorf("after leaving: %d", w.Code)
	}
}

// CreateInvite makes an invite to a church in dataDir, as `sermon-studio admin
// invite` does through the running studio, and returns its token.
func CreateInvite(dataDir, church, role string, valid time.Duration, by string) (string, error) {
	if church == "" || church != filepath.Base(church) || strings.HasPrefix(church, ".") {
		return "", fmt.Errorf("no church %q", church)
	}
	if ids, err := churchFolders(dataDir); err != nil {
		return "", err
	} else if !slices.Contains(ids, church) {
		return "", fmt.Errorf("no church %q in %s", church, dataDir)
	}
	token, _, err := addInvite(filepath.Join(dataDir, church), church, role, valid, by)
	return token, err
}

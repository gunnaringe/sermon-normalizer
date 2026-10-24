package main

import (
	"bytes"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/gunnaringe/sermon-studio/web"
)

// adminStudio starts a studio (without the login) on a temporary data dir,
// with its admin socket, as `sermon-studio serve` does.
func adminStudio(t *testing.T) string {
	t.Helper()
	// Short: a Unix socket's path has a limit of about 100 bytes.
	data, err := os.MkdirTemp("", "ss")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(data) })
	os.MkdirAll(filepath.Join(data, "kirke-a", "Taler"), 0o755)
	app, err := web.NewApp(web.AppConfig{Base: web.Config{CacheDir: t.TempDir(), Logger: log.New(&bytes.Buffer{}, "", 0)}, DataDir: data})
	if err != nil {
		t.Fatal(err)
	}
	l, err := listenAdmin(web.AdminSocket(data))
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: app.AdminHandler()}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close(); app.Close() })
	return data
}

// admin runs `sermon-studio admin <args>` and returns what it printed.
func admin(t *testing.T, data string, args ...string) (string, error) {
	t.Helper()
	var c cli
	var out bytes.Buffer
	p, err := newParser(&c, &out, &out, func(int) { t.Fatalf("exit: %s", out.String()) })
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := p.Parse(append([]string{"admin", "--data-dir", data}, args...))
	if err != nil {
		return "", err
	}
	c.Admin.out = &out
	err = ctx.Run(&c.Admin)
	return out.String(), err
}

func TestAdminChurches(t *testing.T) {
	data := adminStudio(t)
	out, err := admin(t, data, "church", "create", "kirke-b", "--name", "Kirke B")
	if err != nil || !strings.Contains(out, "Added Kirke B (kirke-b).") {
		t.Fatalf("create: %v %q", err, out)
	}
	if _, err := os.Stat(filepath.Join(data, "kirke-b", "Taler")); err != nil {
		t.Errorf("no folder: %v", err)
	}
	for _, bad := range [][]string{{"kirke-b", "--name", "Again"}, {"Kirke_C", "--name", "C"}, {"settings", "--name", "S"}, {"kirke-c", "--name", "Kirke B"}} {
		if _, err := admin(t, data, append([]string{"church", "create"}, bad...)...); err == nil {
			t.Errorf("create %v: no error", bad)
		}
	}
	if _, err := admin(t, data, "church", "rename", "kirke-b", "Kirke Bé"); err != nil {
		t.Errorf("rename: %v", err)
	}
	out, err = admin(t, data, "church", "list")
	if err != nil || !strings.Contains(out, "kirke-a") || !strings.Contains(out, "Kirke Bé") {
		t.Errorf("list: %v %q", err, out)
	}
	out, err = admin(t, data, "--json", "church", "list")
	if err != nil || !strings.Contains(out, `"id": "kirke-b"`) {
		t.Errorf("list --json: %v %q", err, out)
	}
	// Without the login there are no members or invites.
	if _, err := admin(t, data, "invite", "kirke-a"); err == nil || !strings.Contains(err.Error(), "--no-login") {
		t.Errorf("invite without the login: %v", err)
	}
	if _, err := admin(t, data, "church", "rename", "nope", "X"); err == nil || !strings.Contains(err.Error(), `no church "nope"`) {
		t.Errorf("rename of no church: %v", err)
	}
}

// A second studio on the same data dir is refused; a socket left behind is not in the way.
func TestAdminSocketOnePerDataDir(t *testing.T) {
	data := adminStudio(t)
	if _, err := listenAdmin(web.AdminSocket(data)); err == nil || !strings.Contains(err.Error(), "another studio") {
		t.Errorf("second studio: %v", err)
	}
	dead, _ := os.MkdirTemp("", "ss")
	defer os.RemoveAll(dead)
	os.WriteFile(web.AdminSocket(dead), nil, 0o600) // left behind
	l, err := listenAdmin(web.AdminSocket(dead))
	if err != nil {
		t.Fatalf("stale socket: %v", err)
	}
	defer l.Close()
	if st, err := os.Stat(web.AdminSocket(dead)); runtime.GOOS != "windows" && (err != nil || st.Mode().Perm() != 0o600) {
		t.Errorf("socket mode: %v %v", st, err)
	}
}

func TestAdminWithoutStudio(t *testing.T) {
	_, err := admin(t, t.TempDir(), "church", "list")
	if err == nil || !strings.Contains(err.Error(), "the studio is not running") {
		t.Errorf("%v", err)
	}
}

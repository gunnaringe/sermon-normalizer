package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/alecthomas/kong"
)

func parse(t *testing.T, args ...string) (*kong.Context, error) {
	t.Helper()
	var out bytes.Buffer
	p, err := newParser(&cli{}, &out, &out, func(int) { t.Fatalf("exit: %s", out.String()) })
	if err != nil {
		t.Fatal(err)
	}
	return p.Parse(args)
}

// The login is chosen, never left out: neither, or both, is an error.
func TestLoginMustBeChosen(t *testing.T) {
	for _, c := range []struct {
		args []string
		want string // in the error; "" = no error
	}{
		{[]string{"--data-dir", "d"}, "--oidc-issuer=STRING or --no-login"},
		{[]string{"--data-dir", "d", "--oidc-issuer", "https://id.example", "--no-login"}, "can't be used together"},
		{[]string{"--data-dir", "d", "--no-login", "--oidc-client-id", "x"}, "OIDC settings are given"},
		{[]string{"--data-dir", "d", "--oidc-isuer", "https://id.example"}, "did you mean"},
		{[]string{"--no-login"}, "--root or --data-dir"},
		{[]string{"--data-dir", "d", "--no-login"}, ""},
		{[]string{"serve", "--data-dir", "d", "--oidc-issuer", "https://id.example", "--oidc-client-id", "x"}, ""},
	} {
		_, err := parse(t, c.args...)
		if c.want == "" && err != nil || c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)) {
			t.Errorf("%v: %v, want %q", c.args, err, c.want)
		}
	}
}

// Settings come from SERMON_STUDIO_* too (a ConfigMap); a flag wins.
func TestSettingsFromEnvironment(t *testing.T) {
	t.Setenv("SERMON_STUDIO_NO_LOGIN", "true")
	t.Setenv("SERMON_STUDIO_DATA_DIR", "/from-env")
	t.Setenv("SERMON_STUDIO_PARALLEL_RUNS", "3")
	ctx, err := parse(t, "--parallel-runs", "2")
	if err != nil {
		t.Fatal(err)
	}
	s := ctx.Model.Target.Interface().(cli).Serve
	if ctx.Command() != "serve" || !s.NoLogin || s.DataDir != "/from-env" || s.ParallelRuns != 2 {
		t.Errorf("%s %+v", ctx.Command(), s)
	}
}

// A variable this version does not know is a warning, with the nearest name.
func TestUnknownEnvironmentWarns(t *testing.T) {
	var out bytes.Buffer
	p, _ := newParser(&cli{}, &out, &out, func(int) {})
	warnUnknownEnv(p, []string{"SERMON_STUDIO_OIDC_ISUER=x", "SERMON_STUDIO_NEXT_VERSION=1", "SERMON_STUDIO_DATA_DIR=/d", "HOME=/root",
		"SERMON_STUDIO_SERVICE_HOST=10.0.0.1", "SERMON_STUDIO_PORT=tcp://10.0.0.1:8080", "SERMON_STUDIO_PORT_8080_TCP_ADDR=10.0.0.1"}, &out)
	got := out.String()
	for _, want := range []string{"SERMON_STUDIO_OIDC_ISUER is not a setting of this version (did you mean SERMON_STUDIO_OIDC_ISSUER?)",
		"SERMON_STUDIO_NEXT_VERSION is not a setting of this version; ignored"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "DATA_DIR") || strings.Contains(got, "HOME") || strings.Contains(got, "SERVICE") || strings.Contains(got, "_PORT") {
		t.Errorf("warned about a known or foreign variable:\n%s", got)
	}
}

func TestAdminInviteFlags(t *testing.T) {
	if _, err := parse(t, "admin", "--data-dir", "d", "invite", "kirke", "--role", "owner"); err == nil ||
		!strings.Contains(err.Error(), "must be one of") {
		t.Errorf("role owner: %v", err)
	}
	ctx, err := parse(t, "admin", "invite", "--data-dir", "d", "kirke")
	if err != nil || ctx.Command() != "admin invite <church>" {
		t.Errorf("%v %v", ctx, err)
	}
}

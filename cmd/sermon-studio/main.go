// Command sermon-studio serves the web app: browse date folders, cut
// the sermon on a waveform, run and follow jobs, and import new recordings.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/alecthomas/kong"

	"github.com/gunnaringe/sermon-studio/normalizer"
	"github.com/gunnaringe/sermon-studio/web"
)

var version = "dev"

// cli is the command line. Every flag can also be set by an environment
// variable, SERMON_STUDIO_<FLAG> (SERMON_STUDIO_DATA_DIR), e.g. from a
// ConfigMap; a flag wins over the variable.
type cli struct {
	Version kong.VersionFlag `help:"Print the version and exit."`
	Serve   serveCmd         `cmd:"" default:"withargs" help:"Run the studio (the default command)."`
	Admin   adminCmd         `cmd:"" help:"The site administrator's tools, run where the studio runs."`
}

type serveCmd struct {
	Listen  string `default:":8080" help:"Address to listen on."`
	DataDir string `help:"Folder with a folder per church: <id>/Taler (sermons), <id>/Innboks (inbox), <id>/church.json (name and settings)."`
	Root    string `help:"A single church without a church folder: the folder with its date folders (instead of --data-dir; settings are not saved)."`
	Inbox   string `help:"With --root: the folder new recordings arrive in (optional)."`

	// The login must be chosen: the studio's own, or none at all. A missing
	// or misspelt setting never means open access.
	OIDCIssuer           string `name:"oidc-issuer" xor:"login" required:"" group:"Login (one of --oidc-issuer and --no-login is required)" help:"The OpenID Connect provider (its issuer URL): the studio's own login. A church is for its members, who join with an invite link."`
	NoLogin              bool   `xor:"login" required:"" group:"Login (one of --oidc-issuer and --no-login is required)" help:"No login and no access control: anyone who reaches the studio can use every church and add churches. Only for the desktop, a trusted network, or behind a login proxy that lets only the right people through."`
	OIDCClientID         string `name:"oidc-client-id" group:"Login (one of --oidc-issuer and --no-login is required)" help:"With --oidc-issuer: the studio's client id at the provider."`
	OIDCClientSecretFile string `name:"oidc-client-secret-file" type:"existingfile" group:"Login (one of --oidc-issuer and --no-login is required)" help:"With --oidc-issuer: a file with the client secret (none = a public client, PKCE only)."`
	PublicURL            string `group:"Login (one of --oidc-issuer and --no-login is required)" help:"The studio's address as the browser sees it (with --oidc-issuer, the callback is <public-url>/auth/callback)."`
	CookieDomain         string `group:"Login (one of --oidc-issuer and --no-login is required)" help:"With --oidc-issuer: a parent domain for the session cookie, shared by the studio's hosts (default: this host only)."`

	CacheDir     string `default:"${cacheDir}" help:"Where waveform data and previews are kept."`
	CacheDays    int    `default:"30" help:"Remove cached analyses not used for this many days."`
	TrashDays    int    `default:"${trashDays}" help:"Keep a deleted recording in the trash for this many days."`
	Layout       string `default:"${layout}" help:"Where imported recordings go under --root, as a Go time layout."`
	FFmpeg       string `name:"ffmpeg" default:"ffmpeg" help:"Path to ffmpeg."`
	ParallelRuns int    `default:"1" help:"How many runs (making the files) can work at once, across all churches; each uses about one core."`

	ShareListen        string `group:"Share page" help:"Address for the public share page, where recordings are sent and finished sermons downloaded with a code made in the studio (Shares, in the header); empty = off."`
	ShareURL           string `name:"share-url" group:"Share page" help:"The share page's public address (e.g. https://upload-sermons.example.org/), for share links."`
	ShareIPHeader      string `name:"share-ip-header" group:"Share page" help:"Header the proxy puts the client's address in (Cloudflare: CF-Connecting-IP); used for rate limits."`
	ShareFailsPerIP    int    `name:"share-fails-per-ip" default:"${failsPerIP}" group:"Share page" help:"Wrong codes from one address in 10 minutes before it is blocked for an hour."`
	ShareFailsTotal    int    `name:"share-fails-total" default:"${failsTotal}" group:"Share page" help:"Wrong codes from everyone in 10 minutes before codes are refused for 10 minutes."`
	ShareRequestsPerIP int    `name:"share-requests-per-ip" default:"${requestsPerIP}" group:"Share page" help:"Requests per minute to the share page from one address."`
	ShareRequestsTotal int    `name:"share-requests-total" default:"${requestsTotal}" group:"Share page" help:"Requests per minute to the share page from everyone."`
	ShareUploadsPerIP  int    `name:"share-uploads-per-ip" default:"${uploadsPerIP}" group:"Share page" help:"Chunks received at once from one address."`
	ShareUploadsTotal  int    `name:"share-uploads-total" default:"${uploadsTotal}" group:"Share page" help:"Chunks received at once from everyone."`
}

// Validate checks what kong cannot: the settings that belong together.
func (s *serveCmd) Validate() error {
	if s.Root == "" && s.DataDir == "" {
		return errors.New("--root or --data-dir is required")
	}
	if s.ParallelRuns < 1 {
		return errors.New("--parallel-runs must be at least 1")
	}
	if s.NoLogin && (s.OIDCClientID != "" || s.OIDCClientSecretFile != "" || s.CookieDomain != "") {
		return errors.New("--no-login, but OIDC settings are given: choose the login (--oidc-issuer) or none")
	}
	return nil
}

func main() {
	var c cli
	parser, err := newParser(&c, os.Stdout, os.Stderr, os.Exit)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	warnUnknownEnv(parser, os.Environ(), os.Stderr)
	ctx, err := parser.Parse(os.Args[1:])
	parser.FatalIfErrorf(err)
	parser.FatalIfErrorf(ctx.Run(&c.Admin)) // the admin commands' Run gets their common flags
}

// envPrefix starts every environment variable the studio reads.
const envPrefix = "SERMON_STUDIO"

func newParser(c *cli, stdout, stderr io.Writer, exit func(int)) (*kong.Kong, error) {
	return kong.New(c,
		kong.Name("sermon-studio"),
		kong.Description("Cut a church service recording on a waveform, get loudness-normalised FLAC, MP3 and Opus. "+
			"Every flag can also be set by an environment variable, "+envPrefix+"_<FLAG> (e.g. "+envPrefix+"_DATA_DIR)."),
		kong.DefaultEnvars(envPrefix),
		kong.Writers(stdout, stderr),
		kong.Exit(exit),
		kong.Vars{
			"version":       "sermon-studio " + version,
			"cacheDir":      defaultCacheDir(),
			"trashDays":     strconv.Itoa(web.DefaultTrashDays),
			"layout":        web.DefaultLayout,
			"failsPerIP":    strconv.Itoa(web.DefaultRateLimits.FailsPerIP),
			"failsTotal":    strconv.Itoa(web.DefaultRateLimits.FailsTotal),
			"requestsPerIP": strconv.Itoa(web.DefaultRateLimits.RequestsPerIP),
			"requestsTotal": strconv.Itoa(web.DefaultRateLimits.RequestsTotal),
			"uploadsPerIP":  strconv.Itoa(web.DefaultRateLimits.UploadsPerIP),
			"uploadsTotal":  strconv.Itoa(web.DefaultRateLimits.UploadsTotal),
		})
}

// warnUnknownEnv says which SERMON_STUDIO_* variables are not settings of
// this version, with the nearest one that is. A warning, not an error: a
// setting added to a ConfigMap before the version that knows it is rolled out
// must not stop the running one. The login cannot be lost this way: without
// one of --oidc-issuer and --no-login the studio does not start.
func warnUnknownEnv(parser *kong.Kong, environ []string, w io.Writer) {
	known := map[string]bool{}
	var walk func(n *kong.Node)
	walk = func(n *kong.Node) {
		for _, f := range n.Flags {
			for _, e := range f.Envs {
				known[e] = true
			}
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(parser.Model.Node)
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		if !strings.HasPrefix(name, envPrefix+"_") || known[name] || serviceLink(name) {
			continue
		}
		msg := fmt.Sprintf("warning: %s is not a setting of this version; ignored", name)
		if near := nearest(name, known); near != "" {
			msg = fmt.Sprintf("warning: %s is not a setting of this version (did you mean %s?); ignored", name, near)
		}
		fmt.Fprintln(w, msg)
	}
}

// serviceLink reports the variables Kubernetes sets for a Service named
// sermon-studio (SERMON_STUDIO_SERVICE_HOST, SERMON_STUDIO_PORT_8080_TCP...):
// not settings, and no reason to warn.
func serviceLink(name string) bool {
	rest := strings.TrimPrefix(name, envPrefix+"_")
	return strings.HasPrefix(rest, "SERVICE_") || rest == "PORT" || strings.HasPrefix(rest, "PORT_")
}

// nearest is the known name closest to name, if it is only a few edits away.
func nearest(name string, known map[string]bool) string {
	best, bestD := "", 4
	for k := range known {
		if d := editDistance(name, k); d < bestD || (d == bestD && k < best) {
			best, bestD = k, d
		}
	}
	return best
}

func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

// Run starts the studio and serves until it is stopped.
func (s *serveCmd) Run() error {
	bin, err := exec.LookPath(s.FFmpeg)
	if err != nil {
		return fmt.Errorf("cannot find %q in PATH (Linux: apt install ffmpeg)", s.FFmpeg)
	}
	logger := log.New(os.Stderr, "", log.LstdFlags)
	web.CleanCache(s.CacheDir, time.Duration(s.CacheDays)*24*time.Hour)
	var oidc *web.OIDCOptions
	if !s.NoLogin {
		oidc = &web.OIDCOptions{Issuer: s.OIDCIssuer, ClientID: s.OIDCClientID, PublicURL: s.PublicURL, CookieDomain: s.CookieDomain}
		if s.OIDCClientSecretFile != "" {
			b, err := os.ReadFile(s.OIDCClientSecretFile)
			if err != nil {
				return fmt.Errorf("--oidc-client-secret-file: %w", err)
			}
			oidc.ClientSecret = strings.TrimSpace(string(b))
		}
	}
	var share *web.ShareOptions
	if s.ShareListen != "" {
		limits := web.RateLimits{FailsPerIP: s.ShareFailsPerIP, FailsTotal: s.ShareFailsTotal, RequestsPerIP: s.ShareRequestsPerIP,
			RequestsTotal: s.ShareRequestsTotal, UploadsPerIP: s.ShareUploadsPerIP, UploadsTotal: s.ShareUploadsTotal}
		share = &web.ShareOptions{URL: s.ShareURL, IPHeader: s.ShareIPHeader, Limits: limits}
	}
	srv, err := web.NewApp(web.AppConfig{
		Base: web.Config{Root: s.Root, Inbox: s.Inbox, CacheDir: s.CacheDir,
			Layout: s.Layout, TrashDays: s.TrashDays, FFmpeg: normalizer.FFmpeg{Bin: bin}, Logger: logger},
		DataDir: s.DataDir, OIDC: oidc,
		Share: share, ParallelRuns: s.ParallelRuns,
	})
	if err != nil {
		return err
	}
	// No read or write timeouts: uploads of whole services and event
	// streams of long runs must not be cut off.
	hs := &http.Server{Addr: s.Listen, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errc := make(chan error, 3)
	// The admin socket (sermon-studio admin), in the data dir: also how a second
	// studio on the same data dir is refused.
	var as *http.Server
	if s.DataDir != "" {
		l, err := listenAdmin(web.AdminSocket(s.DataDir))
		if err != nil {
			srv.Close()
			return err
		}
		as = &http.Server{Handler: srv.AdminHandler(), ReadHeaderTimeout: 10 * time.Second}
		go func() { errc <- as.Serve(l) }()
		defer os.Remove(web.AdminSocket(s.DataDir))
	}
	go func() { errc <- hs.ListenAndServe() }()
	logger.Printf("sermon-studio %s listening on %s (data dir %q, root %q)", version, s.Listen, s.DataDir, s.Root)
	// The public share page: upload chunks are small, so it can have timeouts (a download sets its own).
	var ss *http.Server
	if share != nil {
		ss = &http.Server{Addr: s.ShareListen, Handler: srv.ShareHandler(), ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout: 5 * time.Minute, WriteTimeout: 6 * time.Minute, IdleTimeout: 2 * time.Minute, MaxHeaderBytes: 16 << 10}
		go func() { errc <- ss.ListenAndServe() }()
		logger.Printf("share page listening on %s", s.ShareListen)
	}

	select {
	case err := <-errc:
		srv.Close()
		return err
	case <-ctx.Done():
	}
	logger.Print("shutting down: cancelling jobs and waiting for them to clean up")
	if ss != nil { // first, so no upload finishes into a closed app
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		ss.Shutdown(sctx)
		cancel()
	}
	if as != nil {
		as.Close()
	}
	srv.Close() // ends event streams too, so Shutdown does not wait on them
	sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return hs.Shutdown(sctx)
}

// listenAdmin listens on the admin socket, for this user only. A socket left
// by a studio that is gone is replaced; one that answers is another studio
// on the same data dir, which is refused.
func listenAdmin(path string) (net.Listener, error) {
	if c, err := net.DialTimeout("unix", path, time.Second); err == nil {
		c.Close()
		return nil, fmt.Errorf("another studio is running on this data dir (%s answers)", path)
	}
	os.Remove(path)
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("admin socket: %w", err)
	}
	os.Chmod(path, 0o600) // the data dir is the studio's own anyway
	return l, nil
}

func defaultCacheDir() string {
	if dir, err := os.UserCacheDir(); err == nil {
		return filepath.Join(dir, "sermon-studio")
	}
	return filepath.Join(os.TempDir(), "sermon-studio-cache")
}

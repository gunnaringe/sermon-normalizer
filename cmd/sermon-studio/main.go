// Command sermon-studio serves the web app: browse date folders, cut
// the sermon on a waveform, run and follow jobs, and import new recordings.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/gunnaringe/sermon-studio/normalizer"
	"github.com/gunnaringe/sermon-studio/web"
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	listen := flag.String("listen", ":8080", "address to listen on")
	dataDir := flag.String("data-dir", "", "folder with a folder per church: <id>/Taler (sermons), <id>/Innboks (inbox), <id>/church.json (name and settings); churches can be added in the app")
	root := flag.String("root", "", "a single church without a church folder: the folder with its date folders (instead of --data-dir; settings are not saved)")
	inbox := flag.String("inbox", "", "with --root: the folder new recordings arrive in (optional)")
	groupsHeader := flag.String("groups-header", "", "request header in which the login proxy sends the user's groups (space-separated); turns on access control: a church is for the group sermon_studio_<church id, - as _>, sermon_studio_admin sees all and can add churches")
	logoutURL := flag.String("logout-url", "", "the login proxy's logout address (a POST); shows a Log out button in the header")
	cacheDir := flag.String("cache-dir", defaultCacheDir(), "where waveform data and previews are kept")
	cacheDays := flag.Int("cache-days", 30, "remove cached analyses not used for this many days")
	layout := flag.String("layout", web.DefaultLayout, "where imported recordings go under --root, as a Go time layout")
	ffBin := flag.String("ffmpeg", "ffmpeg", "path to ffmpeg")
	shareListen := flag.String("share-listen", "", "address for the public upload page, where recordings are sent with a code made in the studio (Settings → Shares); \"\" = off")
	shareURL := flag.String("share-url", "", "the upload page's public address (e.g. https://upload-sermons.example.org/), for share links")
	shareIPHeader := flag.String("share-ip-header", "", "header the proxy puts the client's address in (Cloudflare: CF-Connecting-IP); used for rate limits")
	var limits web.RateLimits
	for _, l := range []struct {
		v    *int
		name string
		d    int
		help string
	}{
		{&limits.FailsPerIP, "share-fails-per-ip", web.DefaultRateLimits.FailsPerIP, "wrong codes from one address in 10 minutes before it is blocked for an hour"},
		{&limits.FailsTotal, "share-fails-total", web.DefaultRateLimits.FailsTotal, "wrong codes from everyone in 10 minutes before codes are refused for 10 minutes"},
		{&limits.RequestsPerIP, "share-requests-per-ip", web.DefaultRateLimits.RequestsPerIP, "requests per minute to the upload page from one address"},
		{&limits.RequestsTotal, "share-requests-total", web.DefaultRateLimits.RequestsTotal, "requests per minute to the upload page from everyone"},
		{&limits.UploadsPerIP, "share-uploads-per-ip", web.DefaultRateLimits.UploadsPerIP, "chunks received at once from one address"},
		{&limits.UploadsTotal, "share-uploads-total", web.DefaultRateLimits.UploadsTotal, "chunks received at once from everyone"},
	} {
		flag.IntVar(l.v, l.name, l.d, l.help)
	}
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println("sermon-studio", version)
		return nil
	}
	if *root == "" && *dataDir == "" {
		return errors.New("--root or --data-dir is required")
	}
	bin, err := exec.LookPath(*ffBin)
	if err != nil {
		return fmt.Errorf("cannot find %q in PATH (Linux: apt install ffmpeg)", *ffBin)
	}
	logger := log.New(os.Stderr, "", log.LstdFlags)
	web.CleanCache(*cacheDir, time.Duration(*cacheDays)*24*time.Hour)
	var share *web.ShareOptions
	if *shareListen != "" {
		share = &web.ShareOptions{URL: *shareURL, IPHeader: *shareIPHeader, Limits: limits}
	}
	srv, err := web.NewApp(web.AppConfig{
		Base: web.Config{Root: *root, Inbox: *inbox, CacheDir: *cacheDir,
			Layout: *layout, FFmpeg: normalizer.FFmpeg{Bin: bin}, Logger: logger},
		DataDir: *dataDir, GroupsHeader: *groupsHeader, LogoutURL: *logoutURL,
		Share: share,
	})
	if err != nil {
		return err
	}
	// No read or write timeouts: uploads of whole services and event
	// streams of long runs must not be cut off.
	hs := &http.Server{Addr: *listen, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errc := make(chan error, 2)
	go func() { errc <- hs.ListenAndServe() }()
	logger.Printf("sermon-studio %s listening on %s (data dir %q, root %q)", version, *listen, *dataDir, *root)
	// The public upload page: chunks are small, so it can have timeouts.
	var ss *http.Server
	if share != nil {
		ss = &http.Server{Addr: *shareListen, Handler: srv.ShareHandler(), ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout: 5 * time.Minute, WriteTimeout: 6 * time.Minute, IdleTimeout: 2 * time.Minute, MaxHeaderBytes: 16 << 10}
		go func() { errc <- ss.ListenAndServe() }()
		logger.Printf("upload page listening on %s", *shareListen)
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
	srv.Close() // ends event streams too, so Shutdown does not wait on them
	sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return hs.Shutdown(sctx)
}

func defaultCacheDir() string {
	if dir, err := os.UserCacheDir(); err == nil {
		return filepath.Join(dir, "sermon-studio")
	}
	return filepath.Join(os.TempDir(), "sermon-studio-cache")
}

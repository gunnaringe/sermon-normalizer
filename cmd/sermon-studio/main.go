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
	name := flag.String("name", "", "ignored: a church's name is in its church.json (kept so older deployments still start)")
	inbox := flag.String("inbox", "", "with --root: the folder new recordings arrive in (optional)")
	cacheDir := flag.String("cache-dir", defaultCacheDir(), "where waveform data and previews are kept")
	cacheDays := flag.Int("cache-days", 30, "remove cached analyses not used for this many days")
	archive := flag.String("archive", "", "the folder name older versions kept the whole recording in (e.g. arkiv); it is renamed to original/ in each date folder at start")
	layout := flag.String("layout", web.DefaultLayout, "where imported recordings go under --root, as a Go time layout")
	ffBin := flag.String("ffmpeg", "ffmpeg", "path to ffmpeg")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println("sermon-studio", version)
		return nil
	}
	if *name != "" {
		fmt.Fprintln(os.Stderr, "note: --name is ignored; the name is set on the Settings page")
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
	srv, err := web.NewApp(web.AppConfig{
		Base: web.Config{Root: *root, Inbox: *inbox, CacheDir: *cacheDir, LegacyOriginals: *archive,
			Layout: *layout, FFmpeg: normalizer.FFmpeg{Bin: bin}, Logger: logger},
		DataDir: *dataDir,
	})
	if err != nil {
		return err
	}
	// No read or write timeouts: uploads of whole services and event
	// streams of long runs must not be cut off.
	hs := &http.Server{Addr: *listen, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errc := make(chan error, 1)
	go func() { errc <- hs.ListenAndServe() }()
	logger.Printf("sermon-studio %s listening on %s (data dir %q, root %q)", version, *listen, *dataDir, *root)

	select {
	case err := <-errc:
		srv.Close()
		return err
	case <-ctx.Done():
	}
	logger.Print("shutting down: cancelling jobs and waiting for them to clean up")
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

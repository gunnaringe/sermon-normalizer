// Command sermon-normalizer serves the web app: browse date folders, cut
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

	"github.com/gunnaringe/sermon-normalizer/normalizer"
	"github.com/gunnaringe/sermon-normalizer/web"
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
	root := flag.String("root", "", "folder with the date folders (required)")
	inbox := flag.String("inbox", "", "folder new recordings arrive in (optional)")
	cacheDir := flag.String("cache-dir", defaultCacheDir(), "where waveform data and previews are kept")
	cacheDays := flag.Int("cache-days", 30, "remove cached analyses not used for this many days")
	archive := flag.String("archive", "", "default archive folder for runs (relative to the date folder)")
	layout := flag.String("layout", web.DefaultLayout, "where imported recordings go under --root, as a Go time layout")
	ffBin := flag.String("ffmpeg", "ffmpeg", "path to ffmpeg")
	publish := flag.String("publish", "", "copy finished files here, in the same folders (e.g. a folder Resilio Sync shares)")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println("sermon-normalizer", version)
		return nil
	}
	if *root == "" {
		return errors.New("--root is required")
	}
	bin, err := exec.LookPath(*ffBin)
	if err != nil {
		return fmt.Errorf("cannot find %q in PATH (Linux: apt install ffmpeg)", *ffBin)
	}
	logger := log.New(os.Stderr, "", log.LstdFlags)
	web.CleanCache(*cacheDir, time.Duration(*cacheDays)*24*time.Hour)
	srv, err := web.New(web.Config{Root: *root, Inbox: *inbox, CacheDir: *cacheDir, Archive: *archive,
		Layout: *layout, FFmpeg: normalizer.FFmpeg{Bin: bin}, Publish: *publish, Logger: logger})
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
	logger.Printf("sermon-normalizer %s listening on %s (root %s)", version, *listen, *root)

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
		return filepath.Join(dir, "sermon-normalizer")
	}
	return filepath.Join(os.TempDir(), "sermon-normalizer-cache")
}

// Command sermon-studio-desktop is Sermon Studio on one computer: it runs the studio on 127.0.0.1 and opens it
// in a window of its own. Starting it again opens another window to the one running; it stops by itself a
// while after the last window has closed, when nothing is running.
//
// Everything desktop is here and in internal/desktop; the studio (web) is used as it is.
package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/gunnaringe/sermon-studio/cmd/sermon-studio-desktop/internal/desktop"
	"github.com/gunnaringe/sermon-studio/normalizer"
	"github.com/gunnaringe/sermon-studio/web"
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		log.Print("error: ", err)
		if f := flag.Lookup("no-window"); f == nil || f.Value.String() != "true" { // unattended: no dialog to wait on
			desktop.ShowError(err.Error())
		}
		os.Exit(1)
	}
}

func run() error {
	dataDir := flag.String("data-dir", "", "the data folder, instead of the one in the settings (not saved)")
	noWindow := flag.Bool("no-window", false, "do not open a window")
	idleAfter := flag.Duration("idle-after", 5*time.Minute, "stop when no window has been open and nothing has run for this long")
	showVersion := flag.Bool("version", false, "print the version and exit")
	licenses := flag.Bool("licenses", false, "print the licences of the ffmpeg built in and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println("sermon-studio-desktop", version)
		return nil
	}
	if *licenses {
		fmt.Print(cmp.Or(desktop.Licenses(), "No ffmpeg is built in.\n"))
		return nil
	}

	configDir, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return err
	}
	configDir, cacheDir = filepath.Join(configDir, desktop.Name), filepath.Join(cacheDir, desktop.Name)
	out, closeLog, err := desktop.OpenLog(cacheDir)
	if err != nil {
		return err
	}
	defer closeLog()
	log.SetOutput(out)
	logger := log.New(out, "", log.LstdFlags)

	settings, err := desktop.LoadSettings(configDir)
	if err != nil {
		return err
	}
	ln, running, err := listen(settings)
	if err != nil {
		return err
	}
	if ln != nil {
		defer ln.Close() // also when the start fails: a second start must not wait on a port nobody answers
	}
	if running {
		logger.Printf("already running on port %d: opening a window", settings.Port)
		if *noWindow {
			return nil
		}
		return desktop.OpenWindow(desktop.URL(settings.Port))
	}
	if *dataDir != "" { // after listen, which may save the settings
		settings.DataDir = *dataDir
	}

	if err := os.MkdirAll(settings.DataDir, 0o755); err != nil {
		return err
	}
	first, err := desktop.FirstChurch(settings.DataDir)
	if err != nil {
		return fmt.Errorf("making the first church in %s: %w", settings.DataDir, err)
	}
	bin, err := desktop.FFmpeg(cacheDir)
	if err != nil {
		return err
	}
	// The studio's own cache (waveforms, previews) in a folder of its own: CleanCache removes what is old there.
	studioCache := filepath.Join(cacheDir, "studio")
	web.CleanCache(studioCache, 30*24*time.Hour)
	app, err := web.NewApp(web.AppConfig{
		Base: web.Config{CacheDir: studioCache, Layout: web.DefaultLayout, TrashDays: web.DefaultTrashDays,
			FFmpeg: normalizer.FFmpeg{Bin: bin}, Logger: logger},
		DataDir: settings.DataDir,
	})
	if err != nil {
		return err
	}

	presence := desktop.NewPresence()
	// Requests end when the program stops: an open window's event stream would keep Shutdown waiting.
	base, cancelRequests := context.WithCancel(context.Background())
	defer cancelRequests()
	hs := &http.Server{Handler: desktop.Guard(settings.Port, presence.Handler(settings.Token, app.Handler())),
		ReadHeaderTimeout: 10 * time.Second, BaseContext: func(net.Listener) context.Context { return base }}
	errc := make(chan error, 1)
	go func() { errc <- hs.Serve(ln) }()
	url := desktop.URL(settings.Port)
	logger.Printf("sermon-studio-desktop %s at %s (data folder %q, ffmpeg %s)", version, url, settings.DataDir, bin)
	if first {
		url += "#/settings" // to give the church its name
	}
	if !*noWindow {
		if err := desktop.OpenWindow(url); err != nil {
			logger.Printf("opening a window: %v (open %s in a browser)", err, url)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	tick := time.NewTicker(min(10*time.Second, max(*idleAfter/4, 100*time.Millisecond)))
	defer tick.Stop()
wait:
	for {
		select {
		case err := <-errc:
			app.Close()
			return err
		case <-ctx.Done():
			break wait
		case <-tick.C:
			if presence.IdleFor() >= *idleAfter && !app.Busy() {
				logger.Printf("no window open for %s: stopping", *idleAfter)
				break wait
			}
		}
	}
	cancelRequests()
	app.Close()
	sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return hs.Shutdown(sctx)
}

// listen listens on the port in the settings. When the port is taken by this program already (a second start,
// perhaps at the same moment as the first), running is true. When it is taken by something else, another
// port is picked and kept.
func listen(s *desktop.Settings) (ln net.Listener, running bool, err error) {
	addr := "127.0.0.1:" + strconv.Itoa(s.Port)
	if ln, err = net.Listen("tcp", addr); err == nil {
		return ln, false, nil
	}
	for range 10 {
		if desktop.Running(s.Port, s.Token) {
			return nil, true, nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	if ln, err = net.Listen("tcp", "127.0.0.1:0"); err != nil {
		return nil, false, err
	}
	old := s.Port
	s.Port = ln.Addr().(*net.TCPAddr).Port
	if err := s.Save(); err != nil {
		ln.Close()
		return nil, false, errors.Join(errors.New("saving the new port"), err)
	}
	log.Printf("port %d is taken by another program: using %d from now on", old, s.Port)
	return ln, false, nil
}

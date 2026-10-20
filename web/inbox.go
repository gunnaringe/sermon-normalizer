package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/gunnaringe/sermon-studio/normalizer"
)

var errNoInbox = errors.New("no inbox folder configured")

// inboxName checks an inbox file name: a plain file of one of the
// normalizer.InputTypes, not hidden and not one being written.
func inboxName(name string) (string, error) {
	base := filepath.Base(name)
	if base != name || base == "" || strings.HasPrefix(base, ".") || strings.ContainsAny(base, `/\`) || normalizer.IsPartial(base) {
		return "", fmt.Errorf("invalid file name %q", name)
	}
	if _, ok := normalizer.KindOf(base); ok {
		return base, nil
	}
	return "", fmt.Errorf("%s: not a type of file that can be used (%s)", base, strings.Join(normalizer.InputTypes(), " "))
}

var (
	reNameDate = regexp.MustCompile(`(20\d\d)[-_.]?(\d\d)[-_.]?(\d\d)`)
	reMetaDate = regexp.MustCompile(`(?mi)^\s+(?:date|creation_time|origination_date)\s*:\s*(\d{4}-\d{2}-\d{2})`)
)

func validDate(s string) bool {
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

// dateFromName finds a date like 2026-10-04 or 20261004 in a file name.
func dateFromName(name string) string {
	for _, m := range reNameDate.FindAllStringSubmatch(name, -1) {
		if d := m[1] + "-" + m[2] + "-" + m[3]; validDate(d) {
			return d
		}
	}
	return ""
}

// dateFromMetadata reads a recording date from the file's tags (BWF
// origination date in WAV, DATE or creation_time in FLAC).
func dateFromMetadata(stderr string) string {
	for _, m := range reMetaDate.FindAllStringSubmatch(stderr, -1) {
		if validDate(m[1]) {
			return m[1]
		}
	}
	return ""
}

// suggestDate guesses when a recording was made: from its name, its tags,
// or its modification time (the client's, for uploads).
// Tag reads are remembered by cacheKey, as the inbox page asks every few seconds.
func (s *Server) suggestDate(ctx context.Context, path string, mod time.Time) (string, string) {
	if d := dateFromName(filepath.Base(path)); d != "" {
		return d, "name"
	}
	key, _ := cacheKey(path)
	s.tagsMu.Lock()
	d, ok := s.tagDates[key]
	s.tagsMu.Unlock()
	if !ok {
		if res, err := s.cfg.FFmpeg.Run(ctx, "-i", path, "-t", "0", "-f", "null", "-"); err == nil {
			d = dateFromMetadata(res.Stderr)
		}
		if key != "" && ctx.Err() == nil {
			s.tagsMu.Lock()
			s.tagDates[key] = d
			s.tagsMu.Unlock()
		}
	}
	if d != "" {
		return d, "metadata"
	}
	return mod.Format("2006-01-02"), "modified"
}

// newRawFolder is the folder for a new raw recording of date (made after the
// layout, not created here): <date>, or <date> <name> when there is a name.
// It fails if the folder already has a raw recording; the status is the HTTP
// status for the error.
func (s *Server) newRawFolder(date, name string) (string, int, error) {
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		return "", http.StatusBadRequest, fmt.Errorf("invalid date %q (use YYYY-MM-DD)", date)
	}
	folder, err := s.resolve(d.Format(s.cfg.Layout))
	if err != nil {
		return "", http.StatusBadRequest, err
	}
	if filepath.Base(folder) != date {
		return "", http.StatusInternalServerError, fmt.Errorf("the layout %q must end in the date (2006-01-02)", s.cfg.Layout)
	}
	if name != "" {
		if folder, err = s.resolve(s.relPath(filepath.Dir(folder)) + "/" + date + " " + name); err != nil {
			return "", http.StatusBadRequest, err
		}
	}
	if !fileExists(folder) {
		return folder, 0, nil
	}
	paths, err := normalizer.NewPaths(folder)
	if err != nil {
		return "", http.StatusInternalServerError, err
	}
	if raw := paths.Source(); raw != "" {
		return "", http.StatusConflict, fmt.Errorf("%s already has a raw recording (%s); give this one a name", filepath.Base(folder), filepath.Base(raw))
	}
	return folder, 0, nil
}

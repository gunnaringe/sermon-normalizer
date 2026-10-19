package web

import (
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gunnaringe/sermon-normalizer/normalizer"
)

// Meta are a church's default tags for new sermons, as templates with
// placeholders (see expandTemplate).
type Meta struct {
	Title  string `json:"title"`
	Artist string `json:"artist"`
	Album  string `json:"album"`
}

// DefaultMeta is what a church starts with: the title is the date.
var DefaultMeta = Meta{Title: "{yyyy}-{mm}-{dd}"}

// placeholders are the names usable in a template, with what each gives.
var placeholders = []struct {
	Name string `json:"name"`
	Help string `json:"help"`
}{
	{"yyyy", "year, 2026"},
	{"yy", "year, 26"},
	{"mm", "month, 10"},
	{"dd", "day, 04"},
	{"date", "2026-10-04"},
	{"weekday", "Sunday"},
	{"name", "the sermon's name (empty if it has none)"},
}

const maxTemplate = 200

var rePlaceholder = regexp.MustCompile(`\{([^{}]*)\}`)

// expandTemplate fills in the placeholders of a template for a sermon on
// date, with the optional name. An unknown placeholder is an error, so a typo
// does not end up in the tags. Spaces left around an empty {name} are tidied.
func expandTemplate(tpl string, date time.Time, name string) (string, error) {
	var bad []string
	out := rePlaceholder.ReplaceAllStringFunc(tpl, func(m string) string {
		switch key := m[1 : len(m)-1]; key {
		case "yyyy":
			return date.Format("2006")
		case "yy":
			return date.Format("06")
		case "mm":
			return date.Format("01")
		case "dd":
			return date.Format("02")
		case "date":
			return date.Format("2006-01-02")
		case "weekday":
			return date.Weekday().String()
		case "name":
			return name
		default:
			bad = append(bad, m)
			return m
		}
	})
	if len(bad) > 0 {
		return "", fmt.Errorf("unknown placeholder %s (use %s)", strings.Join(bad, ", "), placeholderNames())
	}
	if strings.ContainsAny(out, "{}") {
		return "", errors.New("unmatched { or }")
	}
	out = strings.Map(func(r rune) rune {
		if r < ' ' {
			return ' '
		}
		return r
	}, out)
	return strings.Join(strings.Fields(out), " "), nil
}

func placeholderNames() string {
	var n []string
	for _, p := range placeholders {
		n = append(n, "{"+p.Name+"}")
	}
	return strings.Join(n, " ")
}

// expand fills in all three templates.
func (m Meta) expand(date time.Time, name string) (Meta, error) {
	var out Meta
	for _, f := range []struct {
		label string
		in    string
		out   *string
	}{{"Title", m.Title, &out.Title}, {"Artist", m.Artist, &out.Artist}, {"Album", m.Album, &out.Album}} {
		if len(f.in) > maxTemplate {
			return out, fmt.Errorf("%s is too long (at most %d characters)", f.label, maxTemplate)
		}
		v, err := expandTemplate(f.in, date, name)
		if err != nil {
			return out, fmt.Errorf("%s: %w", f.label, err)
		}
		*f.out = v
	}
	return out, nil
}

var reFolderDate = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})(?: (.+))?$`)

// parseFolder splits a date folder name, "2026-10-04" or "2026-10-04 Evening",
// into its date and sermon name.
func parseFolder(base string) (time.Time, string, bool) {
	m := reFolderDate.FindStringSubmatch(base)
	if m == nil {
		return time.Time{}, "", false
	}
	d, err := time.Parse("2006-01-02", m[1])
	return d, strings.TrimSpace(m[2]), err == nil
}

// metaStore holds a church's settings; save persists them (nil = memory only).
type metaStore struct {
	mu   sync.Mutex
	meta Meta
	save func(Meta) error
}

func (m *metaStore) get() Meta {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.meta
}

// defaultTags are the church's templates filled in for a date folder, and
// the folder's date.
func (s *Server) defaultTags(folder string) (Meta, time.Time, error) {
	date, name, ok := parseFolder(filepath.Base(folder))
	if !ok {
		return Meta{}, time.Time{}, fmt.Errorf("%q is not a date folder (2026-10-04 or 2026-10-04 Name), so the tags cannot be filled in", filepath.Base(folder))
	}
	m, err := s.meta.get().expand(date, name)
	return m, date, err
}

// runTags is the tags for a run: what the form sent, else the defaults. A
// field sent empty means "no tag".
func (s *Server) runTags(folder string, q runRequest) (normalizer.Tags, string) {
	def, date, err := s.defaultTags(folder)
	msg := ""
	if err != nil {
		msg = "Default tags not applied: " + err.Error()
	}
	pick := func(sent *string, d string) string {
		if sent != nil {
			return strings.Join(strings.Fields(*sent), " ")
		}
		return d
	}
	t := normalizer.Tags{Title: pick(q.Title, def.Title), Artist: pick(q.Artist, def.Artist), Album: pick(q.Album, def.Album)}
	if !date.IsZero() {
		t.Date = date.Format("2006-01-02")
	}
	return t, msg
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"meta": s.meta.get(), "placeholders": placeholders})
}

func (s *Server) handleSettingsPut(w http.ResponseWriter, r *http.Request) {
	var q struct {
		Meta Meta `json:"meta"`
	}
	if err := decodeRequest(r, &q); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if _, err := q.Meta.expand(time.Now(), "Sample"); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.meta.mu.Lock()
	defer s.meta.mu.Unlock()
	if s.meta.save != nil {
		if err := s.meta.save(q.Meta); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
	}
	s.meta.meta = q.Meta
	writeJSON(w, http.StatusOK, map[string]any{"meta": q.Meta})
}

// handleMetaPreview expands templates for the settings form, as they would
// come out for a date (default today) and name.
func (s *Server) handleMetaPreview(w http.ResponseWriter, r *http.Request) {
	var q struct {
		Meta
		Date string `json:"date"`
		Name string `json:"name"`
	}
	if err := decodeRequest(r, &q); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	date := time.Now()
	if q.Date != "" {
		var err error
		if date, err = time.Parse("2006-01-02", q.Date); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid date %q", q.Date))
			return
		}
	}
	m, err := q.Meta.expand(date, strings.TrimSpace(q.Name))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

package web

import (
	"path/filepath"
	"testing"

	"github.com/gunnaringe/sermon-studio/normalizer"
)

// Date folders from older versions keep the whole recording in the --archive
// folder; at start it is renamed to original/, and only where original/ is free.
func TestMigrateOriginals(t *testing.T) {
	ts := newTestServer(t, normalizer.FFmpeg{})
	ts.file(t, "2026/2026-10-04/arkiv/2026-10-04_raw.flac", "x")
	ts.file(t, "2026/2026-10-04/arkiv/2026-10-04_edit.flac", "old cut")
	ts.file(t, "2026/2026-10-11 Kveld/arkiv/2026-10-11 Kveld_raw.flac", "x")
	ts.file(t, "2026/2026-10-18/arkiv/a", "x")
	ts.file(t, "2026/2026-10-18/original/b", "x")
	ts.file(t, "2026/2026-10-25/original/2026-10-25_raw.flac", "x")

	ts.cfg.LegacyOriginals = "arkiv"
	ts.migrateOriginals()
	for _, f := range []string{
		"2026/2026-10-04/original/2026-10-04_raw.flac",
		"2026/2026-10-04/original/2026-10-04_edit.flac", // old cuts are kept, unused
		"2026/2026-10-11 Kveld/original/2026-10-11 Kveld_raw.flac",
		"2026/2026-10-18/arkiv/a", // original/ was taken: left alone
		"2026/2026-10-18/original/b",
		"2026/2026-10-25/original/2026-10-25_raw.flac",
	} {
		if !fileExists(filepath.Join(ts.root, f)) {
			t.Errorf("missing %s", f)
		}
	}
	if fileExists(filepath.Join(ts.root, "2026/2026-10-04/arkiv")) {
		t.Error("arkiv is still there")
	}
	// The recording is found where it was moved to.
	if e := ts.entry(filepath.Join(ts.root, "2026/2026-10-04")); !e.Raw {
		t.Errorf("entry %+v", e)
	}
	// Nothing happens without a legacy name, or with a path-like one.
	ts.file(t, "2026/2026-11-01/arkiv/x", "x")
	for _, name := range []string{"", "../2026", "a/b"} {
		ts.cfg.LegacyOriginals = name
		ts.migrateOriginals()
	}
	if !fileExists(filepath.Join(ts.root, "2026/2026-11-01/arkiv/x")) {
		t.Error("migrated without a legacy name")
	}
}

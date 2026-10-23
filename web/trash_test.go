package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gunnaringe/sermon-studio/normalizer"
)

// Deleting moves a recording to the trash, hidden from everything; it can be restored, removed for
// good, and goes by itself when it has been there long enough.
func TestTrash(t *testing.T) {
	ts := newTestServer(t, normalizer.FFmpeg{})
	ts.file(t, "2026/2026-10-04/2026-10-04_raw.flac", "audio")
	ts.file(t, "2026/2026-10-04/2026-10-04.mp3", "mp3")
	ts.file(t, "2026/2026-10-11/2026-10-11_raw.flac", "other")
	list := func() []folderEntry { return ts.allRecordings() }
	if len(list()) != 2 {
		t.Fatalf("recordings: %+v", list())
	}

	// Not a recording, not a path in the trash.
	for _, q := range []string{"2026", "nothing", ".trash", "../x"} {
		if w := ts.do(t, "POST", "/api/recordings/delete?path="+q, nil); w.Code < 400 {
			t.Errorf("delete %q: %d", q, w.Code)
		}
	}

	w := ts.do(t, "POST", "/api/recordings/delete?path=2026/2026-10-04", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	if fileExists(filepath.Join(ts.root, "2026", "2026-10-04")) {
		t.Error("the folder is still there")
	}
	if got := list(); len(got) != 1 || got[0].Name != "2026-10-11" {
		t.Errorf("recordings after: %+v", got)
	}
	// The trash is out of reach of the paths of the API.
	if w := ts.do(t, "GET", "/api/folders?path=.trash", nil); w.Code != http.StatusBadRequest {
		t.Errorf("the trash as a path: %d", w.Code)
	}

	items := decode[struct {
		Items []trashItem
		Days  int
	}](t, ts.do(t, "GET", "/api/trash", nil))
	if len(items.Items) != 1 || items.Days != DefaultTrashDays {
		t.Fatalf("trash: %+v", items)
	}
	it := items.Items[0]
	if it.Name != "2026-10-04" || it.From != "2026/2026-10-04" || it.Size != int64(len("audio")+len("mp3")) || it.DaysLeft != DefaultTrashDays {
		t.Errorf("item: %+v", it)
	}

	// Restore: back where it was, with what was in it. Not over one that is there.
	ts.file(t, "2026/2026-10-04/2026-10-04_raw.flac", "new")
	if w := ts.do(t, "POST", "/api/trash/restore?id="+it.ID, nil); w.Code != http.StatusConflict {
		t.Errorf("restore over an existing one: %d", w.Code)
	}
	os.RemoveAll(filepath.Join(ts.root, "2026", "2026-10-04"))
	if w := ts.do(t, "POST", "/api/trash/restore?id="+it.ID, nil); w.Code != http.StatusOK {
		t.Fatalf("restore: %d %s", w.Code, w.Body)
	}
	b, _ := os.ReadFile(filepath.Join(ts.root, "2026", "2026-10-04", "2026-10-04.mp3"))
	if string(b) != "mp3" || fileExists(filepath.Join(ts.root, "2026", "2026-10-04", trashNote)) || len(list()) != 2 {
		t.Errorf("restored: %q %+v", b, list())
	}

	// Remove for good; ids that are not in the trash are refused.
	ts.do(t, "POST", "/api/recordings/delete?path=2026/2026-10-04", nil)
	id := ts.do(t, "GET", "/api/trash", nil)
	it = decode[struct{ Items []trashItem }](t, id).Items[0]
	for _, bad := range []string{"", "..", "../2026", ".trash", it.ID + "/..", "20260101-000000_x"} {
		if w := ts.do(t, "POST", "/api/trash/remove?id="+bad, nil); w.Code != http.StatusNotFound {
			t.Errorf("remove %q: %d", bad, w.Code)
		}
	}
	if w := ts.do(t, "POST", "/api/trash/remove?id="+it.ID, nil); w.Code != http.StatusOK || fileExists(filepath.Join(ts.root, trashName, it.ID)) {
		t.Errorf("remove: %d", w.Code)
	}
	if len(list()) != 1 {
		t.Errorf("recordings: %+v", list())
	}
}

func TestTrashPurge(t *testing.T) {
	ts := newTestServerWith(t, normalizer.FFmpeg{}, func(c *Config) { c.TrashDays = 7 })
	ts.file(t, "2026/2026-10-04/2026-10-04_raw.flac", "a")
	ts.file(t, "2026/2026-10-11/2026-10-11_raw.flac", "b")
	for _, d := range []string{"2026-10-04", "2026-10-11"} {
		if w := ts.do(t, "POST", "/api/recordings/delete?path=2026/"+d, nil); w.Code != http.StatusOK {
			t.Fatal(w.Code, w.Body)
		}
	}
	ts.purgeTrash(time.Now().Add(6 * 24 * time.Hour))
	if n := len(ts.trashItems(time.Now())); n != 2 {
		t.Errorf("after 6 days: %d in the trash", n)
	}
	ts.purgeTrash(time.Now().Add(8 * 24 * time.Hour))
	if n := len(ts.trashItems(time.Now())); n != 0 {
		t.Errorf("after 8 days: %d in the trash", n)
	}
	if des, _ := os.ReadDir(ts.trashRoot()); len(des) != 0 {
		t.Errorf("left: %v", des)
	}
}

// Deleting the original keeps the files made from it, and says so; without files it is refused.
func TestDeleteOriginal(t *testing.T) {
	ts := newTestServer(t, normalizer.FFmpeg{})
	ts.file(t, "2026/2026-10-04/original/2026-10-04_raw.flac", "audio")
	ts.file(t, "2026/2026-10-04/2026-10-04.mp3", "mp3")
	ts.file(t, "2026/2026-10-11/2026-10-11_raw.flac", "other")
	del := func(q string) *httptest.ResponseRecorder {
		return ts.do(t, "POST", "/api/recordings/delete-original?path="+q, nil)
	}

	for _, q := range []string{"2026", "nothing", "../x"} {
		if w := del(q); w.Code < 400 {
			t.Errorf("delete original of %q: %d", q, w.Code)
		}
	}
	if w := del("2026/2026-10-11"); w.Code != http.StatusBadRequest {
		t.Errorf("no files made, but deleted: %d", w.Code)
	}
	if !fileExists(filepath.Join(ts.root, "2026", "2026-10-11", "2026-10-11_raw.flac")) {
		t.Error("the original without files is gone")
	}

	w := del("2026/2026-10-04")
	if w.Code != http.StatusOK {
		t.Fatalf("delete original: %d %s", w.Code, w.Body)
	}
	info := decode[folderInfo](t, w)
	if info.Raw != nil || info.OriginalDeleted == nil || len(info.Outputs) != 1 {
		t.Errorf("after: raw %v, deleted %v, outputs %+v", info.Raw, info.OriginalDeleted, info.Outputs)
	}
	if fileExists(filepath.Join(ts.root, "2026", "2026-10-04", "original")) {
		t.Error("original/ is still there")
	}
	if got := ts.entry(filepath.Join(ts.root, "2026", "2026-10-04")); !got.Date || got.Raw {
		t.Errorf("the recording in the list: %+v", got)
	}
	// It cannot be made again, and says why.
	q := ts.processing().start()
	q.Mp3, q.Sections = true, []span{{Start: 1, End: 2}}
	if c := ts.check(filepath.Join(ts.root, "2026", "2026-10-04"), q); c.Error != "the original recording was deleted, so the files cannot be made again" {
		t.Errorf("check: %q", c.Error)
	}
	if w := del("2026/2026-10-04"); w.Code != http.StatusBadRequest {
		t.Errorf("deleted twice: %d", w.Code)
	}
}

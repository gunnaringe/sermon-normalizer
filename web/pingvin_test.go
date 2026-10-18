package web

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gunnaringe/sermon-normalizer/normalizer"
)

// The tables and columns the watcher reads, as Pingvin Share X v2.0.0's
// migrations leave them (other columns left out).
const pingvinSchema = `
CREATE TABLE "ReverseShare" ("id" TEXT NOT NULL PRIMARY KEY, "createdAt" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  "token" TEXT NOT NULL, "shareExpiration" DATETIME NOT NULL, "maxShareSize" TEXT NOT NULL,
  "sendEmailNotification" BOOLEAN NOT NULL, "remainingUses" INTEGER NOT NULL,
  "simplified" BOOLEAN NOT NULL DEFAULT false, "publicAccess" BOOLEAN NOT NULL DEFAULT true, "creatorId" TEXT NOT NULL);
CREATE UNIQUE INDEX "ReverseShare_token_key" ON "ReverseShare"("token");
CREATE TABLE "Share" ("id" TEXT NOT NULL PRIMARY KEY, "createdAt" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  "name" TEXT, "uploadLocked" BOOLEAN NOT NULL DEFAULT false, "isZipReady" BOOLEAN NOT NULL DEFAULT false,
  "views" INTEGER NOT NULL DEFAULT 0, "expiration" DATETIME NOT NULL, "description" TEXT, "removedReason" TEXT,
  "creatorId" TEXT, "reverseShareId" TEXT, "storageProvider" TEXT NOT NULL DEFAULT 'LOCAL');
CREATE TABLE "File" ("id" TEXT NOT NULL PRIMARY KEY, "createdAt" DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  "name" TEXT NOT NULL, "size" TEXT NOT NULL, "shareId" TEXT NOT NULL);
`

type fakePingvin struct {
	t      *testing.T
	sqlite string
	data   string
	n      int
}

func newFakePingvin(t *testing.T) *fakePingvin {
	t.Helper()
	sqlite, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("sqlite3 not in PATH")
	}
	p := &fakePingvin{t: t, sqlite: sqlite, data: t.TempDir()}
	p.sql(pingvinSchema)
	p.sql(`INSERT INTO "ReverseShare" ("id","token","shareExpiration","maxShareSize","sendEmailNotification","remainingUses","creatorId") VALUES
  ('rs1','norkirken-melhus-x7',4102444800000,'0',0,1000,'u'), ('rs2','other_church',4102444800000,'0',0,1000,'u'),
  ('rs3','norkirkenXmelhus',4102444800000,'0',0,1000,'u')`)
	return p
}

func (p *fakePingvin) sql(sql string) {
	p.t.Helper()
	cmd := exec.Command(p.sqlite, filepath.Join(p.data, "pingvin-share.db"), sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		p.t.Fatalf("sqlite3: %v: %s", err, out)
	}
}

// upload adds a finished upload of content (or of the file at from) to a
// share of the given reverse share ("" = none), and returns the file ID.
func (p *fakePingvin) upload(share, reverseShare, name string, content []byte, removed bool) string {
	p.t.Helper()
	p.n++
	id := "0000000" + strconv.Itoa(p.n) + "-89ab-4def-8123-456789abcdef"
	dir := filepath.Join(p.data, "uploads", "shares", share)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		p.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id), content, 0o644); err != nil {
		p.t.Fatal(err)
	}
	rs, rm := "NULL", "NULL"
	if reverseShare != "" {
		rs = "'" + reverseShare + "'"
	}
	if removed {
		rm = "'spam'"
	}
	p.sql(`INSERT OR IGNORE INTO "Share" ("id","expiration","reverseShareId","removedReason","uploadLocked") VALUES ('` +
		share + `',4102444800000,` + rs + `,` + rm + `,1);
INSERT INTO "File" ("id","name","size","shareId") VALUES ('` + id + `','` + strings.ReplaceAll(name, "'", "''") +
		`','` + strconv.Itoa(len(content)) + `','` + share + `')`)
	return id
}

func (p *fakePingvin) config() *PingvinConfig {
	return &PingvinConfig{Data: p.data, Prefix: "norkirken-melhus", SQLite: p.sqlite, Every: time.Hour}
}

func TestPingvinQuery(t *testing.T) {
	fp := newFakePingvin(t)
	ts := newTestServerWith(t, normalizer.FFmpeg{}, fp.config())
	if files, err := ts.pingvin.query(context.Background()); err != nil || len(files) != 0 {
		t.Fatalf("empty: %v %v", files, err)
	}
	a := fp.upload("Ab3dE", "rs1", "service.wav", []byte("a"), false)
	b := fp.upload("Ab3dE", "rs1", "notes.txt", []byte("b"), false)
	fp.upload("Zz9", "rs2", "other.wav", []byte("c"), false)                      // another reverse share
	fp.upload("Yy8", "rs3", "lookalike.wav", []byte("d"), false)                  // _ or % would have matched this
	fp.upload("Xx7", "rs1", "removed.wav", []byte("e"), true)                     // removed by the admin
	c := fp.upload("norkirken-melhus-direct", "", "own.flac", []byte("f"), false) // a share named after it
	files, err := ts.pingvin.query(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, f := range files {
		ids = append(ids, f.ID)
	}
	if !slices.Equal(ids, []string{a, b, c}) || files[0].Name != "service.wav" || files[0].Size != "1" || files[0].Share != "Ab3dE" {
		t.Errorf("files %+v", files)
	}

	for _, bad := range []string{"norkirken_melhus", "x' OR 1=1 --", ""} {
		cfg := fp.config()
		cfg.Prefix = bad
		if _, err := newPingvin(ts.Server, *cfg); err == nil {
			t.Errorf("prefix %q accepted", bad)
		}
	}
}

func TestIntegrationPingvin(t *testing.T) {
	ff := requireFFmpeg(t)
	fp := newFakePingvin(t)
	wav := filepath.Join(t.TempDir(), "a.wav")
	service(t, ff, wav)
	content, _ := os.ReadFile(wav)
	id := fp.upload("Ab3dE", "rs1", "Gudstjeneste 2026-10-04.wav", content, false)
	// A recorder's name without a date: the date comes from the WAV's tags,
	// which must survive the conversion.
	tagged := filepath.Join(t.TempDir(), "tagged.wav")
	if _, err := ff.Run(context.Background(), "-y", "-i", wav, "-t", "5", "-metadata", "date=2026-09-20", "-c", "copy", tagged); err != nil {
		t.Fatal(err)
	}
	taggedContent, _ := os.ReadFile(tagged)
	fp.upload("Tt1", "rs1", "ZOOM0001.WAV", taggedContent, false)
	skipped := fp.upload("Ab3dE", "rs1", "slides.pdf", []byte("pdf"), false)
	// Still being written: the size in the database is not reached yet.
	partial := fp.upload("Cc1", "rs1", "late.wav", content[:1000], false)
	fp.sql(`UPDATE "File" SET "size"='` + strconv.Itoa(len(content)) + `' WHERE "id"='` + partial + `'`)

	ts := newTestServerWith(t, ff, fp.config())
	ts.pingvin.poll(context.Background())

	flac := filepath.Join(ts.inbox, "Gudstjeneste 2026-10-04.flac")
	if same, err := ff.SameAudio(context.Background(), wav, flac); err != nil || !same {
		t.Fatalf("inbox FLAC: same=%v err=%v", same, err)
	}
	if b, _ := os.ReadFile(filepath.Join(fp.data, "uploads", "shares", "Ab3dE", id)); len(b) != len(content) {
		t.Error("Pingvin's file must stay as it is")
	}
	seen, _ := os.ReadFile(filepath.Join(ts.inbox, seenFile))
	if !strings.Contains(string(seen), id) || !strings.Contains(string(seen), skipped) || strings.Contains(string(seen), partial) {
		t.Errorf("seen file %q", seen)
	}
	list := decode[struct{ Files []inboxFile }](t, ts.do(t, "GET", "/api/inbox", nil))
	dates := map[string]string{}
	for _, f := range list.Files {
		dates[f.Name] = f.SuggestedDate + " " + f.DateSource
	}
	if len(list.Files) != 2 || dates["Gudstjeneste 2026-10-04.flac"] != "2026-10-04 name" || dates["ZOOM0001.flac"] != "2026-09-20 metadata" {
		t.Errorf("inbox %v", dates)
	}
	os.Remove(filepath.Join(ts.inbox, "ZOOM0001.flac"))

	// The waveform was started at once; wait for it.
	key, _ := cacheKey(flac)
	for i := 0; !ts.analyses.done(key) && i < 600; i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if !ts.analyses.done(key) {
		t.Fatal("the analysis was not started")
	}
	// Imported, the recording keeps its prepared waveform: no new analysis.
	if rec := ts.do(t, "POST", "/api/inbox/import", importRequest{Name: "Gudstjeneste 2026-10-04.flac", Date: "2026-10-04"}); rec.Code != 200 {
		t.Fatalf("import: %d %s", rec.Code, rec.Body)
	}
	if st := decode[analysisState](t, ts.do(t, "GET", "/api/analysis?path=2026/2026-10-04", nil)); st.State != "done" {
		t.Errorf("after import: %+v", st)
	}

	// Brought in once: removing it from the inbox does not bring it back.
	ts.pingvin.poll(context.Background())
	if entries, _ := os.ReadDir(ts.inbox); len(entries) != 1 { // just the seen file
		t.Errorf("inbox after a second poll: %v", entries)
	}
	// A restart reads what was seen.
	again, err := newPingvin(ts.Server, *fp.config())
	if err != nil || !again.seen[id] {
		t.Errorf("seen after restart: %v %v", again.seen, err)
	}
}

func TestIntegrationUploadConverts(t *testing.T) {
	ff := requireFFmpeg(t)
	ts := newTestServer(t, ff)
	wav := filepath.Join(t.TempDir(), "a.wav")
	service(t, ff, wav)
	content, _ := os.ReadFile(wav)
	size := int64(len(content))
	half := size / 2
	chunk(t, ts, "2026-10-04", "ZOOM0001.WAV", size, 0, string(content[:half]), time.Time{})
	if rec := chunk(t, ts, "2026-10-04", "ZOOM0001.WAV", size, half, string(content[half:]), time.Time{}); rec.Code != 201 {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body)
	}
	// Conversion and analysis start right away; a run waits for the FLAC.
	st := decode[analysisState](t, ts.do(t, "GET", "/api/analysis?path=2026/2026-10-04", nil))
	if st.State != "running" || st.Stage != "convert" {
		t.Errorf("right after the upload: %+v", st)
	}
	q := defaultRequest()
	q.Start = 30
	if pl := ts.check(filepath.Join(ts.root, "2026", "2026-10-04"), q); !strings.Contains(pl.Error, "converted to FLAC") {
		t.Errorf("run during conversion: %+v", pl.Error)
	}
	st = waitAnalysis(t, ts, "2026/2026-10-04")
	dir := filepath.Join(ts.root, "2026", "2026-10-04")
	if st.State != "done" || fileExists(filepath.Join(dir, "2026-10-04_raw.wav")) {
		t.Fatalf("after: %+v", st)
	}
	if same, err := ff.SameAudio(context.Background(), wav, filepath.Join(dir, "2026-10-04_raw.flac")); err != nil || !same {
		t.Errorf("FLAC differs: %v %v", same, err)
	}
}

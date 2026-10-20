package web

import (
	"log"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestTidyNames(t *testing.T) {
	got, err := tidyNames([]string{"kveld", " Kveld ", "Påske", "Aften", "", "påske"})
	if err != nil || !reflect.DeepEqual(got, []string{"Aften", "kveld", "Påske"}) {
		t.Errorf("tidyNames = %q, %v", got, err)
	}
	if _, err := tidyNames([]string{"ok", "a/b"}); err == nil {
		t.Error("a bad name should be refused")
	}
}

// Names are learned when a named sermon is made (upload or rename), only
// while suggestions are on, are kept per church, and can be edited.
func TestNameSuggestions(t *testing.T) {
	data := t.TempDir()
	os.MkdirAll(filepath.Join(data, "main", "Taler"), 0o755)
	cfg := AppConfig{Base: Config{CacheDir: t.TempDir(), Logger: log.New(testLog{t}, "", 0)}, DataDir: data}
	a, err := NewApp(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close) // waits for the analyses the uploads start
	h := a.Handler()
	do(h, "POST", "/api/tenants", `{"name":"Kirke B"}`)
	upload := func(query string) int {
		return do(h, "POST", "/api/upload?"+query+"&name=a.flac&size=3&offset=0", "abc").Code
	}
	names := func(tenant string) NameList {
		return decode[NameList](t, do(h, "GET", "/api/names?tenant="+tenant, ""))
	}

	if got := names("main"); !got.Enabled || len(got.List) != 0 {
		t.Fatalf("start: %+v", got)
	}
	if c := upload("date=2026-10-04&sermon=Kveld"); c != http.StatusCreated {
		t.Fatalf("upload: %d", c)
	}
	if c := upload("date=2026-10-04"); c != http.StatusCreated { // no name: nothing learned
		t.Fatalf("upload: %d", c)
	}
	if c := upload("date=2026-10-11&sermon=kveld"); c != http.StatusCreated { // same name, other case
		t.Fatalf("upload: %d", c)
	}
	if got := names("main"); !reflect.DeepEqual(got.List, []string{"Kveld"}) {
		t.Errorf("learned: %+v", got)
	}
	if got := names("kirke-b"); len(got.List) != 0 {
		t.Errorf("another church got the name: %+v", got)
	}

	// Rename learns too; turning suggestions off stops learning.
	// The upload started an analysis (it fails at once here, without ffmpeg);
	// a rename waits until it has stopped.
	w := do(h, "POST", "/api/rename?path=2026%2F2026-10-04", `{"date":"2026-10-04","name":"Påske"}`)
	for deadline := time.Now().Add(5 * time.Second); w.Code == http.StatusConflict && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
		w = do(h, "POST", "/api/rename?path=2026%2F2026-10-04", `{"date":"2026-10-04","name":"Påske"}`)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", w.Code, w.Body)
	}
	if got := names("main"); !reflect.DeepEqual(got.List, []string{"Kveld", "Påske"}) {
		t.Errorf("after rename: %+v", got)
	}
	if w := do(h, "PUT", "/api/names", `{"enabled":false,"list":["Kveld","Påske"]}`); w.Code != http.StatusOK {
		t.Fatalf("put: %d %s", w.Code, w.Body)
	}
	upload("date=2026-10-18&sermon=Engangs")
	if got := names("main"); got.Enabled || !reflect.DeepEqual(got.List, []string{"Kveld", "Påske"}) {
		t.Errorf("while off: %+v", got)
	}
	// Remove a one-off.
	do(h, "PUT", "/api/names", `{"enabled":true,"list":["Påske"]}`)
	if w := do(h, "PUT", "/api/names", `{"enabled":true,"list":["a/b"]}`); w.Code != http.StatusBadRequest {
		t.Errorf("bad name: %d", w.Code)
	}
	a.Close()

	a, err = NewApp(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if got := decode[NameList](t, do(a.Handler(), "GET", "/api/names?tenant=main", "")); !got.Enabled || !reflect.DeepEqual(got.List, []string{"Påske"}) {
		t.Errorf("after restart: %+v", got)
	}
}

package web

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/gunnaringe/sermon-studio/normalizer"
)

// Shares let people without a login send recordings to a church's inbox, or
// download one finished sermon (a download share, sharedownload.go): an admin
// makes a share in the studio and hands out its code (or a link with the code
// in the fragment); the public share page (ShareHandler, on its own listener)
// takes the code and nothing else. The code is only shown
// when it is made; a church keeps the SHA-256 of it in <church>/shares.json.

const sharesFile = "shares.json"

// ShareOptions turn on the public upload page.
type ShareOptions struct {
	URL      string // public address of the upload page, for links; "" = only the code is shown
	IPHeader string // header the proxy puts the client's address in; "" = the connection's address
	Limits   RateLimits
}

// shareTypes are what a share can accept: what the inbox takes in.
var shareTypes = normalizer.InputTypes()

const (
	defaultShareFileSize = 4 << 30
	maxShareFileSize     = 64 << 30
	shareLogMax          = 200            // uploads listed per share; the counts cover all
	pendingStale         = 24 * time.Hour // an upload that stopped this long ago is dropped
	diskMargin           = 1 << 30        // free space left after an upload
)

// Share is one code and what it allows.
type Share struct {
	ID          string     `json:"id"`
	Kind        string     `json:"kind,omitempty"` // "" = uploads to the inbox, kindDownload = downloads of one recording
	Hash        string     `json:"hash"`           // SHA-256 of the code, hex
	Hint        string     `json:"hint"`           // the code's last four characters, to tell shares apart
	Label       string     `json:"label"`
	Created     time.Time  `json:"created"`
	Expires     *time.Time `json:"expires,omitempty"`
	MaxUploads  int        `json:"maxUploads,omitempty"` // 0 = no limit
	MaxFileSize int64      `json:"maxFileSize"`
	MaxTotal    int64      `json:"maxTotal,omitempty"` // bytes over all uploads; 0 = no limit
	Types       []string   `json:"types,omitempty"`    // the ones it takes, if not AllTypes
	AllTypes    bool       `json:"allTypes,omitempty"` // every type the inbox takes, also ones added later
	Paused      bool       `json:"paused,omitempty"`   // takes no uploads until resumed

	// A download share: the recording (its path, kept up to date, and its id in recording.json), the
	// formats it gives, and how many downloads it allows (0 = no limit). Count and Log are its downloads.
	Recording    string                     `json:"recording,omitempty"`
	RecordingID  string                     `json:"recordingId,omitempty"`
	Formats      []string                   `json:"formats,omitempty"`
	MaxDownloads int                        `json:"maxDownloads,omitempty"`
	Tickets      map[string]*downloadTicket `json:"tickets,omitempty"` // by ticket id

	Count   int                       `json:"count"` // finished uploads
	Bytes   int64                     `json:"bytes"`
	Log     []ShareUpload             `json:"log,omitempty"`
	Pending map[string]*pendingUpload `json:"pending,omitempty"` // by upload id
}

// ShareUpload is a finished upload, or a download of a download share, as listed to the admin.
type ShareUpload struct {
	Name string    `json:"name"` // as it arrived in the inbox; the file downloaded
	Size int64     `json:"size"`
	IP   string    `json:"ip"`
	Time time.Time `json:"time"`
}

// pendingUpload is an upload that has been started; it counts against the
// share's limits until it finishes or goes stale. Kept in shares.json so an
// upload continues after a restart.
type pendingUpload struct {
	Name     string    `json:"name"` // the cleaned name it will get
	Size     int64     `json:"size"`
	Modified int64     `json:"modified,omitempty"` // ms since 1970, from the browser
	IP       string    `json:"ip"`
	Started  time.Time `json:"started"`
}

// defaultEndedSharesDays is how long a share that has ended is kept, unless the church chose otherwise;
// endedSharesChoices are what it can choose (0 = keep them).
const defaultEndedSharesDays = 30

var endedSharesChoices = []int{7, 30, 90, 365, 0}

// endedSharesDays is how many days the church keeps a share that has ended; 0 = until it is removed by hand.
func (a *App) endedSharesDays(t *tenant) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	if d := t.cfg.EndedSharesDays; d != nil {
		return *d
	}
	return defaultEndedSharesDays
}

// endedAt is when a share ended: its expiry, or its last upload or download when that used it up or
// filled it. Zero while it is open or only paused.
func (sh *Share) endedAt(now time.Time) time.Time {
	switch sh.state(now) {
	case "expired":
		return *sh.Expires
	case "used up", "full":
		if n := len(sh.Log); n > 0 {
			return sh.Log[n-1].Time
		}
		return sh.Created
	}
	return time.Time{}
}

// state says whether a share takes uploads: "" if it does, else why not. A
// paused share that has also ended says how it ended: resuming would not open it.
func (sh *Share) state(now time.Time) string {
	switch {
	case sh.Expires != nil && now.After(*sh.Expires):
		return "expired"
	case sh.MaxUploads > 0 && sh.Count >= sh.MaxUploads, sh.MaxDownloads > 0 && sh.Count >= sh.MaxDownloads:
		return "used up"
	case sh.MaxTotal > 0 && sh.Bytes >= sh.MaxTotal:
		return "full"
	case sh.Paused:
		return "paused"
	}
	return ""
}

// types are the file types the share takes.
func (sh *Share) types() []string {
	if sh.AllTypes {
		return shareTypes
	}
	return sh.Types
}

// reserved is what started uploads hold of the share's limits.
func (sh *Share) reserved() (n int, bytes int64) {
	for _, p := range sh.Pending {
		n++
		bytes += p.Size
	}
	return n, bytes
}

// room says whether one more upload of size bytes fits.
func (sh *Share) room(size int64) error {
	n, bytes := sh.reserved()
	if sh.MaxUploads > 0 && sh.Count+n >= sh.MaxUploads {
		return errors.New("this code takes no more uploads (others are still uploading)")
	}
	if size > sh.MaxFileSize {
		return fmt.Errorf("the file is larger than the %s this code allows", sizeText(sh.MaxFileSize))
	}
	if sh.MaxTotal > 0 && sh.Bytes+bytes+size > sh.MaxTotal {
		return errors.New("the file does not fit in what is left for this code")
	}
	return nil
}

func sizeText(n int64) string {
	if n >= 1<<30 {
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	}
	return fmt.Sprintf("%d MB", n>>20)
}

// Codes are 16 characters from an alphabet without look-alikes (no 0/O,
// 1/I), 80 bits, shown in groups of four.
const codeAlphabet = "23456789ABCDEFGHJKLMNPQRSTUVWXYZ"

func newCode() string {
	b := make([]byte, 16)
	rand.Read(b)
	var sb strings.Builder
	for i, c := range b {
		if i > 0 && i%4 == 0 {
			sb.WriteByte('-')
		}
		sb.WriteByte(codeAlphabet[c%32]) // 256 is a multiple of 32: no bias
	}
	return sb.String()
}

// normCode takes a code as typed: any case, with or without dashes and spaces.
func normCode(s string) string {
	s = strings.ToUpper(s)
	return strings.Map(func(r rune) rune {
		if r == '-' || r == ' ' {
			return -1
		}
		return r
	}, s)
}

func hashCode(code string) string {
	sum := sha256.Sum256([]byte(normCode(code)))
	return hex.EncodeToString(sum[:])
}

func randomID(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func readShares(dir string) ([]*Share, error) {
	b, err := os.ReadFile(filepath.Join(dir, sharesFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	var list []*Share
	if err == nil {
		err = json.Unmarshal(b, &list)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", sharesFile, err)
	}
	for _, sh := range list {
		// Made when these two were all there was: it meant all of them.
		if !sh.AllTypes && slices.Equal(sh.Types, []string{".wav", ".flac"}) {
			sh.AllTypes, sh.Types = true, nil
		}
	}
	return list, nil
}

// saveShares writes a church's shares atomically; call with a.shareMu held.
// The root church has no folder: its shares stay in memory.
func (a *App) saveShares(t *tenant) error {
	if t.dir == "" {
		return nil
	}
	b, err := json.MarshalIndent(t.shares, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(t.dir, sharesFile)
	tmp := path + ".partial"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return normalizer.Rename(tmp, path)
}

// findShare is the church and share of a code, or nil.
func (a *App) findShare(code string) (*tenant, *Share) {
	want := []byte(hashCode(code))
	a.mu.Lock()
	tenants := slices.Clone(a.tenants)
	a.mu.Unlock()
	a.shareMu.Lock()
	defer a.shareMu.Unlock()
	for _, t := range tenants {
		for _, sh := range t.shares {
			if subtle.ConstantTimeCompare([]byte(sh.Hash), want) == 1 {
				return t, sh
			}
		}
	}
	return nil, nil
}

// shareView is a share as the admin sees it.
type shareView struct {
	ID          string        `json:"id"`
	Kind        string        `json:"kind"`
	Hint        string        `json:"hint"`
	Label       string        `json:"label"`
	Created     time.Time     `json:"created"`
	Expires     *time.Time    `json:"expires,omitempty"`
	MaxUploads  int           `json:"maxUploads"`
	MaxFileSize int64         `json:"maxFileSize"`
	MaxTotal    int64         `json:"maxTotal"`
	Types       []string      `json:"types"`
	AllTypes    bool          `json:"allTypes"`
	State       string        `json:"state"` // "" = open
	Count       int           `json:"count"`
	Bytes       int64         `json:"bytes"`
	Uploading   int           `json:"uploading"`
	Log         []ShareUpload `json:"log"`

	Recording    string   `json:"recording,omitempty"` // a download share's
	Formats      []string `json:"formats,omitempty"`
	MaxDownloads int      `json:"maxDownloads"`
	Downloading  int      `json:"downloading"`       // files being sent now
	Resumable    int      `json:"resumable"`         // counted downloads that can still be fetched or resumed
	Missing      bool     `json:"missing,omitempty"` // its recording is not there (in the trash)

	Ended    *time.Time `json:"ended,omitempty"`    // when it ended (expired, used up, full)
	RemoveAt *time.Time `json:"removeAt,omitempty"` // when it is removed by itself, if the church does that
}

func (sh *Share) view(now time.Time) shareView {
	n, _ := sh.reserved()
	log := slices.Clone(sh.Log)
	slices.Reverse(log) // newest first
	if log == nil {
		log = []ShareUpload{}
	}
	v := shareView{ID: sh.ID, Kind: sh.Kind, Hint: sh.Hint, Label: sh.Label, Created: sh.Created, Expires: sh.Expires,
		MaxUploads: sh.MaxUploads, MaxFileSize: sh.MaxFileSize, MaxTotal: sh.MaxTotal, Types: sh.types(), AllTypes: sh.AllTypes,
		State: sh.state(now), Count: sh.Count, Bytes: sh.Bytes, Uploading: n, Log: log}
	if sh.Kind == kindDownload {
		v.Kind, v.Types, v.MaxFileSize, v.Recording, v.Formats, v.MaxDownloads = kindDownload, []string{}, 0, sh.Recording, sh.Formats, sh.MaxDownloads
		for _, tk := range sh.Tickets {
			if now.Before(tk.Expires) {
				v.Resumable++
			}
		}
	} else {
		v.Kind = kindUpload
	}
	return v
}

// adminTenant is the church of a request to the studio's share API.
func (a *App) adminTenant(w http.ResponseWriter, r *http.Request) (*tenant, bool) {
	t, ok := a.tenantFor(r)
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("no such church"))
	}
	return t, ok
}

// handleShares lists the church's shares: GET api/shares.
func (a *App) handleShares(w http.ResponseWriter, r *http.Request) {
	t, ok := a.adminTenant(w, r)
	if !ok {
		return
	}
	// ?recording=<path>: only the download shares of that recording
	only, one := "", r.URL.Query().Has("recording")
	if one {
		abs, err := t.srv.resolve(r.URL.Query().Get("recording"))
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		only = abs
	}
	found := a.findRecordings(t, nil)
	days := a.endedSharesDays(t)
	a.shareMu.Lock()
	now := time.Now()
	list := []shareView{}
	for _, sh := range t.shares {
		if one && (sh.Kind != kindDownload || found[sh] != only) {
			continue
		}
		v := sh.view(now)
		v.Missing = sh.Kind == kindDownload && found[sh] == ""
		v.Downloading = a.share.sending[sh.ID]
		if e := sh.endedAt(now); !e.IsZero() {
			v.Ended = &e
			if days > 0 {
				at := e.Add(time.Duration(days) * 24 * time.Hour)
				v.RemoveAt = &at
			}
		}
		list = append(list, v)
	}
	a.shareMu.Unlock()
	slices.Reverse(list) // newest first
	writeJSON(w, http.StatusOK, map[string]any{
		"shares": list, "url": a.cfg.Share.URL, "types": shareTypes,
		"inbox": t.srv.cfg.Inbox != "", "defaultFileSize": defaultShareFileSize,
		"endedDays": days, "endedDaysChoices": endedSharesChoices,
	})
}

// activeFor is how long after its last chunk an upload counts as going on, for the header's number.
const activeFor = 2 * time.Minute

// handleSharesActive is how many uploads and downloads of the church's shares are going on now, for the
// number on the shares icon: GET api/shares/active → {active}. An upload counts while a chunk is being
// received or came in the last activeFor; one that stopped (the page was closed) does not.
func (a *App) handleSharesActive(w http.ResponseWriter, r *http.Request) {
	t, ok := a.adminTenant(w, r)
	if !ok {
		return
	}
	a.shareMu.Lock()
	type upload struct {
		path string
		busy bool
	}
	var uploads []upload
	n := 0
	for _, sh := range t.shares {
		n += a.share.sending[sh.ID]
		for uid := range sh.Pending {
			uploads = append(uploads, upload{partialPath(t, uid), a.share.busy[uid]})
		}
	}
	a.shareMu.Unlock()
	for _, u := range uploads {
		if st, err := os.Stat(u.path); u.busy || (err == nil && time.Since(st.ModTime()) < activeFor) {
			n++
		}
	}
	writeJSON(w, http.StatusOK, map[string]int{"active": n})
}

// handleEndedSharesDays sets how long the church keeps a share that has ended: PUT api/shares/ended-days {days}
// (0 = until it is removed by hand).
func (a *App) handleEndedSharesDays(w http.ResponseWriter, r *http.Request) {
	t, ok := a.adminTenant(w, r)
	if !ok {
		return
	}
	var q struct {
		Days int `json:"days"`
	}
	if err := decodeRequest(r, &q); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if !slices.Contains(endedSharesChoices, q.Days) {
		writeError(w, http.StatusBadRequest, errors.New("choose how long ended shares are kept from the list"))
		return
	}
	if err := a.update(t, func(c *churchConfig) { c.EndedSharesDays = &q.Days }); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"days": q.Days})
}

// handleShareCreate makes a share and returns its code, the only time it is
// shown: POST api/shares.
func (a *App) handleShareCreate(w http.ResponseWriter, r *http.Request) {
	t, ok := a.adminTenant(w, r)
	if !ok {
		return
	}
	var q struct {
		Kind         string     `json:"kind"` // "" or kindUpload, kindDownload
		Recording    string     `json:"recording"`
		Formats      []string   `json:"formats"`
		MaxDownloads int        `json:"maxDownloads"`
		Label        string     `json:"label"`
		Expires      *time.Time `json:"expires"` // nil = never
		MaxUploads   int        `json:"maxUploads"`
		MaxFileSize  int64      `json:"maxFileSize"`
		MaxTotal     int64      `json:"maxTotal"`
		Types        []string   `json:"types"`
		AllTypes     bool       `json:"allTypes"`
	}
	if err := decodeRequest(r, &q); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	label := strings.Join(strings.Fields(q.Label), " ")
	if q.Kind == kindDownload {
		a.createDownloadShare(w, t, label, q.Recording, q.Formats, q.MaxDownloads, q.Expires)
		return
	}
	if q.Kind != "" && q.Kind != kindUpload {
		writeError(w, http.StatusBadRequest, errors.New("unknown kind of share"))
		return
	}
	if t.srv.cfg.Inbox == "" {
		writeError(w, http.StatusBadRequest, errNoInbox)
		return
	}
	var types []string
	for _, ext := range shareTypes {
		if slices.Contains(q.Types, ext) {
			types = append(types, ext)
		}
	}
	if q.MaxFileSize == 0 {
		q.MaxFileSize = defaultShareFileSize
	}
	now := time.Now()
	err := checkLabel(label)
	switch {
	case err != nil:
	case !q.AllTypes && (len(types) == 0 || len(types) != len(q.Types)):
		err = fmt.Errorf("choose file types from %s", strings.Join(shareTypes, ", "))
	case q.MaxFileSize < 1<<20 || q.MaxFileSize > maxShareFileSize:
		err = fmt.Errorf("the max file size must be between 1 MB and %s", sizeText(maxShareFileSize))
	case q.MaxUploads < 0 || q.MaxTotal < 0:
		err = errors.New("limits cannot be negative")
	case q.Expires != nil && !q.Expires.After(now):
		err = errors.New("the expiry is in the past")
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	sh := &Share{MaxUploads: q.MaxUploads, MaxFileSize: q.MaxFileSize, MaxTotal: q.MaxTotal, Types: types, AllTypes: q.AllTypes}
	if q.AllTypes {
		sh.Types = nil
	}
	a.addShare(w, t, sh, label, q.Expires)
}

func checkLabel(label string) error {
	if label == "" || len(label) > 80 || strings.ContainsFunc(label, func(r rune) bool { return r < ' ' }) {
		return errors.New("give the share a name (up to 80 characters)")
	}
	return nil
}

// addShare gives a checked share its code, id and times, keeps it, and returns the code.
func (a *App) addShare(w http.ResponseWriter, t *tenant, sh *Share, label string, expires *time.Time) {
	now := time.Now()
	code := newCode()
	sh.ID, sh.Hash, sh.Hint, sh.Label, sh.Created = randomID(6), hashCode(code), code[len(code)-4:], label, now.UTC()
	if expires != nil {
		e := expires.UTC()
		sh.Expires = &e
	}
	a.shareMu.Lock()
	t.shares = append(t.shares, sh)
	err := a.saveShares(t)
	if err != nil {
		t.shares = t.shares[:len(t.shares)-1]
	}
	view := sh.view(now)
	a.shareMu.Unlock()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	a.cfg.Base.Logger.Printf("share: %s made %s share %s (%q)", t.id, view.Kind, sh.ID, label)
	writeJSON(w, http.StatusCreated, map[string]any{"share": view, "code": code, "url": a.cfg.Share.URL})
}

// handleSharePause stops a share's uploads until it is resumed:
// POST api/shares/{id}/pause. Uploads in progress are dropped, with their files.
func (a *App) handleSharePause(w http.ResponseWriter, r *http.Request) {
	a.setPaused(w, r, true)
}

// handleShareResume lets a paused share take uploads again:
// POST api/shares/{id}/resume.
func (a *App) handleShareResume(w http.ResponseWriter, r *http.Request) {
	a.setPaused(w, r, false)
}

func (a *App) setPaused(w http.ResponseWriter, r *http.Request, pause bool) {
	t, ok := a.adminTenant(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	a.shareMu.Lock()
	defer a.shareMu.Unlock()
	i := slices.IndexFunc(t.shares, func(sh *Share) bool { return sh.ID == id })
	if i < 0 {
		writeError(w, http.StatusNotFound, errors.New("no such share"))
		return
	}
	sh := t.shares[i]
	switch state := sh.state(time.Now()); {
	case pause && state != "":
		writeError(w, http.StatusConflict, errors.New("this share has ended"))
		return
	case !pause && state != "paused":
		writeError(w, http.StatusConflict, errors.New("only a paused share can be resumed"))
		return
	}
	pending, tickets := sh.Pending, sh.Tickets
	sh.Paused = pause
	if pause {
		sh.Pending, sh.Tickets = nil, nil // downloads that were going on stop too
	}
	if err := a.saveShares(t); err != nil {
		sh.Paused, sh.Pending, sh.Tickets = !pause, pending, tickets
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if pause {
		for uid := range pending {
			if !a.share.busy[uid] {
				normalizer.Remove(partialPath(t, uid))
			}
		}
	}
	a.cfg.Base.Logger.Printf("share: %s %s share %s (%q)", t.id, map[bool]string{true: "paused", false: "resumed"}[pause], sh.ID, sh.Label)
	writeJSON(w, http.StatusOK, map[string]string{"id": id})
}

// handleShareRemove deletes a share that takes no uploads (paused, expired,
// used up or full), with its log: POST api/shares/{id}/remove. Its code then
// works no more. An open share is paused first.
func (a *App) handleShareRemove(w http.ResponseWriter, r *http.Request) {
	t, ok := a.adminTenant(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	n, open, err := a.removeShares(t, func(sh *Share) bool { return sh.ID == id })
	switch {
	case err != nil:
		writeError(w, http.StatusInternalServerError, err)
	case open:
		writeError(w, http.StatusConflict, errors.New("this share is still open: pause it first"))
	case n == 0:
		writeError(w, http.StatusNotFound, errors.New("no such share"))
	default:
		writeJSON(w, http.StatusOK, map[string]int{"removed": n})
	}
}

// handleShareRemoveEnded deletes every share that has ended (expired, used up
// or full): POST api/shares/remove-ended. Paused shares stay.
func (a *App) handleShareRemoveEnded(w http.ResponseWriter, r *http.Request) {
	t, ok := a.adminTenant(w, r)
	if !ok {
		return
	}
	n, _, err := a.removeShares(t, func(sh *Share) bool { return sh.state(time.Now()) != "paused" })
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"removed": n})
}

// removeShares drops the church's paused or ended shares that pick says yes to, and the
// partial files of their uploads. A share that still takes uploads is left
// alone; open says one was picked.
func (a *App) removeShares(t *tenant, pick func(*Share) bool) (n int, open bool, err error) {
	a.shareMu.Lock()
	defer a.shareMu.Unlock()
	now := time.Now()
	var keep, gone []*Share
	for _, sh := range t.shares {
		switch {
		case !pick(sh):
			keep = append(keep, sh)
		case sh.state(now) == "":
			keep = append(keep, sh)
			open = true
		default:
			gone = append(gone, sh)
		}
	}
	if len(gone) == 0 {
		return 0, open, nil
	}
	old := t.shares
	t.shares = keep
	if err := a.saveShares(t); err != nil {
		t.shares = old
		return 0, open, err
	}
	for _, sh := range gone {
		for uid := range sh.Pending {
			if !a.share.busy[uid] {
				normalizer.Remove(partialPath(t, uid))
			}
		}
		a.cfg.Base.Logger.Printf("share: %s removed share %s (%q)", t.id, sh.ID, sh.Label)
	}
	return len(gone), open, nil
}

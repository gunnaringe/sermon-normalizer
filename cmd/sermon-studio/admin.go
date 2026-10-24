package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/gunnaringe/sermon-studio/web"
)

// adminCmd is `sermon-studio admin`: the site administrator's tools, run
// where the studio runs. They talk to the running studio over its admin
// socket in the data dir (web.AdminSocket), so a change takes effect at once.
type adminCmd struct {
	DataDir string `required:"" help:"The studio's --data-dir (where its admin socket is)."`
	JSON    bool   `name:"json" help:"Print JSON, for scripts."`

	Church  churchCmd  `cmd:"" help:"Add, list and rename churches."`
	Members membersCmd `cmd:"" help:"List a church's members."`
	Invite  inviteCmd  `cmd:"" help:"Print an invite link to a church (single use)."`
	Remove  removeCmd  `cmd:"" help:"Take a member out of a church."`

	out io.Writer // os.Stdout; tests set it
}

type churchCmd struct {
	Create churchCreateCmd `cmd:"" help:"Add a church; prints an invite link for its first admin."`
	List   churchListCmd   `cmd:"" help:"List the churches."`
	Rename churchRenameCmd `cmd:"" help:"Change a church's name."`
}

type churchCreateCmd struct {
	ID   string `arg:"" help:"The church's id: its folder and address (lowercase letters, digits and hyphens)."`
	Name string `required:"" help:"The church's name, as the studio shows it."`
}

type churchListCmd struct{}

type churchRenameCmd struct {
	ID   string `arg:"" help:"The church's id."`
	Name string `arg:"" help:"Its new name."`
}

type membersCmd struct {
	Church string `arg:"" help:"The church's id."`
}

type inviteCmd struct {
	Church  string `arg:"" help:"The church's id."`
	Role    string `enum:"admin,member" default:"member" help:"admin or member."`
	Expires string `default:"7d" help:"How long the link works: 7d, 48h, 30m."`
}

type removeCmd struct {
	Church string `arg:"" help:"The church's id."`
	Member string `arg:"" help:"The member: the id 'admin members' shows, or their email."`
}

// call sends one request to the running studio's admin socket and decodes
// its answer into out.
func (a *adminCmd) call(method, path string, body, out any) error {
	sock := web.AdminSocket(a.DataDir)
	client := &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sock)
		}}}
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, "http://admin"+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		if _, serr := os.Stat(sock); serr != nil {
			return fmt.Errorf("the studio is not running on %s (no %s): start it, then try again", a.DataDir, sock)
		}
		return fmt.Errorf("the studio does not answer on %s: %w", sock, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		json.NewDecoder(resp.Body).Decode(&e)
		if e.Error == "" {
			e.Error = resp.Status
		}
		return errors.New(e.Error)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// print writes v as JSON with --json, else as text.
func (a *adminCmd) print(v any, text func(w io.Writer)) error {
	w := a.out
	if w == nil {
		w = os.Stdout
	}
	if a.JSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	text(tw)
	return tw.Flush()
}

func printInvite(w io.Writer, inv web.AdminInvite) {
	fmt.Fprintf(w, "%s\n(single use, as %s, until %s)\n", inv.Link, inv.Role, inv.Expires.Local().Format("2006-01-02 15:04"))
}

func (c *churchCreateCmd) Run(a *adminCmd) error {
	var out struct {
		Church web.TenantInfo   `json:"church"`
		Invite *web.AdminInvite `json:"invite"`
	}
	if err := a.call("POST", "/churches", map[string]string{"id": c.ID, "name": c.Name}, &out); err != nil {
		return err
	}
	return a.print(out, func(w io.Writer) {
		fmt.Fprintf(w, "Added %s (%s).\n", out.Church.Name, out.Church.ID)
		if out.Invite != nil {
			fmt.Fprintln(w, "Invite link for its first admin:")
			printInvite(w, *out.Invite)
		}
	})
}

func (c *churchListCmd) Run(a *adminCmd) error {
	var out []web.AdminChurch
	if err := a.call("GET", "/churches", nil, &out); err != nil {
		return err
	}
	return a.print(out, func(w io.Writer) {
		fmt.Fprintln(w, "ID\tNAME\tMEMBERS\tADMINS")
		for _, c := range out {
			fmt.Fprintf(w, "%s\t%s\t%d\t%d\n", c.ID, c.Name, c.Members, c.Admins)
		}
	})
}

func (c *churchRenameCmd) Run(a *adminCmd) error {
	var out web.TenantInfo
	if err := a.call("POST", "/churches/"+url.PathEscape(c.ID)+"/rename", map[string]string{"name": c.Name}, &out); err != nil {
		return err
	}
	return a.print(out, func(w io.Writer) { fmt.Fprintf(w, "%s is now called %s.\n", out.ID, out.Name) })
}

// adminMember is a member as the admin socket lists them.
type adminMember struct {
	ID     string    `json:"id"`
	Name   string    `json:"name"`
	Email  string    `json:"email,omitempty"`
	Role   string    `json:"role"`
	Joined time.Time `json:"joined"`
}

func (c *membersCmd) Run(a *adminCmd) error {
	var out []adminMember
	if err := a.call("GET", "/churches/"+url.PathEscape(c.Church)+"/members", nil, &out); err != nil {
		return err
	}
	return a.print(out, func(w io.Writer) {
		fmt.Fprintln(w, "ID\tNAME\tEMAIL\tROLE\tJOINED")
		for _, m := range out {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", m.ID, m.Name, m.Email, m.Role, m.Joined.Local().Format("2006-01-02"))
		}
	})
}

func (c *inviteCmd) Run(a *adminCmd) error {
	valid, err := parseDuration(c.Expires)
	if err != nil {
		return fmt.Errorf("--expires: %w", err)
	}
	var out web.AdminInvite
	body := map[string]any{"role": c.Role, "seconds": int64(valid / time.Second)}
	if err := a.call("POST", "/churches/"+url.PathEscape(c.Church)+"/invites", body, &out); err != nil {
		return err
	}
	return a.print(out, func(w io.Writer) { printInvite(w, out) })
}

func (c *removeCmd) Run(a *adminCmd) error {
	var out struct {
		Removed string `json:"removed"`
	}
	path := "/churches/" + url.PathEscape(c.Church) + "/members/" + url.PathEscape(c.Member) + "/remove"
	if err := a.call("POST", path, nil, &out); err != nil {
		return err
	}
	return a.print(out, func(w io.Writer) { fmt.Fprintf(w, "%s is no longer a member of %s.\n", out.Removed, c.Church) })
}

// parseDuration is time.ParseDuration with days (7d).
func parseDuration(s string) (time.Duration, error) {
	if n, ok := strings.CutSuffix(s, "d"); ok {
		var days int
		if _, err := fmt.Sscanf(n, "%d", &days); err != nil || days <= 0 {
			return 0, fmt.Errorf("%q is not a number of days", s)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}

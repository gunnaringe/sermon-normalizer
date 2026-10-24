package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gunnaringe/sermon-studio/web"
)

// adminCmd is `sermon-studio admin`: the site administrator's tools, run
// where the studio runs. They work on --data-dir while the server runs.
type adminCmd struct {
	Invite inviteCmd `cmd:"" help:"Print an invite link to a church (single use)."`
}

type inviteCmd struct {
	Church    string `arg:"" help:"The church's id (its folder in --data-dir)."`
	DataDir   string `required:"" help:"The studio's --data-dir."`
	PublicURL string `required:"" help:"The studio's --public-url, for the link."`
	Role      string `enum:"admin,member" default:"member" help:"admin or member."`
	Expires   string `default:"7d" help:"How long the link works: 7d, 48h, 30m."`
}

func (c *inviteCmd) Run() error {
	valid, err := parseDuration(c.Expires)
	if err != nil {
		return fmt.Errorf("--expires: %w", err)
	}
	token, err := web.CreateInvite(c.DataDir, c.Church, c.Role, valid, "admin cli")
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "%s/invite.html#%s\n(single use, as %s, until %s)\n", strings.TrimSuffix(c.PublicURL, "/"), token,
		c.Role, time.Now().Add(valid).Format("2006-01-02 15:04"))
	return nil
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

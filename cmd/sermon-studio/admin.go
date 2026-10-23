package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gunnaringe/sermon-studio/web"
)

// runAdmin is `sermon-studio admin`: the site administrator's tool, run
// where the studio runs. It works on --data-dir while the server runs.
func runAdmin(args []string) error {
	usage := errors.New("usage: sermon-studio admin invite --data-dir <dir> --public-url <url> [--role admin|member] [--expires 7d] <church>")
	if len(args) == 0 || args[0] != "invite" {
		return usage
	}
	fs := flag.NewFlagSet("admin invite", flag.ContinueOnError)
	dataDir := fs.String("data-dir", "", "the studio's --data-dir")
	publicURL := fs.String("public-url", "", "the studio's --public-url, for the link")
	role := fs.String("role", web.RoleMember, "admin or member")
	expires := fs.String("expires", "7d", "how long the link works: 7d, 48h, 30m")
	// The church may come before, between or after the flags.
	var names []string
	for rest := args[1:]; ; {
		if err := fs.Parse(rest); err != nil {
			return err
		}
		if fs.NArg() == 0 {
			break
		}
		names, rest = append(names, fs.Arg(0)), fs.Args()[1:]
	}
	if len(names) != 1 {
		return usage
	}
	church := names[0]
	if *dataDir == "" || *publicURL == "" {
		return usage
	}
	valid, err := parseDuration(*expires)
	if err != nil {
		return fmt.Errorf("--expires: %w", err)
	}
	token, err := web.CreateInvite(*dataDir, church, *role, valid, "admin cli")
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "%s/invite.html#%s\n(single use, as %s, until %s)\n", strings.TrimSuffix(*publicURL, "/"), token,
		*role, time.Now().Add(valid).Format("2006-01-02 15:04"))
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

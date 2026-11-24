package launchd

import (
	"strings"
	"testing"
	"time"
)

func TestPlist(t *testing.T) {
	p := Agent{
		Program:  "/opt/homebrew/bin/cg-dev-auth",
		Args:     []string{"agent"},
		Interval: 5 * time.Minute,
		Env:      map[string]string{"PATH": "/a&b:/usr/bin"},
		Log:      "/Users/me/Library/Logs/cg-dev-auth.log",
	}.Plist()
	for _, want := range []string{
		"<string>dev.cg-dev-auth.refresh</string>",
		"<string>/opt/homebrew/bin/cg-dev-auth</string>\n\t\t<string>agent</string>",
		"<string>/a&amp;b:/usr/bin</string>",
		"<key>StartInterval</key>\n\t<integer>300</integer>",
		"<key>RunAtLoad</key>\n\t<true/>",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("missing %q in:\n%s", want, p)
		}
	}
}

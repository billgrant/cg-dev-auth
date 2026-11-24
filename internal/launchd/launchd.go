// Package launchd renders the macOS LaunchAgent that keeps tokens fresh.
package launchd

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Label identifies the LaunchAgent.
const Label = "dev.cg-dev-auth.refresh"

// PlistPath returns the LaunchAgent plist path for home.
func PlistPath(home string) string {
	return filepath.Join(home, "Library", "LaunchAgents", Label+".plist")
}

// LogPath returns the agent's log file for home.
func LogPath(home string) string {
	return filepath.Join(home, "Library", "Logs", "cg-dev-auth.log")
}

// Agent describes the LaunchAgent.
type Agent struct {
	// Program is the absolute path of the cg-dev-auth binary.
	Program string
	// Args follow Program.
	Args []string
	// Interval between runs. launchd also runs a job missed while the Mac
	// slept once it wakes.
	Interval time.Duration
	// Env is the job's environment; launchd's default PATH is minimal, so
	// PATH must include chainctl's directory.
	Env map[string]string
	// Log receives stdout and stderr.
	Log string
}

func esc(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		case '\'':
			b.WriteString("&apos;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Plist renders the LaunchAgent property list.
func (a Agent) Plist() string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
`)
	fmt.Fprintf(&b, "\t<key>Label</key>\n\t<string>%s</string>\n", esc(Label))
	b.WriteString("\t<key>ProgramArguments</key>\n\t<array>\n")
	for _, s := range append([]string{a.Program}, a.Args...) {
		fmt.Fprintf(&b, "\t\t<string>%s</string>\n", esc(s))
	}
	b.WriteString("\t</array>\n")
	if len(a.Env) > 0 {
		keys := make([]string, 0, len(a.Env))
		for k := range a.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteString("\t<key>EnvironmentVariables</key>\n\t<dict>\n")
		for _, k := range keys {
			fmt.Fprintf(&b, "\t\t<key>%s</key>\n\t\t<string>%s</string>\n", esc(k), esc(a.Env[k]))
		}
		b.WriteString("\t</dict>\n")
	}
	b.WriteString("\t<key>RunAtLoad</key>\n\t<true/>\n")
	fmt.Fprintf(&b, "\t<key>StartInterval</key>\n\t<integer>%d</integer>\n", int(a.Interval.Seconds()))
	fmt.Fprintf(&b, "\t<key>StandardOutPath</key>\n\t<string>%s</string>\n", esc(a.Log))
	fmt.Fprintf(&b, "\t<key>StandardErrorPath</key>\n\t<string>%s</string>\n", esc(a.Log))
	b.WriteString("\t<key>ProcessType</key>\n\t<string>Background</string>\n")
	b.WriteString("</dict>\n</plist>\n")
	return b.String()
}

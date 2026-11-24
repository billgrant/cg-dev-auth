// Package npmrc edits a user .npmrc, which npm and Yarn Classic read.
//
// cg-dev-auth owns one fenced block, placed at the end of the file because npm
// lets the last occurrence of a key win. Everything outside the block is left
// byte for byte as it was.
package npmrc

import (
	"fmt"
	"strings"
)

const (
	BeginMarker = "# >>> cg-dev-auth >>> (managed block: edit with cg-dev-auth, not by hand)"
	EndMarker   = "# <<< cg-dev-auth <<<"
)

// Settings is what the managed block contains.
type Settings struct {
	// Registry is the registry URL, with a trailing slash.
	Registry string
	// Token is the bearer token; empty writes the block without a token line.
	Token string
	// AlwaysAuth adds always-auth=true for the registry. Yarn Classic needs
	// it; npm 11+ warns that it is an unknown key, so it is written only when
	// Yarn Classic is in use.
	AlwaysAuth bool
}

// authPrefix turns https://host/path/ into //host/path/, npm's per-registry
// key prefix.
func authPrefix(registry string) string {
	p := registry
	if i := strings.Index(p, "://"); i >= 0 {
		p = p[i+1:]
	}
	if !strings.HasSuffix(p, "/") {
		p += "/"
	}
	return p
}

func block(s Settings) string {
	prefix := authPrefix(s.Registry)
	var b strings.Builder
	b.WriteString(BeginMarker + "\n")
	fmt.Fprintf(&b, "registry=%s\n", s.Registry)
	if s.Token != "" {
		fmt.Fprintf(&b, "%s:_authToken=%s\n", prefix, s.Token)
	}
	if s.AlwaysAuth {
		fmt.Fprintf(&b, "%s:always-auth=true\n", prefix)
	}
	b.WriteString(EndMarker + "\n")
	return b.String()
}

// split returns the content before the managed block, the block (without
// markers), and the content after it. found is false when there is no block.
func split(content string) (before, inner, after string, found bool, err error) {
	start := strings.Index(content, BeginMarker)
	if start < 0 {
		if strings.Contains(content, EndMarker) {
			return "", "", "", false, fmt.Errorf("found %q without its begin marker; fix the file by hand", EndMarker)
		}
		return content, "", "", false, nil
	}
	rest := content[start:]
	end := strings.Index(rest, EndMarker)
	if end < 0 {
		return "", "", "", false, fmt.Errorf("found the cg-dev-auth begin marker without its end marker; fix the file by hand")
	}
	endAbs := start + end + len(EndMarker)
	if endAbs < len(content) && content[endAbs] == '\n' {
		endAbs++
	}
	innerStart := start + len(BeginMarker)
	return content[:start], content[innerStart : start+end], content[endAbs:], true, nil
}

// Apply returns content with the managed block set to s, moved to the end.
func Apply(content string, s Settings) (string, error) {
	before, _, after, _, err := split(content)
	if err != nil {
		return "", err
	}
	rest := before + after
	if rest != "" && !strings.HasSuffix(rest, "\n") {
		rest += "\n"
	}
	return rest + block(s), nil
}

// Remove returns content without the managed block.
func Remove(content string) (string, error) {
	before, _, after, _, err := split(content)
	if err != nil {
		return "", err
	}
	return before + after, nil
}

// Read returns the settings in the managed block, and whether there is one.
func Read(content string) (Settings, bool, error) {
	_, inner, _, found, err := split(content)
	if err != nil || !found {
		return Settings{}, found, err
	}
	var s Settings
	for _, line := range strings.Split(inner, "\n") {
		line = strings.TrimSpace(line)
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch {
		case k == "registry":
			s.Registry = v
		case strings.HasSuffix(k, ":_authToken"):
			s.Token = v
		case strings.HasSuffix(k, ":always-auth"):
			s.AlwaysAuth = v == "true"
		}
	}
	return s, true, nil
}

// Conflicts returns lines outside the managed block that set registry or an
// auth key for the managed registry. They are overridden by the block but
// worth reporting, since they show stale or competing config.
func Conflicts(content, registry string) []string {
	before, _, after, _, err := split(content)
	if err != nil {
		return nil
	}
	prefix := authPrefix(registry)
	var out []string
	for _, line := range strings.Split(before+after, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, ";") {
			continue
		}
		k, _, _ := strings.Cut(t, "=")
		k = strings.TrimSpace(k)
		if k == "registry" || strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	return out
}

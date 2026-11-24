// Package uvtoml edits a user uv.toml.
//
// TOML requires top-level keys to come before any table, so cg-dev-auth owns
// two fenced blocks: one at the top of the file for keyring-provider, and one
// at the end for its [[index]] table. A competing top-level keyring-provider
// line is commented out (and restored on removal) because TOML rejects
// duplicate keys. The result is parsed before it is returned, so an edit
// never leaves an invalid file.
package uvtoml

import (
	"fmt"
	"strings"

	"github.com/BurntSushi/toml"
)

const (
	topBegin    = "# >>> cg-dev-auth >>> (managed block: edit with cg-dev-auth, not by hand)"
	topEnd      = "# <<< cg-dev-auth <<<"
	indexBegin  = "# >>> cg-dev-auth index >>> (managed block: edit with cg-dev-auth, not by hand)"
	indexEnd    = "# <<< cg-dev-auth index <<<"
	disabledTag = "# cg-dev-auth disabled: "

	// IndexName is the name of the managed [[index]].
	IndexName = "chainguard"
)

// Settings is what the managed blocks contain.
type Settings struct {
	// IndexURL is the index URL, including the _token@ username the keyring
	// subprocess provider needs.
	IndexURL string
}

// cut removes the fenced block [begin, end] from content.
func cut(content, begin, end string) (string, error) {
	s := strings.Index(content, begin)
	if s < 0 {
		if strings.Contains(content, end) {
			return "", fmt.Errorf("found %q without its begin marker; fix the file by hand", end)
		}
		return content, nil
	}
	e := strings.Index(content[s:], end)
	if e < 0 {
		return "", fmt.Errorf("found a cg-dev-auth begin marker without its end marker; fix the file by hand")
	}
	stop := s + e + len(end)
	if stop < len(content) && content[stop] == '\n' {
		stop++
	}
	return content[:s] + content[stop:], nil
}

func strip(content string) (string, error) {
	c, err := cut(content, topBegin, topEnd)
	if err != nil {
		return "", err
	}
	return cut(c, indexBegin, indexEnd)
}

// isTopLevelKey reports whether line sets key, and whether it sits before the
// first table header (only those lines are top-level).
func mapTopLevel(content string, fn func(line string) string) string {
	lines := strings.SplitAfter(content, "\n")
	inTable := false
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "[") {
			inTable = true
		}
		if !inTable {
			lines[i] = fn(l)
		}
	}
	return strings.Join(lines, "")
}

func setsKey(line, key string) bool {
	t := strings.TrimSpace(line)
	if !strings.HasPrefix(t, key) {
		return false
	}
	rest := strings.TrimSpace(t[len(key):])
	return strings.HasPrefix(rest, "=")
}

// Apply returns content with the managed blocks set to s.
func Apply(content string, s Settings) (string, error) {
	rest, err := strip(content)
	if err != nil {
		return "", err
	}
	rest = mapTopLevel(rest, func(l string) string {
		if setsKey(l, "keyring-provider") {
			return disabledTag + l
		}
		return l
	})
	if rest != "" && !strings.HasSuffix(rest, "\n") {
		rest += "\n"
	}
	top := topBegin + "\n" + `keyring-provider = "subprocess"` + "\n" + topEnd + "\n"
	idx := indexBegin + "\n" +
		"[[index]]\n" +
		fmt.Sprintf("name = %q\n", IndexName) +
		fmt.Sprintf("url = %q\n", s.IndexURL) +
		"default = true\n" +
		`authenticate = "always"` + "\n" +
		indexEnd + "\n"
	out := top + rest + idx
	if _, err := Parse(out); err != nil {
		return "", fmt.Errorf("refusing to write an invalid uv.toml: %w", err)
	}
	return out, nil
}

// Remove returns content without the managed blocks and with lines it had
// disabled put back.
func Remove(content string) (string, error) {
	rest, err := strip(content)
	if err != nil {
		return "", err
	}
	return mapTopLevel(rest, func(l string) string {
		return strings.Replace(l, disabledTag, "", 1)
	}), nil
}

// Managed reports whether content has the managed blocks.
func Managed(content string) bool {
	return strings.Contains(content, topBegin) || strings.Contains(content, indexBegin)
}

// Index is an [[index]] entry.
type Index struct {
	Name         string `toml:"name"`
	URL          string `toml:"url"`
	Default      bool   `toml:"default"`
	Explicit     bool   `toml:"explicit"`
	Authenticate string `toml:"authenticate"`
}

// Info is what status reports from a uv.toml.
type Info struct {
	KeyringProvider string  `toml:"keyring-provider"`
	Index           []Index `toml:"index"`
	// Legacy pip-style settings, which compete with [[index]].
	IndexURL      string   `toml:"index-url"`
	ExtraIndexURL []string `toml:"extra-index-url"`
}

// Parse decodes the settings status cares about.
func Parse(content string) (Info, error) {
	var info Info
	_, err := toml.Decode(content, &info)
	return info, err
}

// Managed returns the managed [[index]] entry, if present.
func (i Info) Managed() (Index, bool) {
	for _, ix := range i.Index {
		if ix.Name == IndexName {
			return ix, true
		}
	}
	return Index{}, false
}

// CompetingDefaults returns the URLs of other indexes marked default = true.
// Status reports them instead of guessing which one uv picks.
func (i Info) CompetingDefaults() []string {
	var out []string
	for _, ix := range i.Index {
		if ix.Default && ix.Name != IndexName {
			out = append(out, ix.URL)
		}
	}
	return out
}

// Package pipconf edits keys in the [global] section of a pip.conf.
//
// pip reads the file with Python's configparser, which rejects a duplicated
// section, so keys are edited in place inside [global] rather than appended
// in a block of their own. Lines that are not touched are kept verbatim.
package pipconf

import (
	"strings"
)

const section = "global"

func splitLines(content string) []string {
	if content == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(content, "\n"), "\n")
}

func joinLines(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

func isComment(t string) bool { return strings.HasPrefix(t, "#") || strings.HasPrefix(t, ";") }

// sectionName returns the name of a [section] header line.
func sectionName(line string) (string, bool) {
	t := strings.TrimSpace(line)
	if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
		return strings.TrimSpace(t[1 : len(t)-1]), true
	}
	return "", false
}

// normKey normalizes a pip option name: pip accepts index_url and index-url.
func normKey(k string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(k), "_", "-"))
}

// keyOf returns the normalized key of an option line. Continuation lines
// (indented) and comments are not option lines.
func keyOf(line string) (key, value string, ok bool) {
	if line == "" || line[0] == ' ' || line[0] == '\t' {
		return "", "", false
	}
	t := strings.TrimSpace(line)
	if t == "" || isComment(t) {
		return "", "", false
	}
	i := strings.IndexAny(t, "=:")
	if i < 0 {
		return "", "", false
	}
	return normKey(t[:i]), strings.TrimSpace(t[i+1:]), true
}

func isContinuation(line string) bool {
	return line != "" && (line[0] == ' ' || line[0] == '\t') && strings.TrimSpace(line) != ""
}

// bounds returns the index of the [global] header and the index just past
// its last line. found is false when there is no [global] section.
func bounds(lines []string) (start, end int, found bool) {
	start = -1
	for i, l := range lines {
		name, ok := sectionName(l)
		if !ok {
			continue
		}
		if start >= 0 {
			return start, i, true
		}
		if strings.EqualFold(name, section) {
			start = i
		}
	}
	if start < 0 {
		return -1, -1, false
	}
	return start, len(lines), true
}

// find returns the line index of key inside [global], the number of lines
// it spans (including continuation lines) and its value, with continuation
// lines joined by newlines.
func find(lines []string, key string) (idx, span int, value string, found bool) {
	start, end, ok := bounds(lines)
	if !ok {
		return 0, 0, "", false
	}
	key = normKey(key)
	for i := start + 1; i < end; i++ {
		k, v, ok := keyOf(lines[i])
		if !ok || k != key {
			continue
		}
		span = 1
		vals := []string{}
		if v != "" {
			vals = append(vals, v)
		}
		for i+span < end && isContinuation(lines[i+span]) {
			vals = append(vals, strings.TrimSpace(lines[i+span]))
			span++
		}
		return i, span, strings.Join(vals, "\n"), true
	}
	return 0, 0, "", false
}

// format renders key = value; a multi-line value becomes continuation lines.
func format(key, value string) []string {
	parts := strings.Split(value, "\n")
	out := []string{key + " = " + parts[0]}
	for _, p := range parts[1:] {
		out = append(out, "    "+p)
	}
	return out
}

// Get returns the value of key in [global].
func Get(content, key string) (string, bool) {
	_, _, v, ok := find(splitLines(content), key)
	return v, ok
}

// Set sets key in [global] to value and returns the new content and the
// previous value (nil when the key was absent). A [global] section is created
// when missing.
func Set(content, key, value string) (string, *string) {
	lines := splitLines(content)
	if idx, span, old, ok := find(lines, key); ok {
		prev := old
		repl := append(append([]string{}, lines[:idx]...), format(key, value)...)
		lines = append(repl, lines[idx+span:]...)
		return joinLines(lines), &prev
	}
	start, end, ok := bounds(lines)
	if !ok {
		if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
			lines = append(lines, "")
		}
		lines = append(lines, "["+section+"]")
		lines = append(lines, format(key, value)...)
		return joinLines(lines), nil
	}
	// Insert after the section's last non-blank line.
	at := end
	for at > start+1 && strings.TrimSpace(lines[at-1]) == "" {
		at--
	}
	repl := append(append([]string{}, lines[:at]...), format(key, value)...)
	lines = append(repl, lines[at:]...)
	return joinLines(lines), nil
}

// Restore puts key back to prev, or deletes it when prev is nil. It removes
// a [global] section left empty, if Set had created it.
func Restore(content, key string, prev *string) string {
	if prev != nil {
		out, _ := Set(content, key, *prev)
		return out
	}
	lines := splitLines(content)
	idx, span, _, ok := find(lines, key)
	if !ok {
		return content
	}
	lines = append(lines[:idx], lines[idx+span:]...)
	// Drop an empty [global] section (header followed only by blank lines).
	if start, end, ok := bounds(lines); ok {
		empty := true
		for _, l := range lines[start+1 : end] {
			if strings.TrimSpace(l) != "" {
				empty = false
				break
			}
		}
		if empty {
			lines = append(lines[:start], lines[end:]...)
			for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
				lines = lines[:len(lines)-1]
			}
		}
	}
	return joinLines(lines)
}

// Package yarnrc edits a user ~/.yarnrc.yml, which Yarn Berry (2+) reads.
//
// It edits the YAML node tree rather than re-marshalling a struct, so keys it
// does not manage, their order and their comments are kept.
package yarnrc

import (
	"bytes"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	keyServer     = "npmRegistryServer"
	keyRegistries = "npmRegistries"
	keyToken      = "npmAuthToken"
	keyAlwaysAuth = "npmAlwaysAuth"
)

// Settings is what cg-dev-auth manages in .yarnrc.yml.
type Settings struct {
	// Registry is the registry URL (a trailing slash is dropped, as Yarn
	// expects).
	Registry string
	// Token is the bearer token; empty leaves any existing token in place.
	Token string
}

// RegistryKey returns the npmRegistries key for registry: //host/path with
// no trailing slash.
func RegistryKey(registry string) string {
	p := strings.TrimSuffix(registry, "/")
	if i := strings.Index(p, "://"); i >= 0 {
		p = p[i+1:]
	}
	return p
}

func serverURL(registry string) string { return strings.TrimSuffix(registry, "/") }

func parse(content []byte) (*yaml.Node, *yaml.Node, error) {
	var doc yaml.Node
	if len(bytes.TrimSpace(content)) > 0 {
		if err := yaml.Unmarshal(content, &doc); err != nil {
			return nil, nil, fmt.Errorf("parse .yarnrc.yml: %w", err)
		}
	}
	if doc.Kind == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 {
		return nil, nil, fmt.Errorf("parse .yarnrc.yml: unexpected document structure")
	}
	root := doc.Content[0]
	if root.Kind == yaml.ScalarNode && root.Tag == "!!null" {
		*root = yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	}
	if root.Kind != yaml.MappingNode {
		return nil, nil, fmt.Errorf(".yarnrc.yml: top level is not a mapping")
	}
	return &doc, root, nil
}

func encode(doc *yaml.Node) ([]byte, error) {
	root := doc.Content[0]
	if len(root.Content) == 0 && root.HeadComment == "" && root.FootComment == "" && doc.HeadComment == "" && doc.FootComment == "" {
		return []byte{}, nil
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// get returns the value node for key in mapping m, or nil.
func get(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// set sets key in mapping m to v, keeping its position when present.
func set(m *yaml.Node, key string, v *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			// Keep comments that were attached to the old value.
			v.HeadComment, v.LineComment, v.FootComment = m.Content[i+1].HeadComment, m.Content[i+1].LineComment, m.Content[i+1].FootComment
			m.Content[i+1] = v
			return
		}
	}
	k := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	if strings.HasPrefix(key, "//") {
		k.Style = yaml.DoubleQuotedStyle // matches Yarn's docs; plain is valid too
	}
	m.Content = append(m.Content, k, v)
}

// del removes key from mapping m and reports whether it was there.
func del(m *yaml.Node, key string) bool {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return true
		}
	}
	return false
}

func str(v string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v, Style: yaml.DoubleQuotedStyle}
}

func boolean(v bool) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: fmt.Sprint(v)}
}

// Apply sets the registry server and the registry's auth. It returns the
// new content and the npmRegistryServer value it replaced (nil when absent).
func Apply(content []byte, s Settings) (out []byte, prevServer *string, err error) {
	doc, root, err := parse(content)
	if err != nil {
		return nil, nil, err
	}
	if v := get(root, keyServer); v != nil {
		p := v.Value
		prevServer = &p
	}
	set(root, keyServer, str(serverURL(s.Registry)))

	regs := get(root, keyRegistries)
	if regs == nil || regs.Kind != yaml.MappingNode {
		regs = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		set(root, keyRegistries, regs)
	}
	key := RegistryKey(s.Registry)
	entry := get(regs, key)
	if entry == nil || entry.Kind != yaml.MappingNode {
		entry = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		set(regs, key, entry)
	}
	set(entry, keyAlwaysAuth, boolean(true))
	if s.Token != "" {
		set(entry, keyToken, str(s.Token))
	}
	out, err = encode(doc)
	return out, prevServer, err
}

// Remove deletes the registry's auth entry and restores npmRegistryServer to
// prevServer (or deletes it when prevServer is nil).
func Remove(content []byte, registry string, prevServer *string) ([]byte, error) {
	doc, root, err := parse(content)
	if err != nil {
		return nil, err
	}
	if regs := get(root, keyRegistries); regs != nil && regs.Kind == yaml.MappingNode {
		del(regs, RegistryKey(registry))
		if len(regs.Content) == 0 {
			del(root, keyRegistries)
		}
	}
	cur := get(root, keyServer)
	if cur != nil && cur.Value == serverURL(registry) {
		if prevServer != nil {
			set(root, keyServer, str(*prevServer))
		} else {
			del(root, keyServer)
		}
	}
	return encode(doc)
}

// Read returns the current npmRegistryServer and the token stored for
// registry.
func Read(content []byte, registry string) (server, token string, err error) {
	_, root, err := parse(content)
	if err != nil {
		return "", "", err
	}
	if v := get(root, keyServer); v != nil {
		server = v.Value
	}
	if regs := get(root, keyRegistries); regs != nil && regs.Kind == yaml.MappingNode {
		if e := get(regs, RegistryKey(registry)); e != nil && e.Kind == yaml.MappingNode {
			if t := get(e, keyToken); t != nil {
				token = t.Value
			}
		}
	}
	return server, token, nil
}

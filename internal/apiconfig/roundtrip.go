package apiconfig

import (
	"bytes"
	"fmt"
	"os"
	"strconv"

	"gopkg.in/yaml.v3"
)

// Round-trip editing (P0-2 DoD: writes preserve comments and key order).
// We operate on the yaml.Node tree — the only way yaml.v3 keeps comments —
// and never re-marshal typed structs.

// Update loads file, hands its document node to mutate, then writes it back
// atomically (temp file + rename).
func Update(path string, mutate func(doc *yaml.Node) error) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	var doc yaml.Node
	if len(bytes.TrimSpace(data)) == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	} else if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if err := mutate(&doc); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return os.Rename(tmp, path)
}

// Set sets the scalar string value at the given mapping-key path, creating
// intermediate mappings (at the end of the parent) when missing. Existing
// keys keep their position, comments and siblings untouched.
//
//	Set(doc, []string{"agent", "interjection", "default_mode"}, "interrupt")
func Set(doc *yaml.Node, keys []string, value string) error {
	if len(keys) == 0 {
		return fmt.Errorf("apiconfig.Set: empty key path")
	}
	m := rootMap(doc)
	node := valueNode(value)
	parent := m
	for i, k := range keys {
		child := mapValue(parent, k)
		isLast := i == len(keys)-1
		if child == nil {
			got := node
			if !isLast {
				got = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			}
			parent.Content = append(parent.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: k, Tag: "!!str"}, got)
			parent = got
			continue
		}
		if isLast {
			setScalar(child, value)
			return nil
		}
		if child.Kind != yaml.MappingNode {
			return fmt.Errorf("key %q is not a mapping", k)
		}
		parent = child
	}
	return nil
}

// SetList writes a string sequence at the key path (browser.allowed_origins
// style values). Comments elsewhere in the document are preserved.
func SetList(doc *yaml.Node, keys []string, values []string) error {
	if len(keys) == 0 {
		return fmt.Errorf("apiconfig.SetList: empty key path")
	}
	m := rootMap(doc)
	parent := m
	for i, k := range keys {
		child := mapValue(parent, k)
		isLast := i == len(keys)-1
		if child == nil {
			got := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			if !isLast {
				got = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			}
			parent.Content = append(parent.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: k, Tag: "!!str"}, got)
			parent = got
			continue
		}
		if isLast {
			seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			for _, v := range values {
				seq.Content = append(seq.Content,
					&yaml.Node{Kind: yaml.ScalarNode, Value: v, Tag: resolveTag(v)})
			}
			*child = *seq
			return nil
		}
		if child.Kind != yaml.MappingNode {
			return fmt.Errorf("key %q is not a mapping", k)
		}
		parent = child
	}
	return nil
}

// GetNode walks the key path and returns the raw node (PATCH uses it to
// decide scalar vs sequence writes).
func GetNode(doc *yaml.Node, keys []string) (*yaml.Node, bool) {
	m := rootMap(doc)
	var child *yaml.Node
	for i, k := range keys {
		child = mapValue(m, k)
		if child == nil {
			return nil, false
		}
		if i < len(keys)-1 {
			if child.Kind != yaml.MappingNode {
				return nil, false
			}
			m = child
		}
	}
	return child, child != nil
}

// Get returns the scalar string at the key path, if present.
func Get(doc *yaml.Node, keys []string) (string, bool) {
	m := rootMap(doc)
	var child *yaml.Node
	for _, k := range keys {
		child = mapValue(m, k)
		if child == nil {
			return "", false
		}
		if child.Kind != yaml.ScalarNode {
			if child.Kind == yaml.MappingNode {
				m = child
				continue
			}
			return "", false
		}
	}
	if child == nil {
		return "", false
	}
	return child.Value, true
}

func rootMap(doc *yaml.Node) *yaml.Node {
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		return doc.Content[0]
	}
	return doc
}

func mapValue(m *yaml.Node, key string) *yaml.Node {
	if m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Kind == yaml.ScalarNode && m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func valueNode(v string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: resolveTag(v), Value: v}
}

func setScalar(n *yaml.Node, v string) {
	n.Kind = yaml.ScalarNode
	n.Tag = resolveTag(v)
	n.Value = v
	n.Style = 0
	n.Content = nil
	n.Alias = nil
}

// resolveTag keeps numbers/bools unquoted on write-back.
func resolveTag(v string) string {
	switch {
	case v == "true" || v == "false":
		return "!!bool"
	case func() bool { _, err := strconv.Atoi(v); return err == nil }():
		return "!!int"
	case func() bool { _, err := strconv.ParseFloat(v, 64); return err == nil }():
		return "!!float"
	case v == "" || v == "null" || v == "~":
		return "!!null"
	default:
		return "!!str"
	}
}

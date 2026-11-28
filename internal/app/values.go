package app

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/fsutil"
	"gopkg.in/yaml.v3"
)

// ApplyOverrides merges values files and --set overrides into a
// deployment's values.yaml. Overrides are persisted to disk rather than
// passed as ephemeral helmfile flags so the deployment directory stays the
// single declarative record — `obol app sync` and stack-up resume replay
// exactly what was last applied.
//
// Files are merged in order, then set expressions on top (later wins).
func ApplyOverrides(cfg *config.Config, deploymentIdentifier string, valuesFiles, sets []string) error {
	if len(valuesFiles) == 0 && len(sets) == 0 {
		return nil
	}

	appName, id, err := parseDeploymentIdentifier(deploymentIdentifier)
	if err != nil {
		return err
	}

	valuesPath := filepath.Join(cfg.ConfigDir, "applications", appName, id, "values.yaml")

	return applyOverridesToFile(valuesPath, valuesFiles, sets)
}

// applyOverridesToFile does the actual load → merge → write on a
// values.yaml path. It works on yaml.v3 Nodes, so comments, key order and
// quoting of the existing file survive (a map round-trip used to sort keys,
// drop comments and unquote hex strings such as addresses).
func applyOverridesToFile(valuesPath string, valuesFiles, sets []string) error {
	doc, err := loadValuesNode(valuesPath)
	if err != nil {
		return err
	}

	if err := applyOverridesToNode(doc, valuesFiles, sets); err != nil {
		return err
	}

	out, err := encodeValuesNode(doc)
	if err != nil {
		return err
	}

	return fsutil.WriteSecret(valuesPath, out)
}

// applyOverridesToNode merges values files (in order) and then --set
// expressions into doc.
func applyOverridesToNode(doc *yaml.Node, valuesFiles, sets []string) error {
	root := doc.Content[0]

	for _, file := range valuesFiles {
		overlay, err := loadValuesNode(file)
		if err != nil {
			return err
		}

		mergeNodes(root, overlay.Content[0])
	}

	for _, expr := range sets {
		if err := applySetExpression(root, expr); err != nil {
			return err
		}
	}

	return nil
}

// YAML core tags used when building nodes.
const (
	tagMap = "!!map"
	tagStr = "!!str"
)

// loadValuesNode reads a YAML file into a document node whose single
// content node is a mapping. An empty or comment-only file yields an empty
// mapping.
func loadValuesNode(path string) (*yaml.Node, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read values file: %w", err)
	}

	return parseValuesNode(data, path)
}

func parseValuesNode(data []byte, name string) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", name, err)
	}

	if doc.Kind == 0 || len(doc.Content) == 0 {
		return &yaml.Node{
			Kind:        yaml.DocumentNode,
			HeadComment: doc.HeadComment,
			Content:     []*yaml.Node{{Kind: yaml.MappingNode, Tag: tagMap}},
		}, nil
	}

	root := doc.Content[0]
	if root.Kind == yaml.ScalarNode && root.ShortTag() == "!!null" {
		doc.Content[0] = &yaml.Node{Kind: yaml.MappingNode, Tag: tagMap, HeadComment: root.HeadComment}
		return &doc, nil
	}

	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("failed to parse %s: top level must be a mapping", name)
	}

	return &doc, nil
}

// encodeValuesNode serializes a values document with 2-space indentation,
// quoting every number-looking string first (see quoteAmbiguousStrings).
func encodeValuesNode(doc *yaml.Node) ([]byte, error) {
	quoteAmbiguousStrings(doc)

	var buf bytes.Buffer

	// No values at all: write only the comments (helm reads an empty file
	// as no values) rather than a bare "{}".
	if len(doc.Content) == 1 && doc.Content[0].Kind == yaml.MappingNode && len(doc.Content[0].Content) == 0 {
		for _, c := range []string{doc.HeadComment, doc.Content[0].HeadComment} {
			for line := range strings.SplitSeq(c, "\n") {
				if line == "" {
					continue
				}

				if !strings.HasPrefix(line, "#") {
					line = "# " + line
				}

				buf.WriteString(line + "\n")
			}
		}

		return buf.Bytes(), nil
	}

	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)

	if err := enc.Encode(doc); err != nil {
		return nil, fmt.Errorf("failed to marshal values: %w", err)
	}

	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("failed to marshal values: %w", err)
	}

	return buf.Bytes(), nil
}

// numberLikeRE matches strings that start like a number. yaml.v3 (YAML 1.2)
// emits some of them bare because it reads them back as strings, e.g. a
// 20-byte address "0x3D1f…" is too large for an int64. Helm parses values
// with YAML 1.1 rules and turns the same bare text into a number (a float
// like 3.49e+47 for that address). Quoting them keeps them strings.
var numberLikeRE = regexp.MustCompile(`^[-+]?\.?[0-9]`)

// quoteAmbiguousStrings double-quotes every plain string scalar under n
// that starts like a number (hex addresses and hashes, version strings,
// "1:20"-style values).
func quoteAmbiguousStrings(n *yaml.Node) {
	if n == nil {
		return
	}

	if n.Kind == yaml.ScalarNode && n.Style == 0 && n.ShortTag() == tagStr && numberLikeRE.MatchString(n.Value) {
		n.Style = yaml.DoubleQuotedStyle
	}

	for _, c := range n.Content {
		quoteAmbiguousStrings(c)
	}
}

// mergeNodes deep-merges the mapping src into the mapping dst: nested
// mappings merge recursively, everything else (scalars, lists) is replaced
// by src.
func mergeNodes(dst, src *yaml.Node) {
	for i := 0; i+1 < len(src.Content); i += 2 {
		key, val := src.Content[i], src.Content[i+1]

		existing := mappingValue(dst, key.Value)
		if existing != nil && existing.Kind == yaml.MappingNode && val.Kind == yaml.MappingNode {
			mergeNodes(existing, val)
			continue
		}

		setMappingValue(dst, key, val)
	}
}

// mappingValue returns the value node for key in mapping m, or nil.
func mappingValue(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}

	return nil
}

// setMappingValue replaces (keeping the existing key node and its
// comments) or appends key → val in mapping m.
func setMappingValue(m *yaml.Node, key, val *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key.Value {
			// Keep a line comment that sat on the replaced scalar.
			if val.LineComment == "" && m.Content[i+1].Kind == yaml.ScalarNode && val.Kind == yaml.ScalarNode {
				val.LineComment = m.Content[i+1].LineComment
			}

			m.Content[i+1] = val

			return
		}
	}

	m.Content = append(m.Content, key, val)
}

// applySetExpression applies a single "path.to.key=value" override onto
// the mapping root, creating intermediate mappings as needed. The value is
// parsed as a YAML scalar so `true`, `3`, and quoted strings get their
// natural types; anything starting with 0x stays a string (addresses,
// hashes). Existing non-map intermediates are overwritten with maps,
// matching helm --set semantics.
func applySetExpression(root *yaml.Node, expr string) error {
	key, rawValue, ok := strings.Cut(expr, "=")
	if !ok || key == "" {
		return fmt.Errorf("invalid --set expression %q: expected key.path=value", expr)
	}

	value, err := setValueNode(rawValue)
	if err != nil {
		return fmt.Errorf("invalid --set value in %q: %w", expr, err)
	}

	segments := strings.Split(key, ".")
	current := root

	for _, segment := range segments[:len(segments)-1] {
		if segment == "" {
			return fmt.Errorf("invalid --set expression %q: empty key segment", expr)
		}

		next := mappingValue(current, segment)
		if next == nil || next.Kind != yaml.MappingNode {
			next = &yaml.Node{Kind: yaml.MappingNode, Tag: tagMap}
			setMappingValue(current, scalarKey(segment), next)
		}

		current = next
	}

	last := segments[len(segments)-1]
	if last == "" {
		return fmt.Errorf("invalid --set expression %q: empty key segment", expr)
	}

	setMappingValue(current, scalarKey(last), value)

	return nil
}

func scalarKey(s string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: tagStr, Value: s}
}

// setValueNode parses the right-hand side of a --set expression.
func setValueNode(raw string) (*yaml.Node, error) {
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(trimmed, "0x") || strings.HasPrefix(trimmed, "0X") {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: tagStr, Value: trimmed, Style: yaml.DoubleQuotedStyle}, nil
	}

	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
		return nil, err
	}

	if doc.Kind == 0 || len(doc.Content) == 0 {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}, nil
	}

	return doc.Content[0], nil
}

// loadValuesMap reads a YAML file into a string-keyed map. A file that is
// empty or contains only comments yields an empty map.
func loadValuesMap(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read values file: %w", err)
	}

	var values map[string]any
	if err := yaml.Unmarshal(data, &values); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", path, err)
	}

	if values == nil {
		values = map[string]any{}
	}

	return values, nil
}

// bareHexPaths lists key paths in a values file whose value is a plain
// (unquoted) hex scalar too long for an integer (addresses, hashes). Helm
// reads such a value as a float unless it is quoted.
func bareHexPaths(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}

	var out []string

	var walk func(n *yaml.Node, prefix string)
	walk = func(n *yaml.Node, prefix string) {
		switch n.Kind {
		case yaml.DocumentNode:
			for _, c := range n.Content {
				walk(c, prefix)
			}
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Content); i += 2 {
				walk(n.Content[i+1], joinPath(prefix, n.Content[i].Value))
			}
		case yaml.SequenceNode:
			for i, c := range n.Content {
				walk(c, fmt.Sprintf("%s[%d]", prefix, i))
			}
		case yaml.AliasNode:
			// Aliases repeat an anchored value reported where it is defined.
		case yaml.ScalarNode:
			v := strings.ToLower(n.Value)
			// Longer than 16 hex digits: too big for an integer, so helm
			// turns it into a lossy float.
			if n.Style == 0 && strings.HasPrefix(v, "0x") && len(v) > 18 {
				out = append(out, prefix)
			}
		}
	}
	walk(&doc, "")

	return out, nil
}

func joinPath(prefix, key string) string {
	if prefix == "" {
		return key
	}

	return prefix + "." + key
}

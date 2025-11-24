package network

import (
	"fmt"
	"regexp"
	"strings"
	"text/template"
	"text/template/parse"

	"github.com/ObolNetwork/obol-stack/internal/embed"
)

// EnvVar represents an environment variable with its default value
type EnvVar struct {
	Name         string
	DefaultValue string
	FlagName     string   // CLI flag name derived from env var name
	Description  string   // Human-readable description from @description
	EnumValues   []string // Valid enum values from @enum
	Required     bool     // Whether this env var is required (no default value)
}

// extractTemplateFields parses a Go template and extracts all field references
// Returns a map of field names to their line numbers for annotation matching
func extractTemplateFields(content string) (map[string]int, error) {
	// Parse the template
	tmpl, err := template.New("helmfile").Parse(content)
	if err != nil {
		return nil, fmt.Errorf("failed to parse template: %w", err)
	}

	fields := make(map[string]int)
	lines := strings.Split(content, "\n")

	// Walk the template AST to find field references
	var walkNodes func(node parse.Node)
	walkNodes = func(node parse.Node) {
		if node == nil {
			return
		}

		switch n := node.(type) {
		case *parse.ListNode:
			if n != nil {
				for _, child := range n.Nodes {
					walkNodes(child)
				}
			}
		case *parse.ActionNode:
			if n.Pipe != nil {
				for _, cmd := range n.Pipe.Cmds {
					for _, arg := range cmd.Args {
						if field, ok := arg.(*parse.FieldNode); ok {
							// Extract field name (e.g., .Network -> Network)
							if len(field.Ident) > 0 {
								fieldName := field.Ident[0]
								// Find line number of this field in the content
								for i, line := range lines {
									if strings.Contains(line, "{{."+fieldName+"}}") {
										fields[fieldName] = i
										break
									}
								}
							}
						}
					}
				}
			}
		case *parse.IfNode:
			walkNodes(n.List)
			walkNodes(n.ElseList)
		case *parse.RangeNode:
			walkNodes(n.List)
			walkNodes(n.ElseList)
		case *parse.WithNode:
			walkNodes(n.List)
			walkNodes(n.ElseList)
		case *parse.TemplateNode:
			walkNodes(n.Pipe)
		}
	}

	// Walk the template tree
	if tmpl.Tree != nil && tmpl.Tree.Root != nil {
		walkNodes(tmpl.Tree.Root)
	}

	return fields, nil
}

// parseAnnotationsFromLines extracts annotations from comment lines preceding a field
// Returns enum values, default value, description, and whether a default was specified
func parseAnnotationsFromLines(lines []string, fieldLineNum int) ([]string, string, string, bool) {
	var enumValues []string
	var defaultValue string
	var description string
	var hasDefault bool

	// Look backwards from the field line to find annotations
	for i := fieldLineNum - 1; i >= 0; i-- {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		// Stop if we hit a non-comment line or empty line
		if !strings.HasPrefix(trimmed, "#") {
			break
		}

		// Parse @enum annotation
		if enumMatch := regexp.MustCompile(`#\s*@enum\s+(.+)`).FindStringSubmatch(line); enumMatch != nil {
			enumStr := strings.TrimSpace(enumMatch[1])
			enumValues = strings.Split(enumStr, ",")
			for j := range enumValues {
				enumValues[j] = strings.TrimSpace(enumValues[j])
			}
		}

		// Parse @default annotation (value is optional, may be empty string)
		if defaultMatch := regexp.MustCompile(`#\s*@default\s*(.*)`).FindStringSubmatch(line); defaultMatch != nil {
			defaultValue = strings.TrimSpace(defaultMatch[1])
			hasDefault = true
		}

		// Parse @description annotation
		if descMatch := regexp.MustCompile(`#\s*@description\s+(.+)`).FindStringSubmatch(line); descMatch != nil {
			description = strings.TrimSpace(descMatch[1])
		}
	}

	return enumValues, defaultValue, description, hasDefault
}

// ParseEmbeddedNetworkEnvVars extracts template fields from an embedded network values file
// Now uses Go template parsing instead of regex-based env var detection
func ParseEmbeddedNetworkEnvVars(networkName string) ([]EnvVar, error) {
	// Read the embedded values template
	content, err := embed.ReadEmbeddedNetworkFile(networkName, "values.yaml.gotmpl")
	if err != nil {
		return nil, fmt.Errorf("failed to read embedded values: %w", err)
	}

	// Extract template fields using Go template parser
	fields, err := extractTemplateFields(string(content))
	if err != nil {
		return nil, fmt.Errorf("failed to extract template fields: %w", err)
	}

	lines := strings.Split(string(content), "\n")
	var envVars []EnvVar

	// For each field, extract annotations and create EnvVar
	for fieldName, lineNum := range fields {
		enumValues, defaultValue, description, hasDefault := parseAnnotationsFromLines(lines, lineNum)

		// Convert field name to CLI flag name (e.g., ExecutionClient -> execution-client)
		flagName := fieldNameToFlagName(fieldName)

		// Determine if field is required (no @default annotation)
		required := !hasDefault

		envVar := EnvVar{
			Name:         fieldName,
			DefaultValue: defaultValue,
			FlagName:     flagName,
			Description:  description,
			EnumValues:   enumValues,
			Required:     required,
		}
		envVars = append(envVars, envVar)
	}

	return envVars, nil
}

// fieldNameToFlagName converts a template field name to a CLI flag name
// Example: ExecutionClient -> execution-client
// Example: Network -> network
func fieldNameToFlagName(fieldName string) string {
	// Insert hyphen before uppercase letters (except first)
	var result strings.Builder
	for i, r := range fieldName {
		if i > 0 && r >= 'A' && r <= 'Z' {
			result.WriteRune('-')
		}
		result.WriteRune(r)
	}

	// Convert to lowercase
	return strings.ToLower(result.String())
}

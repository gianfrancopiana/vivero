package vivero

import (
	"fmt"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
)

const configDocsURL = "https://github.com/gianfrancopiana/vivero#configuration"

func configSchemaFindings(node *yaml.Node) []ConfigDoctorFinding {
	var findings []ConfigDoctorFinding
	walkConfigSchema(documentContent(node), reflect.TypeOf(ProjectConfig{}), "", &findings)
	return findings
}

func firstUnsupportedConfigFinding(node *yaml.Node) (ConfigDoctorFinding, bool) {
	for _, finding := range configSchemaFindings(node) {
		if finding.Code == "unsupported-config-key" {
			return finding, true
		}
	}
	return ConfigDoctorFinding{}, false
}

func walkConfigSchema(node *yaml.Node, typ reflect.Type, path string, findings *[]ConfigDoctorFinding) {
	if node == nil || typ == nil {
		return
	}
	typ = unwrapConfigType(typ)
	switch typ.Kind() {
	case reflect.Struct:
		walkConfigStruct(node, typ, path, findings)
	case reflect.Map:
		walkConfigMap(node, typ, path, findings)
	case reflect.Slice, reflect.Array:
		walkConfigSlice(node, typ, path, findings)
	}
}

func walkConfigStruct(node *yaml.Node, typ reflect.Type, path string, findings *[]ConfigDoctorFinding) {
	if node.Kind != yaml.MappingNode {
		return
	}
	fields := yamlFieldTypes(typ)
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i]
		value := node.Content[i+1]
		name := strings.TrimSpace(key.Value)
		childPath := joinConfigPath(path, name)
		if finding, ok := unsupportedConfigKeyFinding(key, childPath); ok {
			*findings = append(*findings, finding)
			continue
		}
		fieldType, ok := fields[name]
		if !ok {
			*findings = append(*findings, unknownConfigKeyFinding(key, childPath, fields))
			continue
		}
		walkConfigSchema(value, fieldType, childPath, findings)
	}
}

func walkConfigMap(node *yaml.Node, typ reflect.Type, path string, findings *[]ConfigDoctorFinding) {
	if node.Kind != yaml.MappingNode {
		return
	}
	elem := typ.Elem()
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i]
		value := node.Content[i+1]
		walkConfigSchema(value, elem, joinConfigPath(path, key.Value), findings)
	}
}

func walkConfigSlice(node *yaml.Node, typ reflect.Type, path string, findings *[]ConfigDoctorFinding) {
	if node.Kind != yaml.SequenceNode {
		return
	}
	elem := typ.Elem()
	for i, value := range node.Content {
		walkConfigSchema(value, elem, fmt.Sprintf("%s[%d]", path, i), findings)
	}
}

func documentContent(node *yaml.Node) *yaml.Node {
	if node == nil {
		return nil
	}
	if node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
		return node.Content[0]
	}
	return node
}

func unwrapConfigType(typ reflect.Type) reflect.Type {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	return typ
}

func yamlFieldTypes(typ reflect.Type) map[string]reflect.Type {
	out := map[string]reflect.Type{}
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if field.PkgPath != "" {
			continue
		}
		name := yamlFieldName(field)
		if name == "" || name == "-" {
			continue
		}
		out[name] = field.Type
	}
	return out
}

func yamlFieldName(field reflect.StructField) string {
	tag := field.Tag.Get("yaml")
	if tag == "" {
		return field.Name
	}
	name, _, _ := strings.Cut(tag, ",")
	return name
}

func unsupportedConfigKeyFinding(key *yaml.Node, path string) (ConfigDoctorFinding, bool) {
	if key.Value != "dockerfileInline" {
		return ConfigDoctorFinding{}, false
	}
	return ConfigDoctorFinding{
		Severity:   "error",
		Code:       "unsupported-config-key",
		Path:       path,
		Line:       key.Line,
		Column:     key.Column,
		Message:    "unsupported dockerfileInline; keep Dockerfiles in the app repo instead of embedding them in vivero.yml",
		Suggestion: "Move the Dockerfile into the app repository and reference it with services.<name>.build.dockerfile.",
		Docs:       configDocsURL,
	}, true
}

func unknownConfigKeyFinding(key *yaml.Node, path string, fields map[string]reflect.Type) ConfigDoctorFinding {
	finding := ConfigDoctorFinding{
		Severity: "warning",
		Code:     "unknown-config-key",
		Path:     path,
		Line:     key.Line,
		Column:   key.Column,
		Message:  fmt.Sprintf("unknown config key %s is not part of the current Vivero schema and will be ignored", path),
		Docs:     configDocsURL,
	}
	if suggestion := nearestConfigKey(key.Value, fields); suggestion != "" {
		finding.Suggestion = fmt.Sprintf("Did you mean %s?", suggestion)
	} else {
		finding.Suggestion = "Remove this key, or move it under a supported section before relying on it."
	}
	return finding
}

func nearestConfigKey(name string, fields map[string]reflect.Type) string {
	best := ""
	bestDistance := 1 << 30
	for candidate := range fields {
		distance := editDistance(strings.ToLower(name), strings.ToLower(candidate))
		if distance < bestDistance {
			bestDistance = distance
			best = candidate
		}
	}
	if best == "" {
		return ""
	}
	limit := maxInt(3, len(name)/2)
	if bestDistance > limit {
		return ""
	}
	return best
}

func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 0
			if a[i-1] != b[j-1] {
				cost = 1
			}
			cur[j] = minConfigInt(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

func minConfigInt(values ...int) int {
	best := values[0]
	for _, value := range values[1:] {
		if value < best {
			best = value
		}
	}
	return best
}

func joinConfigPath(parent, child string) string {
	if parent == "" {
		return child
	}
	return parent + "." + child
}

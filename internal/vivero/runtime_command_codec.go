package vivero

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

func normalizeRuntimeCommandYAMLNodes(node *yaml.Node) error {
	return normalizeRuntimeCommandYAMLNodesAt(node, "")
}

func normalizeRuntimeCommandYAMLNodesAt(node *yaml.Node, path string) error {
	if node == nil {
		return nil
	}
	switch node.Kind {
	case yaml.DocumentNode:
		for _, child := range node.Content {
			if err := normalizeRuntimeCommandYAMLNodesAt(child, path); err != nil {
				return err
			}
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			key := node.Content[i]
			value := node.Content[i+1]
			if key == nil {
				continue
			}
			childPath := joinConfigPath(path, key.Value)
			if runtimeCommandConfigPath(childPath) {
				normalized, err := normalizeRuntimeCommandYAMLNode(value)
				if err != nil {
					return err
				}
				if value != nil && normalized != nil {
					*value = *normalized
				}
				continue
			}
			if err := normalizeRuntimeCommandYAMLNodesAt(value, childPath); err != nil {
				return err
			}
		}
	case yaml.SequenceNode:
		for i, child := range node.Content {
			childPath := fmt.Sprintf("%s[%d]", path, i)
			if err := normalizeRuntimeCommandYAMLNodesAt(child, childPath); err != nil {
				return err
			}
		}
	}
	return nil
}

func runtimeCommandConfigPath(path string) bool {
	parts := strings.Split(path, ".")
	if len(parts) == 3 && parts[0] == "services" && parts[2] == "command" {
		return true
	}
	if len(parts) == 4 && parts[0] == "services" && parts[2] == "health" && parts[3] == "command" {
		return true
	}
	if len(parts) == 3 && parts[0] == "backingServices" && parts[2] == "command" {
		return true
	}
	if len(parts) == 4 && parts[0] == "backingServices" && parts[2] == "health" && parts[3] == "command" {
		return true
	}
	if strings.HasPrefix(path, "setup.afterSeeds[") && strings.HasSuffix(path, ".command") {
		return true
	}
	if strings.HasPrefix(path, "setup.everyBoot[") && strings.HasSuffix(path, ".command") {
		return true
	}
	if len(parts) == 4 && parts[0] == "agent" && parts[1] == "iteration" && (parts[2] == "restartCommand" || parts[2] == "dependencyChangedCommand") && parts[3] == "command" {
		return true
	}
	return false
}

func normalizeRuntimeCommandYAMLNode(node *yaml.Node) (*yaml.Node, error) {
	if node == nil || node.Kind == 0 || node.Tag == "!!null" {
		return node, nil
	}
	switch node.Kind {
	case yaml.ScalarNode:
		var shell string
		if err := node.Decode(&shell); err != nil {
			return nil, err
		}
		return runtimeCommandMappingNode(node, "shell", yamlStringNode(shell, node.Line, node.Column)), nil
	case yaml.SequenceNode:
		args := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Line: node.Line, Column: node.Column}
		for i, child := range node.Content {
			if child == nil || child.Kind != yaml.ScalarNode {
				return nil, fmt.Errorf("command arg %d at line %d must be a scalar string", i, yamlNodeLine(child))
			}
			var arg string
			if err := child.Decode(&arg); err != nil {
				return nil, err
			}
			args.Content = append(args.Content, yamlStringNode(arg, child.Line, child.Column))
		}
		return runtimeCommandMappingNode(node, "args", args), nil
	case yaml.MappingNode:
		return node, nil
	default:
		return nil, fmt.Errorf("command at line %d must be a string shell command or a string array exec command", yamlNodeLine(node))
	}
}

func runtimeCommandMappingNode(source *yaml.Node, field string, value *yaml.Node) *yaml.Node {
	return &yaml.Node{
		Kind:   yaml.MappingNode,
		Tag:    "!!map",
		Line:   yamlNodeLine(source),
		Column: yamlNodeColumn(source),
		Content: []*yaml.Node{
			yamlStringNode(field, yamlNodeLine(source), yamlNodeColumn(source)),
			value,
		},
	}
}

func yamlStringNode(value string, line, column int) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value, Line: line, Column: column}
}

func yamlNodeLine(node *yaml.Node) int {
	if node == nil {
		return 0
	}
	return node.Line
}

func yamlNodeColumn(node *yaml.Node) int {
	if node == nil {
		return 0
	}
	return node.Column
}

package schema

import (
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// RuntimeCommand represents an app-owned runtime command.
//
// YAML/JSON scalar form is a shell command and is executed as `/bin/sh -lc <command>`.
// YAML/JSON sequence form is exec/argv form and is passed directly to the runtime.
type RuntimeCommand struct {
	Shell string   `json:"-"`
	Args  []string `json:"-"`
}

func (c RuntimeCommand) IsZero() bool {
	return strings.TrimSpace(c.Shell) == "" && len(c.Args) == 0
}

func (c RuntimeCommand) Display() string {
	if len(c.Args) > 0 {
		return strings.Join(c.Args, " ")
	}
	return c.Shell
}

func (c RuntimeCommand) Key() string {
	if len(c.Args) > 0 {
		return "exec\x00" + strings.Join(c.Args, "\x00")
	}
	return "shell\x00" + c.Shell
}

func (c RuntimeCommand) RuntimeArgs() []string {
	if len(c.Args) > 0 {
		return append([]string(nil), c.Args...)
	}
	if strings.TrimSpace(c.Shell) == "" {
		return nil
	}
	return []string{"/bin/sh", "-lc", c.Shell}
}

func (c RuntimeCommand) MarshalYAML() (any, error) {
	if len(c.Args) > 0 {
		return c.Args, nil
	}
	if c.Shell == "" {
		return nil, nil
	}
	return c.Shell, nil
}

func (c *RuntimeCommand) UnmarshalYAML(node *yaml.Node) error {
	parsed, err := runtimeCommandFromYAML(node)
	if err != nil {
		return err
	}
	*c = parsed
	return nil
}

func runtimeCommandFromYAML(node *yaml.Node) (RuntimeCommand, error) {
	if node == nil || node.Kind == 0 || node.Tag == "!!null" {
		return RuntimeCommand{}, nil
	}
	switch node.Kind {
	case yaml.ScalarNode:
		var s string
		if err := node.Decode(&s); err != nil {
			return RuntimeCommand{}, err
		}
		return RuntimeCommand{Shell: s}, nil
	case yaml.SequenceNode:
		args := make([]string, 0, len(node.Content))
		for i, child := range node.Content {
			if child.Kind != yaml.ScalarNode {
				return RuntimeCommand{}, fmt.Errorf("command arg %d must be a scalar string", i)
			}
			var arg string
			if err := child.Decode(&arg); err != nil {
				return RuntimeCommand{}, err
			}
			args = append(args, arg)
		}
		return RuntimeCommand{Args: args}, nil
	default:
		return RuntimeCommand{}, fmt.Errorf("command must be a string shell command or a string array exec command")
	}
}

func (c RuntimeCommand) MarshalJSON() ([]byte, error) {
	if len(c.Args) > 0 {
		return json.Marshal(c.Args)
	}
	return json.Marshal(c.Shell)
}

func (c *RuntimeCommand) UnmarshalJSON(data []byte) error {
	var shell string
	if err := json.Unmarshal(data, &shell); err == nil {
		*c = RuntimeCommand{Shell: shell}
		return nil
	}
	var args []string
	if err := json.Unmarshal(data, &args); err == nil {
		*c = RuntimeCommand{Args: args}
		return nil
	}
	return fmt.Errorf("command must be a string shell command or a string array exec command")
}

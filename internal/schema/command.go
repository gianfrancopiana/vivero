package schema

// RuntimeCommand represents an app-owned runtime command.
//
// Shell is executed by runtime adapters as `/bin/sh -lc <shell>`.
// Args is exec/argv form and is passed directly to the runtime.
//
// Project YAML accepts either `command: <shell string>` or
// `command: [<argv>, ...]`; internal/vivero normalizes those forms into this
// data-only schema type before decoding.
type RuntimeCommand struct {
	Shell string   `yaml:"shell,omitempty" json:"shell,omitempty"`
	Args  []string `yaml:"args,omitempty" json:"args,omitempty"`
}

func (c RuntimeCommand) IsZero() bool {
	return trimRuntimeCommandSpace(c.Shell) == "" && len(c.Args) == 0
}

func (c RuntimeCommand) Display() string {
	if len(c.Args) > 0 {
		return joinRuntimeCommand(c.Args, " ")
	}
	return c.Shell
}

func (c RuntimeCommand) Key() string {
	if len(c.Args) > 0 {
		return "exec\x00" + joinRuntimeCommand(c.Args, "\x00")
	}
	return "shell\x00" + c.Shell
}

func (c RuntimeCommand) RuntimeArgs() []string {
	if len(c.Args) > 0 {
		return append([]string(nil), c.Args...)
	}
	if trimRuntimeCommandSpace(c.Shell) == "" {
		return nil
	}
	return []string{"/bin/sh", "-lc", c.Shell}
}

func joinRuntimeCommand(parts []string, sep string) string {
	if len(parts) == 0 {
		return ""
	}
	out := parts[0]
	for _, part := range parts[1:] {
		out += sep + part
	}
	return out
}

func trimRuntimeCommandSpace(s string) string {
	start := 0
	for start < len(s) && runtimeCommandSpace(s[start]) {
		start++
	}
	end := len(s)
	for end > start && runtimeCommandSpace(s[end-1]) {
		end--
	}
	return s[start:end]
}

func runtimeCommandSpace(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r', '\v', '\f':
		return true
	default:
		return false
	}
}

package vivero

import (
	"errors"
	"fmt"
	"strings"
)

type cliError struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Hint    string         `json:"hint,omitempty"`
	Details map[string]any `json:"details,omitempty"`
	Cause   error          `json:"-"`
}

func (e cliError) Error() string {
	if e.Cause != nil {
		return e.Message + ": " + e.Cause.Error()
	}
	return e.Message
}

func newCLIError(code, message, hint string, details any) error {
	return cliError{Code: code, Message: message, Hint: hint, Details: normalizeDetails(details)}
}

func normalizeDetails(details any) map[string]any {
	out := map[string]any{}
	switch v := details.(type) {
	case nil:
		return out
	case map[string]any:
		for k, value := range v {
			out[k] = value
		}
	case map[string]string:
		for k, value := range v {
			out[k] = value
		}
	default:
		out["value"] = v
	}
	return out
}

func asCLIError(err error) (cliError, bool) {
	var ce cliError
	if errors.As(err, &ce) {
		return ce, true
	}
	return cliError{}, false
}

func missingRequiredError(command, required, example string) error {
	return newCLIError("missing_required_argument", fmt.Sprintf("%s requires %s", command, required), "Run: "+example, map[string]string{"command": command, "required": required})
}

func unknownCommandError(name string) error {
	hint := "Run: vivero commands --json --no-input"
	if suggestion := suggestCommand(name); suggestion != "" {
		hint = "Did you mean `vivero " + suggestion + "`?"
	}
	return newCLIError("unknown_command", "unknown command: "+name, hint, map[string]string{"command": name})
}

func suggestCommand(name string) string {
	best := ""
	bestDistance := 1000
	for _, cmd := range commandCatalog() {
		candidate := cmd.Path[0]
		d := levenshtein(name, candidate)
		if d < bestDistance {
			bestDistance = d
			best = candidate
		}
	}
	if bestDistance <= 3 {
		return best
	}
	return ""
}

func levenshtein(a, b string) int {
	a = strings.ToLower(a)
	b = strings.ToLower(b)
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
			cur[j] = minEditDistance(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

func minEditDistance(values ...int) int {
	m := values[0]
	for _, v := range values[1:] {
		if v < m {
			m = v
		}
	}
	return m
}

func requireExplicitConfirmation(noInput, dangerous bool, confirm, expected string) error {
	if !dangerous {
		return nil
	}
	if confirm == expected {
		return nil
	}
	if noInput {
		return fmt.Errorf("dangerous operation requires --confirm %s under --no-input", expected)
	}
	return fmt.Errorf("dangerous operation requires confirmation %q", expected)
}

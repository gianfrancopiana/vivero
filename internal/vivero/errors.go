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
	Docs    string         `json:"docs,omitempty"`
	Details map[string]any `json:"details,omitempty"`
	Cause   error          `json:"-"`
}

type cliErrorResponse struct {
	OK    bool     `json:"ok"`
	Error cliError `json:"error"`
}

func (e cliError) Error() string {
	if e.Cause != nil {
		return e.Message + ": " + e.Cause.Error()
	}
	return e.Message
}

func newCLIError(code, message, hint string, details any) error {
	normalized := normalizeDetails(details)
	return cliError{Code: code, Message: message, Hint: hint, Docs: cliErrorDocs(code, normalized), Details: normalized}
}

func cliErrorPayload(err error) cliErrorResponse {
	ce, ok := asCLIError(err)
	if !ok {
		ce = cliError{Code: "error", Message: err.Error()}
	}
	return cliErrorResponse{OK: false, Error: ce}
}

func cliErrorDocs(code string, details map[string]any) string {
	command := stringDetail(details, "command")
	if code == "unknown_command" {
		if suggestion := stringDetail(details, "suggestion"); suggestion != "" {
			return "vivero help " + suggestion
		}
		return "vivero commands --json --no-input"
	}
	if command != "" {
		return "vivero help " + command
	}
	return ""
}

func stringDetail(details map[string]any, key string) string {
	value, ok := details[key]
	if !ok {
		return ""
	}
	s, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
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

func missingArgError(command, required string) error {
	return missingRequiredError(command, required, "vivero help "+command)
}

func unknownCommandError(name string) error {
	name = strings.TrimSpace(name)
	hint := "Run: vivero commands --json --no-input"
	details := map[string]string{"command": name}
	if suggestion := suggestCommand(name); suggestion != "" {
		hint = "Did you mean `vivero " + suggestion + "`?"
		details["suggestion"] = suggestion
	}
	return newCLIError("unknown_command", "unknown command: "+name, hint, details)
}

func unknownSubcommandError(group, action string) error {
	return unknownCommandError(strings.TrimSpace(group + " " + action))
}

func suggestCommand(name string) string {
	name = strings.TrimSpace(name)
	best := ""
	bestDistance := 1000
	seen := map[string]bool{}
	for _, cmd := range commandCatalog() {
		candidates := []string{cmd.Name()}
		if len(cmd.Path) > 0 {
			candidates = append(candidates, cmd.Path[0])
		}
		for _, candidate := range candidates {
			if candidate == "" || seen[candidate] {
				continue
			}
			seen[candidate] = true
			d := levenshtein(name, candidate)
			if d < bestDistance {
				bestDistance = d
				best = candidate
			}
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

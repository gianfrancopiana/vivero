package nameid

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// Docker returns a Docker-compatible, lower-case name component.
func Docker(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '.' || r == '-'
		if !ok {
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
			continue
		}
		b.WriteRune(r)
		lastDash = r == '-'
	}
	out := strings.Trim(b.String(), "-._")
	if out == "" {
		return "vivero"
	}
	return out
}

// ShortStable returns a short deterministic identifier for names that need a suffix.
func ShortStable(input string) string {
	h := sha256.Sum256([]byte(input))
	id := hex.EncodeToString(h[:])
	return id[:8]
}

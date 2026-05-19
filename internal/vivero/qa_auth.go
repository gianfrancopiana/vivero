package vivero

import (
	"fmt"
	"os"
	"strings"
)

type resolvedQAAuthSession struct {
	Name                   string
	ConfiguredStorageState string
	StorageState           string
	Exists                 bool
	Scopes                 []string
	Note                   string
}

func qaAuthPlan(projectPath string, cfg QAAuthConfig) (map[string]any, map[string]resolvedQAAuthSession, error) {
	resolved := map[string]resolvedQAAuthSession{}
	sessions := map[string]any{}
	for _, name := range sortedMapKeys(cfg.Sessions) {
		session := cfg.Sessions[name]
		trimmedName := strings.TrimSpace(name)
		if trimmedName == "" {
			return nil, nil, fmt.Errorf("qa auth session name must not be empty")
		}
		entry := resolvedQAAuthSession{
			Name:                   trimmedName,
			ConfiguredStorageState: strings.TrimSpace(session.StorageState),
			Scopes:                 normalizeStringList(session.Scopes),
			Note:                   strings.TrimSpace(session.Note),
		}
		if entry.ConfiguredStorageState != "" {
			storageState, err := resolveProjectPath(projectPath, entry.ConfiguredStorageState)
			if err != nil {
				return nil, nil, fmt.Errorf("agent.qa.auth.sessions.%s.storageState: %w", trimmedName, err)
			}
			entry.StorageState = storageState
			if _, err := os.Stat(storageState); err == nil {
				entry.Exists = true
			} else if !os.IsNotExist(err) {
				return nil, nil, fmt.Errorf("agent.qa.auth.sessions.%s.storageState: %w", trimmedName, err)
			}
		}
		resolved[trimmedName] = entry
		sessions[trimmedName] = qaAuthSessionMap(entry)
	}
	return map[string]any{"sessions": sessions}, resolved, nil
}

func qaAuthSessionMap(session resolvedQAAuthSession) map[string]any {
	out := map[string]any{
		"storageState":           session.StorageState,
		"configuredStorageState": session.ConfiguredStorageState,
		"exists":                 session.Exists,
		"scopes":                 session.Scopes,
	}
	if session.Note != "" {
		out["note"] = session.Note
	}
	return out
}

func qaAuthSessionForScope(scope QAScope, sessions map[string]resolvedQAAuthSession) (resolvedQAAuthSession, bool) {
	if len(sessions) == 0 {
		return resolvedQAAuthSession{}, false
	}
	if explicit := strings.TrimSpace(scope.AuthSession); explicit != "" {
		session, ok := sessions[explicit]
		return session, ok
	}
	scopeName := qaScopeName(scope)
	for _, name := range sortedMapKeys(sessions) {
		session := sessions[name]
		if qaAuthSessionAppliesToScope(session, scopeName) {
			return session, true
		}
	}
	return resolvedQAAuthSession{}, false
}

func qaAuthSessionAppliesToScope(session resolvedQAAuthSession, scopeName string) bool {
	for _, configured := range session.Scopes {
		switch configured {
		case "*", "all":
			return true
		case scopeName:
			return true
		}
	}
	return false
}

func normalizeStringList(values []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

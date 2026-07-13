package vivero

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

type previewConfigHashInput struct {
	Config  ProjectConfig     `json:"config"`
	Secrets map[string]string `json:"secrets,omitempty"`
}

// previewConfigHash fingerprints all effective profiled runtime configuration
// and current app-service secrets. Only the digest is persisted.
func (a *App) previewConfigHash(cfg ProjectConfig) (string, error) {
	secrets, err := readEnvFile(a.secretFile(cfg.Project.Name))
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(previewConfigHashInput{Config: cfg, Secrets: secrets})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:]), nil
}

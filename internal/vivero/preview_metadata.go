package vivero

import "strings"

func ensureCanonicalPreviewMetadata(req *UpRequest, cfg ProjectConfig) {
	if req == nil {
		return
	}
	if canonicalBranchFromMetadata(req.Metadata) != "" {
		return
	}
	branch := canonicalBranchFromSourceOverrides(req.Sources, cfg.Sources)
	if branch == "" {
		branch = canonicalBranchFromSourceDefaults(cfg.Sources)
	}
	if branch == "" {
		return
	}
	if req.Metadata == nil {
		req.Metadata = map[string]string{}
	}
	req.Metadata["branch"] = branch
}

func canonicalBranchFromMetadata(metadata map[string]string) string {
	for _, key := range []string{"branch", "ref", "git.branch", "git.ref"} {
		if metadata == nil {
			continue
		}
		if ref := strings.TrimSpace(metadata[key]); ref != "" {
			return ref
		}
	}
	return ""
}

func canonicalBranchFromSourceOverrides(overrides map[string]string, sources map[string]SourceConfig) string {
	for _, source := range sortedMapKeys(sources) {
		if ref := strings.TrimSpace(overrides[source+".ref"]); ref != "" {
			return ref
		}
	}
	for _, key := range sortedMapKeys(overrides) {
		if strings.HasSuffix(key, ".ref") {
			if ref := strings.TrimSpace(overrides[key]); ref != "" {
				return ref
			}
		}
	}
	return ""
}

func canonicalBranchFromSourceDefaults(sources map[string]SourceConfig) string {
	for _, source := range sortedMapKeys(sources) {
		if ref := strings.TrimSpace(sources[source].DefaultRef); ref != "" {
			return ref
		}
	}
	return ""
}

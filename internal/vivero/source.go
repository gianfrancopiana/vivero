package vivero

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (a *App) resolveSource(project, projectPath, previewID, name string, src SourceConfig, overrides map[string]string) (PreviewSource, error) {
	if overrides == nil {
		overrides = map[string]string{}
	}
	if p, ok := overrides[name+".path"]; ok {
		abs, err := filepath.Abs(expandPath(p))
		if err != nil {
			return PreviewSource{}, err
		}
		if st, err := os.Stat(abs); err != nil || !st.IsDir() {
			return PreviewSource{}, fmt.Errorf("external source path is not a directory: %s", abs)
		}
		return PreviewSource{Name: name, Mode: "external", Path: abs, Owned: false}, nil
	}
	if src.Path != "" {
		abs, err := resolveSourcePath(projectPath, src.Path)
		if err != nil {
			return PreviewSource{}, err
		}
		if st, err := os.Stat(abs); err != nil || !st.IsDir() {
			return PreviewSource{}, fmt.Errorf("configured source path is not a directory: %s", abs)
		}
		return PreviewSource{Name: name, Mode: "external", Path: abs, Owned: false}, nil
	}
	ref := src.DefaultRef
	if v, ok := overrides[name+".ref"]; ok {
		ref = v
	}
	if ref == "" {
		ref = "main"
	}
	if src.Repo == "" {
		return PreviewSource{}, fmt.Errorf("source %s has no repo/path and no %s.path override", name, name)
	}
	repoPath := filepath.Join(a.Home, "repos", name)
	if _, err := os.Stat(filepath.Join(repoPath, ".git")); os.IsNotExist(err) {
		if out, err := runCmd("", nil, "git", "clone", src.Repo, repoPath); err != nil {
			return PreviewSource{}, fmt.Errorf("git clone %s: %w: %s", src.Repo, err, string(out))
		}
	}
	if out, err := runCmd(repoPath, nil, "git", "fetch", "--all", "--prune"); err != nil {
		return PreviewSource{}, fmt.Errorf("git fetch: %w: %s", err, string(out))
	}
	wt := filepath.Join(a.Home, "worktrees", project, previewID, name)
	_ = os.RemoveAll(wt)
	if err := ensureDir(filepath.Dir(wt)); err != nil {
		return PreviewSource{}, err
	}
	if out, err := runCmd(repoPath, nil, "git", "worktree", "add", "--detach", wt, ref); err != nil {
		return PreviewSource{}, fmt.Errorf("git worktree add %s: %w: %s", ref, err, string(out))
	}
	sha := ref
	if out, err := runCmd(wt, nil, "git", "rev-parse", "--short", "HEAD"); err == nil {
		sha = strings.TrimSpace(string(out))
	}
	return PreviewSource{Name: name, Mode: "managed", Ref: sha, Path: wt, Owned: true}, nil
}

func resolveSourcePath(projectPath, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("source path is required")
	}
	expanded := expandPath(value)
	if filepath.IsAbs(expanded) {
		return filepath.Abs(expanded)
	}
	return resolveProjectPath(projectPath, expanded)
}

func resolveProjectPath(projectPath, value string) (string, error) {
	root, err := filepath.Abs(expandPath(projectPath))
	if err != nil {
		return "", err
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return root, nil
	}
	expanded := expandPath(value)
	if filepath.IsAbs(expanded) {
		return "", fmt.Errorf("path %q must be relative to %s", value, root)
	}
	resolved, err := filepath.Abs(filepath.Join(root, expanded))
	if err != nil {
		return "", err
	}
	if !pathWithinRoot(root, resolved) {
		return "", fmt.Errorf("path %q escapes %s", value, root)
	}
	if realResolved, err := filepath.EvalSymlinks(resolved); err == nil {
		realRoot := root
		if evaluatedRoot, rootErr := filepath.EvalSymlinks(root); rootErr == nil {
			realRoot = evaluatedRoot
		}
		if !pathWithinRoot(realRoot, realResolved) {
			return "", fmt.Errorf("path %q resolves outside %s", value, root)
		}
	}
	return resolved, nil
}

func pathWithinRoot(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

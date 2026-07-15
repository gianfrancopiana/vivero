package vivero

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

func defaultHome() string {
	if v := os.Getenv("VIVERO_HOME"); v != "" {
		return expandPath(v)
	}
	h, err := os.UserHomeDir()
	if err != nil || h == "" {
		return ".vivero"
	}
	return filepath.Join(h, ".vivero")
}

func expandPath(p string) string {
	if p == "" {
		return p
	}
	if strings.HasPrefix(p, "~/") || p == "~" {
		h, err := os.UserHomeDir()
		if err == nil {
			if p == "~" {
				return h
			}
			return filepath.Join(h, p[2:])
		}
	}
	return os.ExpandEnv(p)
}

func ensureDir(path string) error { return os.MkdirAll(path, 0o755) }

func writeJSON(w io.Writer, v any) {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func jsonString(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func writeIndentedJSONFile(path string, v any, perm os.FileMode) error {
	body, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	return atomicWriteFile(path, body, perm)
}

var atomicWriteBeforeRenameHook func(path string, body []byte) error

func atomicWriteFile(path string, body []byte, perm os.FileMode) error {
	if err := ensureDir(filepath.Dir(path)); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if atomicWriteBeforeRenameHook != nil {
		if err := atomicWriteBeforeRenameHook(path, body); err != nil {
			return err
		}
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	cleanup = false
	syncParentDir(filepath.Dir(path))
	return nil
}

func syncParentDir(dir string) {
	f, err := os.Open(dir)
	if err != nil {
		return
	}
	defer f.Close()
	_ = f.Sync()
}

func fromJSONString[T any](s string) (T, error) {
	var t T
	if s == "" {
		return t, nil
	}
	err := json.Unmarshal([]byte(s), &t)
	return t, err
}

func output(w io.Writer, jsonOut bool, v any, human string) {
	if jsonOut {
		writeJSON(w, v)
		return
	}
	if human != "" {
		fmt.Fprintln(w, human)
		return
	}
	writeJSON(w, v)
}

func errOut(w io.Writer, jsonOut bool, err error) int {
	if err == nil {
		return 0
	}
	if jsonOut {
		writeJSON(w, cliErrorPayload(err))
	} else {
		fmt.Fprintln(w, "error:", err)
	}
	return 1
}

func hasArg(args []string, s string) bool {
	for _, a := range args {
		if a == s {
			return true
		}
	}
	return false
}

func flagValue(args []string, name string) (string, bool) {
	for i, a := range args {
		if a == name && i+1 < len(args) {
			return args[i+1], true
		}
		if strings.HasPrefix(a, name+"=") {
			return strings.TrimPrefix(a, name+"="), true
		}
	}
	return "", false
}

func flagValues(args []string, name string) []string {
	values := []string{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == name && i+1 < len(args) {
			values = append(values, args[i+1])
			i++
			continue
		}
		if strings.HasPrefix(a, name+"=") {
			values = append(values, strings.TrimPrefix(a, name+"="))
		}
	}
	return values
}

func positiveIntFlag(args []string, name string) (int, bool, error) {
	raw, ok := flagValue(args, name)
	if !ok {
		return 0, false, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, true, fmt.Errorf("%s must be a positive integer", name)
	}
	return value, true, nil
}

func nonNegativeIntFlag(args []string, name string) (int, bool, error) {
	raw, ok := flagValue(args, name)
	if !ok {
		return 0, false, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0, true, fmt.Errorf("%s must be a non-negative integer", name)
	}
	return value, true, nil
}

func positiveFloatFlag(args []string, name string) (float64, bool, error) {
	raw, ok := flagValue(args, name)
	if !ok {
		return 0, false, nil
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value <= 0 {
		return 0, true, fmt.Errorf("%s must be a positive number", name)
	}
	return value, true, nil
}

func durationFlag(args []string, name string, fallback time.Duration) (time.Duration, error) {
	raw, ok := flagValue(args, name)
	if !ok {
		return fallback, nil
	}
	return durationValue(raw, name, fallback)
}

func durationValue(raw, name string, fallback time.Duration) (time.Duration, error) {
	if strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be a duration: %w", name, err)
	}
	if value <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", name)
	}
	return value, nil
}

func positiveDurationOrDefault(raw string, fallback time.Duration) time.Duration {
	value, err := durationValue(raw, "duration", fallback)
	if err != nil {
		return fallback
	}
	return value
}

func collectKV(args []string, flagName string) (map[string]string, error) {
	out := map[string]string{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		var v string
		if a == flagName {
			if i+1 >= len(args) {
				return nil, fmt.Errorf("%s requires value", flagName)
			}
			v = args[i+1]
			i++
		} else if strings.HasPrefix(a, flagName+"=") {
			v = strings.TrimPrefix(a, flagName+"=")
		} else {
			continue
		}
		parts := strings.SplitN(v, "=", 2)
		if len(parts) != 2 || parts[0] == "" {
			return nil, fmt.Errorf("%s value must be key=value", flagName)
		}
		out[parts[0]] = parts[1]
	}
	return out, nil
}

func positionalArgs(args []string) []string {
	out := []string{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			out = append(out, args[i+1:]...)
			break
		}
		if strings.HasPrefix(a, "--") {
			if !strings.Contains(a, "=") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				i++
			}
			continue
		}
		out = append(out, a)
	}
	return out
}

func splitAfterDoubleDash(args []string) []string {
	for i, a := range args {
		if a == "--" {
			return args[i+1:]
		}
	}
	return nil
}

func cleanRelPath(p string) (string, error) {
	if p == "" {
		return "", errors.New("path is required")
	}
	if filepath.IsAbs(p) {
		return "", fmt.Errorf("path must be relative: %s", p)
	}
	clean := filepath.Clean(p)
	if clean == "." || strings.HasPrefix(clean, "..") || strings.Contains(clean, string(filepath.Separator)+".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe path: %s", p)
	}
	return clean, nil
}

func fileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func runCmd(dir string, env map[string]string, name string, args ...string) ([]byte, error) {
	return runCmdWithStdin(dir, env, "", name, args...)
}

func runCmdWithStdin(dir string, env map[string]string, stdin string, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if env != nil {
		cmd.Env = mergeEnv(env)
	}
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	return cmd.CombinedOutput()
}

func mergeEnv(extra map[string]string) []string {
	base := os.Environ()
	m := map[string]string{}
	for _, e := range base {
		if k, v, ok := strings.Cut(e, "="); ok {
			m[k] = v
		}
	}
	for k, v := range extra {
		m[k] = v
	}
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}

func sortedMapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func nowUTC() time.Time { return time.Now().UTC().Truncate(time.Millisecond) }

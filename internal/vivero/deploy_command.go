package vivero

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	deployCommandOutputLimit       = 64 * 1024
	defaultDeployCommandTimeout    = 30 * time.Minute
	defaultDeployStatusTimeout     = 2 * time.Minute
	deployCommandKillWait          = 2 * time.Second
	deployCommandTruncatedTemplate = "\n[output truncated after %d bytes]\n"
)

type deployCommandOptions struct {
	Timeout     time.Duration
	OutputLimit int
}

type deployCommandResult struct {
	Output    string
	Truncated bool
	TimedOut  bool
	Canceled  bool
	ExitCode  int
	Duration  time.Duration
}

type boundedCommandOutput struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	limit int
	total int
}

func (b *boundedCommandOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.total += len(p)
	if b.limit <= 0 {
		return len(p), nil
	}
	remaining := b.limit - b.buf.Len()
	if remaining <= 0 {
		return len(p), nil
	}
	if len(p) > remaining {
		_, _ = b.buf.Write(p[:remaining])
		return len(p), nil
	}
	_, _ = b.buf.Write(p)
	return len(p), nil
}

func (b *boundedCommandOutput) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *boundedCommandOutput) Truncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.limit > 0 && b.total > b.limit
}

func runDeployCommand(parent context.Context, dir string, env map[string]string, command string, opts deployCommandOptions) (deployCommandResult, error) {
	if parent == nil {
		parent = context.Background()
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultDeployCommandTimeout
	}
	limit := opts.OutputLimit
	if limit <= 0 {
		limit = deployCommandOutputLimit
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	buf := &boundedCommandOutput{limit: limit}
	cmd := exec.CommandContext(ctx, "/bin/sh", "-lc", command)
	cmd.Dir = dir
	cmd.Env = mergeEnv(env)
	cmd.Stdout = buf
	cmd.Stderr = buf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = deployCommandKillWait
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}

	started := time.Now()
	err := cmd.Run()
	result := deployCommandResult{ExitCode: 0, Duration: time.Since(started)}
	if exitErr := new(exec.ExitError); errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
	}
	result.TimedOut = errors.Is(ctx.Err(), context.DeadlineExceeded)
	result.Canceled = errors.Is(ctx.Err(), context.Canceled)
	result.Truncated = buf.Truncated()
	result.Output = sanitizeDeployCommandOutput(buf.String(), cmd.Env, result.Truncated, limit)
	if err == nil && ctx.Err() != nil {
		err = ctx.Err()
	}
	if err != nil {
		switch {
		case result.TimedOut:
			return result, fmt.Errorf("deploy command timed out after %s", timeout)
		case result.Canceled:
			return result, fmt.Errorf("deploy command canceled")
		default:
			return result, err
		}
	}
	return result, nil
}

var deploySensitiveAssignmentRE = regexp.MustCompile(`(?i)\b([a-z0-9_.-]*(?:secret|token|password|passwd|api[_-]?key|private[_-]?key|credential|cookie|session)[a-z0-9_.-]*)=([^\s'";]+)`)
var deploySensitiveBearerRE = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[^\s'";]+`)
var releaseStatusTokenRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._:-]{0,63}$`)

func normalizeReleaseStatusOutput(raw string) (string, error) {
	status := strings.TrimSpace(raw)
	if status == "" {
		return "", nil
	}
	if strings.ContainsAny(status, "\r\n") {
		return "", fmt.Errorf("invalid release status output: expected one status token, got multiple lines")
	}
	if !releaseStatusTokenRE.MatchString(status) {
		return "", fmt.Errorf("invalid release status output: %q is not a safe status token", status)
	}
	return status, nil
}

func sanitizeDeployCommandOutput(output string, env []string, truncated bool, limit int) string {
	for _, value := range sensitiveEnvValues(env) {
		output = redactSensitiveValue(output, value, truncated)
	}
	output = deploySensitiveAssignmentRE.ReplaceAllString(output, `${1}=[REDACTED]`)
	output = deploySensitiveBearerRE.ReplaceAllString(output, `${1} [REDACTED]`)
	if truncated {
		output += fmt.Sprintf(deployCommandTruncatedTemplate, limit)
	}
	return output
}

func redactSensitiveValue(output, value string, truncated bool) string {
	output = strings.ReplaceAll(output, value, "[REDACTED]")
	if !truncated || len(value) < 8 {
		return output
	}
	maxPrefix := len(value) - 1
	if maxPrefix > 64 {
		maxPrefix = 64
	}
	for n := maxPrefix; n >= 8; n-- {
		output = strings.ReplaceAll(output, value[:n], "[REDACTED]")
	}
	return output
}

func capDeployOutput(output string, limit int) string {
	if limit <= 0 || len(output) <= limit {
		return output
	}
	return output[:limit] + fmt.Sprintf(deployCommandTruncatedTemplate, limit)
}

func sensitiveEnvValues(env []string) []string {
	values := []string{}
	seen := map[string]bool{}
	for _, item := range env {
		key, value, ok := strings.Cut(item, "=")
		if !ok || len(value) < 4 || !isSensitiveEnvKey(key) || seen[value] {
			continue
		}
		seen[value] = true
		values = append(values, value)
	}
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	return values
}

func isSensitiveEnvKey(key string) bool {
	key = strings.ToLower(key)
	for _, marker := range []string{"secret", "token", "password", "passwd", "api_key", "apikey", "private_key", "credential", "cookie", "session"} {
		if strings.Contains(key, marker) {
			return true
		}
	}
	return false
}

func deployTimeoutValue(raw string, fallback time.Duration) time.Duration {
	return positiveDurationOrDefault(raw, fallback)
}

func deployCommandTimeout(plan DeployPlan, action string) time.Duration {
	action = strings.TrimSpace(action)
	if action == "status" || action == "blue_green_active_slot" {
		if strings.TrimSpace(plan.StatusTimeout) != "" {
			return deployTimeoutValue(plan.StatusTimeout, defaultDeployStatusTimeout)
		}
		if strings.TrimSpace(plan.CommandTimeout) != "" {
			return deployTimeoutValue(plan.CommandTimeout, defaultDeployStatusTimeout)
		}
		return defaultDeployStatusTimeout
	}
	return deployTimeoutValue(plan.CommandTimeout, defaultDeployCommandTimeout)
}

func deployCommandOptionsForPlan(plan DeployPlan, action string) deployCommandOptions {
	return deployCommandOptions{Timeout: deployCommandTimeout(plan, action), OutputLimit: deployCommandOutputLimit}
}

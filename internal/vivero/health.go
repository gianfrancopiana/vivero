package vivero

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func serviceHealthTimeout(h HealthConfig, fallback time.Duration) time.Duration {
	d := positiveDurationOrDefault(h.Timeout, fallback)
	if d < fallback {
		return fallback
	}
	return d
}

func healthCheckInterval(h HealthConfig) time.Duration {
	return positiveDurationOrDefault(h.Interval, time.Second)
}

func waitHTTP(baseURL string, h HealthConfig, timeout time.Duration) error {
	path := h.Path
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	url := strings.TrimRight(baseURL, "/") + path
	interval := healthCheckInterval(h)
	deadline := time.Now().Add(timeout)
	client := httpClientForURL(url)
	var last string
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			expected := h.ExpectStatus
			if expected == 0 && resp.StatusCode >= 200 && resp.StatusCode < 400 {
				return nil
			}
			if expected != 0 && resp.StatusCode == expected {
				return nil
			}
			last = fmt.Sprintf("%s returned %d", url, resp.StatusCode)
		} else {
			last = err.Error()
		}
		time.Sleep(interval)
	}
	if last == "" {
		last = "timeout"
	}
	return fmt.Errorf("health check failed for %s: %s", url, last)
}

func waitDockerHealthCommand(containerID string, h HealthConfig, timeout time.Duration) error {
	if strings.TrimSpace(h.Command) == "" {
		return nil
	}
	interval := healthCheckInterval(h)
	deadline := time.Now().Add(timeout)
	var last string
	for time.Now().Before(deadline) {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		stdout, stderr, exit, err := dockerExecWithTimeout(containerID, []string{"/bin/sh", "-lc", h.Command}, remaining)
		combined := strings.TrimSpace(stderr + "\n" + stdout)
		if err == nil && exit == 0 {
			return nil
		}
		if err != nil {
			last = err.Error()
		} else {
			last = fmt.Sprintf("exit %d", exit)
		}
		if combined != "" {
			last += ": " + combined
		}
		sleep := interval
		if remaining := time.Until(deadline); remaining < sleep {
			sleep = remaining
		}
		if sleep > 0 {
			time.Sleep(sleep)
		}
	}
	if last == "" {
		last = "timeout"
	}
	return fmt.Errorf("health command failed for %s: %s", containerID, last)
}

func httpClientForURL(raw string) *http.Client {
	defaultClient := &http.Client{Timeout: 5 * time.Second}
	parsed, err := url.Parse(raw)
	if err != nil {
		return defaultClient
	}
	host := parsed.Hostname()
	if host == "" || host == "localhost" || net.ParseIP(host) != nil {
		return defaultClient
	}
	resolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{Timeout: 5 * time.Second}
			return d.DialContext(ctx, "udp", "1.1.1.1:53")
		},
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second, Resolver: resolver}
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			Proxy:             http.ProxyFromEnvironment,
			DialContext:       dialer.DialContext,
			ForceAttemptHTTP2: true,
		},
	}
}

func (a *App) Wait(id string, timeout time.Duration) error {
	p, err := a.getPreview(id)
	if err != nil {
		return err
	}
	project, err := a.getProject(p.Project)
	if err != nil {
		return err
	}
	deadline := time.Now().Add(timeout)
	for name, svcState := range p.Services {
		svcCfg, ok := project.Config.Services[name]
		if !ok || svcState.OriginURL == "" {
			continue
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return fmt.Errorf("timeout waiting for %s", name)
		}
		if err := waitHTTP(svcState.OriginURL, svcCfg.Health, remaining); err != nil {
			return err
		}
		svcState.Status = "healthy"
		svcState.LastHealth = "ok"
		_ = a.saveService(id, svcState)
	}
	return nil
}

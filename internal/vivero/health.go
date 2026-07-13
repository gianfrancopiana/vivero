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
	return positiveDurationOrDefault(h.Timeout, fallback)
}

func healthCheckInterval(h HealthConfig) time.Duration {
	return positiveDurationOrDefault(h.Interval, time.Second)
}

func waitHTTP(baseURL string, h HealthConfig, timeout time.Duration) error {
	return waitHTTPWithCheck(baseURL, h, timeout, nil)
}

func waitHTTPWithCheck(baseURL string, h HealthConfig, timeout time.Duration, check func() error) error {
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
		if check != nil {
			if err := check(); err != nil {
				return err
			}
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		requestClient := *client
		if requestClient.Timeout <= 0 || remaining < requestClient.Timeout {
			requestClient.Timeout = remaining
		}
		resp, err := requestClient.Get(url)
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
		sleep := interval
		if remaining := time.Until(deadline); remaining < sleep {
			sleep = remaining
		}
		if err := sleepHealthInterval(sleep, check); err != nil {
			return err
		}
	}
	if last == "" {
		last = "timeout"
	}
	return fmt.Errorf("health check failed for %s: %s", url, last)
}

func sleepHealthInterval(interval time.Duration, check func() error) error {
	if interval <= 0 {
		return nil
	}
	if check == nil {
		time.Sleep(interval)
		return nil
	}
	deadline := time.Now().Add(interval)
	pollEvery := interval
	if pollEvery > time.Second {
		pollEvery = time.Second
	}
	for {
		if err := check(); err != nil {
			return err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil
		}
		if remaining > pollEvery {
			remaining = pollEvery
		}
		time.Sleep(remaining)
	}
}

func (a *App) waitHTTPForContainer(baseURL, containerID string, h HealthConfig, timeout time.Duration) error {
	return waitHTTPWithCheck(baseURL, h, timeout, func() error { return a.checkServiceRuntime("", "", "docker", containerID) })
}

func (a *App) checkServiceRuntime(previewID, service, runtime, containerID string) error {
	if runtime == "compose" {
		states, err := a.containerRuntime().ComposeProjectContainers(previewID, service)
		if err != nil {
			return fmt.Errorf("inspect compose project for service %s: %w", service, err)
		}
		if healthy, _, reason := composeProjectRuntimeStatus(states, containerID); !healthy {
			return fmt.Errorf("compose service %s is not ready: %s", service, reason)
		}
		return nil
	}
	if containerID == "" {
		return nil
	}
	running, err := a.containerRuntime().ContainerRunning(containerID)
	if err != nil {
		return fmt.Errorf("inspect container %s during health check: %w", containerID, err)
	}
	if !running {
		return fmt.Errorf("container %s exited during health check", containerID)
	}
	return nil
}

func (a *App) waitHTTPForServiceRuntime(baseURL, previewID, name string, svc PreviewService, h HealthConfig, timeout time.Duration) error {
	return waitHTTPWithCheck(baseURL, h, timeout, func() error {
		return a.checkServiceRuntime(previewID, name, svc.Runtime, svc.ContainerID)
	})
}

func trackedProcessRunning(pid int, identity string) bool {
	if pid <= 0 || !processExists(pid) {
		return false
	}
	if strings.TrimSpace(identity) == "" {
		return false
	}
	current, err := processIdentity(pid)
	return err == nil && current == identity
}

func (a *App) waitHTTPForServiceResources(baseURL, previewID, name string, svc PreviewService, h HealthConfig, timeout time.Duration) error {
	return waitHTTPWithCheck(baseURL, h, timeout, func() error {
		if err := a.checkServiceRuntime(previewID, name, svc.Runtime, svc.ContainerID); err != nil {
			return err
		}
		if svc.ProxyPID > 0 && !trackedProcessRunning(svc.ProxyPID, svc.ProxyPIDIdentity) {
			return fmt.Errorf("header rewrite proxy pid %d exited during health check", svc.ProxyPID)
		}
		if svc.TunnelPID > 0 && !trackedProcessRunning(svc.TunnelPID, svc.TunnelPIDIdentity) {
			return fmt.Errorf("tunnel pid %d exited during health check", svc.TunnelPID)
		}
		return nil
	})
}

func probeHTTP(baseURL string, h HealthConfig, timeout time.Duration) error {
	path := h.Path
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	endpoint := strings.TrimRight(baseURL, "/") + path
	client := httpClientForURL(endpoint)
	if timeout > 0 && (client.Timeout == 0 || timeout < client.Timeout) {
		client.Timeout = timeout
	}
	resp, err := client.Get(endpoint)
	if err != nil {
		return fmt.Errorf("health probe failed for %s: %w", endpoint, err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	expected := h.ExpectStatus
	if expected == 0 && resp.StatusCode >= 200 && resp.StatusCode < 400 {
		return nil
	}
	if expected != 0 && resp.StatusCode == expected {
		return nil
	}
	return fmt.Errorf("health probe failed for %s: returned %d", endpoint, resp.StatusCode)
}

// reconcileServiceEndpoint keeps the URL in status output truthful without
// turning inspect/list into another full startup wait. It repairs a dead local
// header proxy in-place, then performs exactly one bounded probe of the URL
// reported to callers.
func (a *App) reconcileServiceEndpoint(previewID, name string, state PreviewService, cfg ServiceConfig) (PreviewService, error) {
	return a.reconcileServiceEndpointWithStarter(previewID, name, state, cfg, a.startHeaderRewriteProxyAt)
}

type headerRewriteProxyRestarter func(previewID, service, runtime, containerID, originURL, hostHeader string, publicRewrite PublicRewriteConfig, routes []publicProxyRoute, h HealthConfig, listenHost, preferredURL string, maxWait time.Duration) (string, int, error)

func (a *App) reconcileServiceEndpointWithStarter(previewID, name string, state PreviewService, cfg ServiceConfig, start headerRewriteProxyRestarter) (PreviewService, error) {
	hostHeader := strings.TrimSpace(cfg.TunnelHostHeader)
	routes := publicProxyRoutesForService(state, cfg)
	needsProxy := serviceNeedsHeaderRewriteProxy(cfg, routes)
	if needsProxy && state.OriginURL != "" && !trackedProcessRunning(state.ProxyPID, state.ProxyPIDIdentity) {
		oldProxyURL := state.ProxyURL
		proxyURL, pid, err := start(previewID, name, state.Runtime, state.ContainerID, state.OriginURL, hostHeader, cfg.PublicRewrite, routes, cfg.Health, cfg.ProxyListenHost, oldProxyURL, 5*time.Second)
		if err != nil {
			state.Status = "unhealthy"
			state.LastHealth = err.Error()
			return state, err
		}
		state.ProxyPID = pid
		state.ProxyPIDIdentity, _ = processIdentity(pid)
		state.ProxyURL = proxyURL
		if state.URL == "" || state.URL == oldProxyURL || state.URL == state.OriginURL {
			state.URL = proxyURL
		}
		if err := a.saveService(previewID, state); err != nil {
			return state, err
		}
		a.recordEvent(previewID, "info", "proxy.restarted", "header rewrite proxy restarted during endpoint reconciliation", name, map[string]string{"pid": fmt.Sprint(pid), "url": proxyURL})
	}
	if state.URL == "" {
		return state, nil
	}
	if err := probeHTTP(state.URL, cfg.Health, 5*time.Second); err != nil {
		state.Status = "unhealthy"
		state.LastHealth = err.Error()
		return state, err
	}
	state.Status = "healthy"
	state.LastHealth = "ok"
	return state, nil
}

func waitDockerHealthCommand(containerID string, h HealthConfig, timeout time.Duration) error {
	if h.Command.IsZero() {
		return nil
	}
	interval := healthCheckInterval(h)
	deadline := time.Now().Add(timeout)
	var last string
	for time.Now().Before(deadline) {
		running, err := dockerContainerRunning(containerID)
		if err != nil {
			return fmt.Errorf("inspect container %s during health command: %w", containerID, err)
		}
		if !running {
			return fmt.Errorf("container %s exited during health command", containerID)
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		stdout, stderr, exit, err := dockerExecWithTimeout(containerID, h.Command.RuntimeArgs(), remaining)
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
			if err := sleepHealthInterval(sleep, func() error {
				running, stateErr := dockerContainerRunning(containerID)
				if stateErr != nil {
					return stateErr
				}
				if !running {
					return fmt.Errorf("container %s exited during health command", containerID)
				}
				return nil
			}); err != nil {
				return err
			}
		}
	}
	if last == "" {
		last = "timeout"
	}
	return fmt.Errorf("health command failed for %s: %s", containerID, last)
}

func httpClientForURL(raw string) *http.Client {
	parsed, err := url.Parse(raw)
	if err != nil {
		return &http.Client{Timeout: 5 * time.Second}
	}
	host := parsed.Hostname()
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	dialContext := dialer.DialContext
	proxy := http.ProxyFromEnvironment
	if host == "localhost" || strings.HasSuffix(strings.ToLower(host), ".localhost") {
		proxy = nil
		dialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			_, port, splitErr := net.SplitHostPort(address)
			if splitErr != nil {
				return nil, splitErr
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort("127.0.0.1", port))
		}
	}
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			Proxy:             proxy,
			DialContext:       dialContext,
			ForceAttemptHTTP2: true,
		},
	}
}

func (a *App) Wait(id string, timeout time.Duration) error {
	p, err := a.getPreviewReconciled(id)
	if err != nil {
		return err
	}
	project, err := a.getProject(p.Project)
	if err != nil {
		return err
	}
	deadline := time.Now().Add(timeout)
	for name, svcState := range p.Services {
		if err := a.checkServiceRuntime(id, name, svcState.Runtime, svcState.ContainerID); err != nil {
			return err
		}
		svcCfg, ok := project.Config.Services[name]
		if !ok {
			if backing, backingOK := project.Config.BackingServices[name]; backingOK {
				svcCfg = serviceConfigForBacking(backing)
				ok = true
			}
		}
		if !ok {
			continue
		}
		if svcState.URL == "" {
			if svcState.Status == "unhealthy" || svcState.Status == "dead" {
				return fmt.Errorf("service %s is %s: %s", name, svcState.Status, svcState.LastHealth)
			}
			continue
		}
		var reconcileErr error
		svcState, reconcileErr = a.reconcileServiceEndpoint(id, name, svcState, svcCfg)
		_ = reconcileErr // The full wait below may recover a transient single-probe failure.
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return fmt.Errorf("timeout waiting for %s", name)
		}
		if err := a.waitHTTPForServiceResources(svcState.URL, id, name, svcState, svcCfg.Health, remaining); err != nil {
			svcState.Status = "unhealthy"
			svcState.LastHealth = err.Error()
			_ = a.saveService(id, svcState)
			return err
		}
		svcState.Status = "healthy"
		svcState.LastHealth = "ok"
		_ = a.saveService(id, svcState)
	}
	return a.setPreviewStatus(id, "running")
}

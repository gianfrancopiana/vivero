package vivero

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"text/template"
)

type publicURLTemplateData struct {
	Project       string
	ProjectSlug   string
	PreviewID     string
	PreviewIDSlug string
	Service       string
	ServiceSlug   string
	Branch        string
	BranchSlug    string
	BaseDomain    string
	Labels        map[string]string
	Metadata      map[string]string
}

func publicURLForService(cfg PublicConfig, req UpRequest, service string) (string, error) {
	host := strings.TrimSpace(cfg.Hostname)
	if host == "" {
		if strings.TrimSpace(cfg.BaseDomain) == "" {
			return "", fmt.Errorf("public.baseDomain is required for stable public URLs")
		}
		pattern := strings.TrimSpace(cfg.HostnameTemplate)
		if pattern == "" {
			pattern = "{{ .PreviewID }}"
			if service != "" {
				pattern += "-{{ .Service }}"
			}
			pattern += ".{{ .BaseDomain }}"
		}
		tpl, err := template.New("public-hostname").Option("missingkey=error").Parse(pattern)
		if err != nil {
			return "", err
		}
		var b bytes.Buffer
		branch := canonicalBranchFromMetadata(req.Metadata)
		if branch == "" {
			branch = req.ID
		}
		data := publicURLTemplateData{
			Project:       req.Project,
			ProjectSlug:   publicDNSLabelSlug(req.Project, "project"),
			PreviewID:     req.ID,
			PreviewIDSlug: publicDNSLabelSlug(req.ID, "preview"),
			Service:       service,
			ServiceSlug:   publicDNSLabelSlug(service, "service"),
			Branch:        branch,
			BranchSlug:    publicDNSLabelSlug(branch, req.ID),
			BaseDomain:    cfg.BaseDomain,
			Labels:        req.Labels,
			Metadata:      req.Metadata,
		}
		if err := tpl.Execute(&b, data); err != nil {
			return "", err
		}
		host = b.String()
	}
	host, err := normalizePublicHostname(host, cfg.BaseDomain)
	if err != nil {
		return "", err
	}
	return "https://" + host, nil
}

func normalizePublicHostname(raw, baseDomain string) (string, error) {
	raw = strings.TrimSpace(strings.TrimRight(raw, "/"))
	if raw == "" {
		return "", fmt.Errorf("public hostname resolved empty")
	}
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		parsed, err := url.Parse(raw)
		if err != nil {
			return "", err
		}
		if parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Port() != "" {
			return "", fmt.Errorf("public hostname must be a host-only DNS name: %q", raw)
		}
		raw = parsed.Hostname()
	} else if strings.ContainsAny(raw, "/?#") || strings.Contains(raw, ":") {
		return "", fmt.Errorf("public hostname must be a host-only DNS name: %q", raw)
	}
	host := strings.ToLower(strings.Trim(hostnameOnly(raw), "."))
	if !isDNSHostname(host) {
		return "", fmt.Errorf("public hostname must be a valid DNS name: %q", raw)
	}
	base := strings.ToLower(strings.Trim(strings.TrimSpace(baseDomain), "."))
	if base != "" {
		if !isDNSHostname(base) {
			return "", fmt.Errorf("public.baseDomain must be a valid DNS name: %q", baseDomain)
		}
		if host != base && !strings.HasSuffix(host, "."+base) {
			return "", fmt.Errorf("public hostname %q must be under base domain %q", host, base)
		}
	}
	return host, nil
}

func isDNSHostname(host string) bool {
	if host == "" || len(host) > 253 || net.ParseIP(host) != nil {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, r := range label {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
				continue
			}
			return false
		}
	}
	return true
}

func isNamedPublicTunnel(cfg PublicConfig) bool {
	mode := strings.ToLower(strings.TrimSpace(cfg.Mode))
	provider := strings.ToLower(strings.TrimSpace(cfg.Provider))
	return cfg.Hostname != "" || cfg.HostnameTemplate != "" || cfg.BaseDomain != "" || mode == "named-tunnel" || mode == "fixed" || (provider == "cloudflare" && mode != "quick-tunnel")
}

func (a *App) validateNamedPublicRouteConflicts(req UpRequest, cfg ProjectConfig) error {
	planned, err := plannedNamedPublicHosts(req, cfg)
	if err != nil {
		return err
	}
	if len(planned) == 0 {
		return nil
	}
	previews, err := a.listPreviews()
	if err != nil {
		return err
	}
	for _, p := range previews {
		if p.ID == req.ID || !previewStatusMayOwnPublicRoutes(p.Status) {
			continue
		}
		for name, svc := range p.Services {
			if !previewServiceHasPublicURL(svc) {
				continue
			}
			host := strings.ToLower(hostnameOnly(hostFromOrigin(svc.URL)))
			if newService := planned[host]; newService != "" {
				return fmt.Errorf("public hostname %s for service %s is already used by preview %s service %s", host, newService, p.ID, name)
			}
		}
	}
	return nil
}

func plannedNamedPublicHosts(req UpRequest, cfg ProjectConfig) (map[string]string, error) {
	planned := map[string]string{}
	if !isNamedPublicTunnel(cfg.Public) {
		return planned, nil
	}
	for _, name := range sortedMapKeys(cfg.Services) {
		svc := cfg.Services[name]
		ports, err := servicePortPlan(svc)
		if err != nil {
			return nil, fmt.Errorf("public URL port plan for service %s: %w", name, err)
		}
		if !(req.Public || svc.Public) || len(ports) == 0 {
			continue
		}
		publicURL, err := publicURLForService(cfg.Public, req, name)
		if err != nil {
			return nil, fmt.Errorf("public URL for service %s: %w", name, err)
		}
		host := strings.ToLower(hostnameOnly(hostFromOrigin(publicURL)))
		if other := planned[host]; other != "" {
			return nil, fmt.Errorf("public hostname %s is used by both services %s and %s", host, other, name)
		}
		planned[host] = name
	}
	return planned, nil
}

func previewStatusMayOwnPublicRoutes(status string) bool {
	switch status {
	case "pending", "preparing_source", "starting_apps", "running", "unhealthy":
		return true
	default:
		return false
	}
}

func (a *App) servePublicPreview(w http.ResponseWriter, r *http.Request) bool {
	host := publicRouteHost(r)
	if !isRoutablePublicHost(host) {
		return false
	}
	p, svcName, svc, ok := a.previewServiceForPublicHost(host)
	if !ok {
		http.NotFound(w, r)
		return true
	}
	project, err := a.getProject(p.Project)
	if err != nil {
		http.Error(w, "preview project unavailable", http.StatusGone)
		return true
	}
	if !previewServiceIsActive(p, svc) {
		writeInactivePublicPreview(w, project.Config.Public, p, svcName)
		return true
	}
	svcCfg, ok := project.Config.Services[svcName]
	if !ok {
		http.Error(w, "preview service unavailable", http.StatusGone)
		return true
	}
	targetRaw := serviceOriginURL(svc)
	if svc.ProxyURL != "" {
		targetRaw = svc.ProxyURL
	}
	if targetRaw == "" {
		http.Error(w, "preview service has no upstream", http.StatusBadGateway)
		return true
	}
	target, err := url.Parse(targetRaw)
	if err != nil {
		http.Error(w, "preview service upstream is invalid", http.StatusBadGateway)
		return true
	}
	hostHeader, err := publicProxyHostHeader(target, svcCfg)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return true
	}
	newHeaderRewriteProxy(target, hostHeader, svcCfg.PublicRewrite).ServeHTTP(w, r)
	return true
}

func publicRouteHost(r *http.Request) string {
	host := ""
	if r != nil {
		host = r.Host
		if host == "" && r.URL != nil {
			host = r.URL.Host
		}
	}
	host = strings.ToLower(hostnameOnly(host))
	if isRoutablePublicHost(host) {
		return host
	}
	if r != nil {
		forwardedHost := strings.ToLower(hostnameOnly(firstForwardedValue(r.Header.Get("X-Forwarded-Host"))))
		if isRoutablePublicHost(forwardedHost) {
			return forwardedHost
		}
	}
	return host
}

func isRoutablePublicHost(host string) bool {
	host = strings.ToLower(hostnameOnly(host))
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return false
	}
	return net.ParseIP(host) == nil
}

func publicProxyHostHeader(target *url.URL, svcCfg ServiceConfig) (string, error) {
	if !isLoopbackHost(target.Host) {
		return "", fmt.Errorf("preview service upstream must be loopback")
	}
	hostHeader := svcCfg.TunnelHostHeader
	if hostHeader == "" {
		hostHeader = svcCfg.OriginHost
	}
	if hostHeader == "" {
		hostHeader = target.Host
	}
	return hostHeader, nil
}

func isLoopbackHost(host string) bool {
	host = strings.ToLower(hostnameOnly(host))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (a *App) previewServiceForPublicHost(host string) (PreviewRecord, string, PreviewService, bool) {
	previews, err := a.listPreviews()
	if err != nil {
		return PreviewRecord{}, "", PreviewService{}, false
	}
	for _, p := range previews {
		for name, svc := range p.Services {
			if !previewServiceHasPublicURL(svc) {
				continue
			}
			if strings.ToLower(hostnameOnly(hostFromOrigin(svc.URL))) == host {
				return p, name, svc, true
			}
		}
	}
	return PreviewRecord{}, "", PreviewService{}, false
}

func previewServiceHasPublicURL(svc PreviewService) bool {
	if strings.TrimSpace(svc.URL) == "" {
		return false
	}
	host := strings.ToLower(hostnameOnly(hostFromOrigin(svc.URL)))
	if !isRoutablePublicHost(host) {
		return false
	}
	originHost := strings.ToLower(hostnameOnly(hostFromOrigin(serviceOriginURL(svc))))
	return originHost != "" && host != originHost
}

func previewServiceIsActive(p PreviewRecord, svc PreviewService) bool {
	if p.Status != "running" && p.Status != "starting_apps" {
		return false
	}
	return svc.Status == "healthy" || svc.Status == "running"
}

func writeInactivePublicPreview(w http.ResponseWriter, cfg PublicConfig, p PreviewRecord, service string) {
	status := http.StatusGone
	if strings.TrimSpace(cfg.InactiveBehavior) == "404" {
		status = http.StatusNotFound
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, "Vivero preview %s/%s is inactive\n", p.ID, service)
}

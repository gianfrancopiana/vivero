package vivero

import (
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
)

const defaultPrimaryPortName = "http"

type ServicePort struct {
	Name      string
	Container int
	Host      int
	Protocol  string
	Primary   bool
	Legacy    bool
}

func servicePortPlan(svc ServiceConfig) ([]ServicePort, error) {
	if svc.Port < 0 {
		return nil, fmt.Errorf("port must be positive")
	}
	if svc.Port > 0 && len(svc.Ports) > 0 {
		return nil, fmt.Errorf("cannot declare both legacy port and named ports")
	}
	if svc.Port > 0 {
		return []ServicePort{{Name: defaultPrimaryPortName, Container: svc.Port, Host: svc.Port, Protocol: "tcp", Primary: true, Legacy: true}}, nil
	}
	if len(svc.Ports) == 0 {
		return nil, nil
	}
	keys := make([]string, 0, len(svc.Ports))
	for name := range svc.Ports {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			return nil, fmt.Errorf("named port has empty name")
		}
		keys = append(keys, trimmed)
	}
	sort.Strings(keys)
	primaryName := strings.TrimSpace(svc.PrimaryPort)
	if primaryName == "" {
		if _, ok := svc.Ports[defaultPrimaryPortName]; ok {
			primaryName = defaultPrimaryPortName
		} else {
			primaryName = keys[0]
		}
	}
	out := make([]ServicePort, 0, len(keys))
	seen := map[string]bool{}
	primarySeen := false
	for _, name := range keys {
		if seen[name] {
			return nil, fmt.Errorf("duplicate named port %q", name)
		}
		seen[name] = true
		cfg, ok := svc.Ports[name]
		if !ok {
			return nil, fmt.Errorf("named port %q uses surrounding whitespace", name)
		}
		if cfg.Container <= 0 {
			return nil, fmt.Errorf("named port %s container port must be positive", name)
		}
		if cfg.Host < 0 {
			return nil, fmt.Errorf("named port %s host port must be positive or zero for dynamic", name)
		}
		protocol := strings.ToLower(strings.TrimSpace(cfg.Protocol))
		if protocol == "" {
			protocol = "tcp"
		}
		if protocol != "tcp" {
			return nil, fmt.Errorf("named port %s protocol %q is unsupported; use tcp", name, protocol)
		}
		primary := name == primaryName
		if primary {
			primarySeen = true
		}
		out = append(out, ServicePort{Name: name, Container: cfg.Container, Host: cfg.Host, Protocol: protocol, Primary: primary})
	}
	if !primarySeen {
		return nil, fmt.Errorf("primaryPort %q does not match a named port", primaryName)
	}
	return out, nil
}

func primaryPreviewPort(ports map[string]PreviewPort) (PreviewPort, bool) {
	if len(ports) == 0 {
		return PreviewPort{}, false
	}
	keys := sortedMapKeys(ports)
	for _, name := range keys {
		port := ports[name]
		if port.Primary {
			return port, true
		}
	}
	if len(keys) == 1 {
		return ports[keys[0]], true
	}
	return PreviewPort{}, false
}

func originHostForService(svc ServiceConfig) string {
	host := strings.TrimSpace(svc.OriginHost)
	if host == "" {
		return "127.0.0.1"
	}
	return host
}

func previewPortsFromPublished(configured []ServicePort, published []PreviewPort, originHost string) (map[string]PreviewPort, error) {
	byName := make(map[string]PreviewPort, len(published))
	for _, port := range published {
		byName[port.Name] = port
	}
	out := make(map[string]PreviewPort, len(configured))
	for _, want := range configured {
		got, ok := byName[want.Name]
		if !ok {
			return nil, fmt.Errorf("published port %s missing from Docker runtime state", want.Name)
		}
		got.Primary = want.Primary
		if got.Protocol == "" {
			got.Protocol = want.Protocol
		}
		if got.Host <= 0 {
			return nil, fmt.Errorf("published port %s has invalid host port %d", want.Name, got.Host)
		}
		got.URL = fmt.Sprintf("http://%s:%d", originHost, got.Host)
		out[want.Name] = got
	}
	return out, nil
}

func parseDockerPublishedPort(value string) (int, string, error) {
	line := strings.TrimSpace(value)
	if line == "" {
		return 0, "", fmt.Errorf("empty docker published port")
	}
	host, portText, err := net.SplitHostPort(line)
	if err != nil {
		return 0, "", fmt.Errorf("parse docker published port %q: %w", value, err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port <= 0 {
		return 0, "", fmt.Errorf("invalid docker published host port %q", portText)
	}
	return port, host, nil
}

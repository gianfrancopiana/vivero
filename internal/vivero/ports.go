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
	Name           string
	Container      int
	Host           int
	HostIP         string
	Protocol       string
	Primary        bool
	Legacy         bool
	ComposeService string
	PublicPath     string
	PublicOrigins  []string
}

func servicePortPlan(svc ServiceConfig) ([]ServicePort, error) {
	if svc.Port < 0 {
		return nil, fmt.Errorf("port must be positive")
	}
	if svc.Port > 0 && len(svc.Ports) > 0 {
		return nil, fmt.Errorf("cannot declare both legacy port and named ports")
	}
	if svc.Port > 0 {
		return []ServicePort{{Name: defaultPrimaryPortName, Container: svc.Port, Host: svc.Port, HostIP: "127.0.0.1", Protocol: "tcp", Primary: true, Legacy: true}}, nil
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
	publicPaths := map[string]string{}
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
		if hostIP := strings.TrimSpace(cfg.HostIP); hostIP != "" && net.ParseIP(hostIP) == nil {
			return nil, fmt.Errorf("named port %s hostIp %q must be an IP address", name, hostIP)
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
		hostIP := strings.TrimSpace(cfg.HostIP)
		if hostIP == "" {
			hostIP = "127.0.0.1"
		}
		publicPath := strings.TrimSpace(cfg.PublicPath)
		if len(cfg.PublicOrigins) > 0 && publicPath == "" {
			return nil, fmt.Errorf("named port %s publicOrigins requires publicPath", name)
		}
		if publicPath != "" {
			if primary {
				return nil, fmt.Errorf("named port %s is primary and cannot declare publicPath", name)
			}
			if !strings.HasPrefix(publicPath, "/") || publicPath == "/" || strings.HasSuffix(publicPath, "/") {
				return nil, fmt.Errorf("named port %s publicPath must start with /, must not be /, and must not end with /", name)
			}
			if previous, exists := publicPaths[publicPath]; exists {
				return nil, fmt.Errorf("named ports %s and %s declare duplicate publicPath %s", previous, name, publicPath)
			}
			publicPaths[publicPath] = name
		}
		origins := make([]string, 0, len(cfg.PublicOrigins))
		for _, origin := range cfg.PublicOrigins {
			if origin = strings.TrimSpace(origin); origin == "" {
				return nil, fmt.Errorf("named port %s publicOrigins cannot contain an empty origin", name)
			}
			origins = append(origins, origin)
		}
		composeService := strings.TrimSpace(cfg.ComposeService)
		out = append(out, ServicePort{Name: name, Container: cfg.Container, Host: cfg.Host, HostIP: hostIP, Protocol: protocol, Primary: primary, ComposeService: composeService, PublicPath: publicPath, PublicOrigins: origins})
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
		ports, err := servicePortPlan(svc)
		if err == nil {
			for _, port := range ports {
				if port.Primary && port.HostIP != "" && port.HostIP != "0.0.0.0" && port.HostIP != "::" {
					return port.HostIP
				}
			}
		}
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
		urlHost := got.HostIP
		if urlHost == "" {
			urlHost = want.HostIP
		}
		if want.Primary && strings.TrimSpace(originHost) != "" {
			urlHost = originHost
		}
		if urlHost == "" || urlHost == "0.0.0.0" || urlHost == "::" {
			urlHost = "127.0.0.1"
		}
		got.URL = "http://" + net.JoinHostPort(urlHost, fmt.Sprint(got.Host))
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

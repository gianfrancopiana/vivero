package vivero

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type publicOriginContextKey struct{}

type publicProxyRoute struct {
	Path       string   `json:"path"`
	Target     string   `json:"target"`
	HostHeader string   `json:"hostHeader,omitempty"`
	Origins    []string `json:"origins,omitempty"`
}

var scriptNonceRE = regexp.MustCompile(`'nonce-([^']+)'`)

func runHeaderRewriteProxy(listen, target, hostHeader string, publicRewrite PublicRewriteConfig, routes []publicProxyRoute) error {
	if listen == "" {
		return fmt.Errorf("--listen is required")
	}
	if target == "" {
		return fmt.Errorf("--target is required")
	}
	targetURL, err := url.Parse(target)
	if err != nil {
		return err
	}
	server := &http.Server{
		Addr:              listen,
		Handler:           newRoutedHeaderRewriteProxy(targetURL, hostHeader, publicRewrite, routes),
		ReadHeaderTimeout: 10 * time.Second,
	}
	return server.ListenAndServe()
}

func newHeaderRewriteProxy(target *url.URL, hostHeader string, publicRewrite PublicRewriteConfig) *httputil.ReverseProxy {
	return newHeaderRewriteProxyWithRewriteHost(target, hostHeader, hostHeader, "", publicRewrite)
}

func newPublicRouteHeaderRewriteProxy(target *url.URL, hostHeader, rewriteHostHeader, basePublicOrigin string, publicRewrite PublicRewriteConfig) *httputil.ReverseProxy {
	if rewriteHostHeader == "" {
		rewriteHostHeader = hostHeader
	}
	return newHeaderRewriteProxyWithRewriteHost(target, hostHeader, rewriteHostHeader, basePublicOrigin, publicRewrite)
}

func newHeaderRewriteProxyWithRewriteHost(target *url.URL, hostHeader, rewriteHostHeader, basePublicOrigin string, publicRewrite PublicRewriteConfig) *httputil.ReverseProxy {
	return newHeaderRewriteProxyWithRoutes(target, hostHeader, rewriteHostHeader, basePublicOrigin, publicRewrite, nil)
}

func newHeaderRewriteProxyWithRoutes(target *url.URL, hostHeader, rewriteHostHeader, basePublicOrigin string, publicRewrite PublicRewriteConfig, routes []publicProxyRoute) *httputil.ReverseProxy {
	proxy := httputil.NewSingleHostReverseProxy(target)
	baseDirector := proxy.Director
	rewriter := newPublicPreviewRewriter(target, rewriteHostHeader, basePublicOrigin, publicRewrite)
	for _, route := range routes {
		for _, origin := range route.Origins {
			origin = strings.TrimRight(strings.TrimSpace(origin), "/")
			if origin != "" {
				rewriter.routedOrigins = append(rewriter.routedOrigins, routedOriginRewrite{From: origin, Path: route.Path})
			}
		}
	}
	proxy.Director = func(req *http.Request) {
		publicOrigin := publicOriginForIncomingRequest(req)
		publicScheme := schemeFromOrigin(publicOrigin)
		maskedOrigin := maskedOriginForIncomingRequest(req, hostHeader)
		baseDirector(req)
		if publicOrigin != "" {
			rewriteRequestOriginHeaders(req.Header, publicOrigin, maskedOrigin)
			// Rewritable response bodies must arrive uncompressed. Otherwise the
			// response rewriter has to silently pass through localhost origins.
			req.Header.Set("Accept-Encoding", "identity")
			*req = *req.WithContext(context.WithValue(req.Context(), publicOriginContextKey{}, publicOrigin))
		}
		if publicScheme != "" {
			req.Header.Set("X-Forwarded-Proto", publicScheme)
			if publicScheme == "https" {
				req.Header.Set("X-Forwarded-Port", "443")
			}
		}
		upstreamHost := hostHeader
		if upstreamHost == "" {
			upstreamHost = target.Host
		}
		req.Host = upstreamHost
		req.Header.Set("X-Forwarded-Host", upstreamHost)
		req.Header.Set("X-Forwarded-Server", upstreamHost)
		req.Header.Del("Forwarded")
	}
	proxy.ModifyResponse = func(resp *http.Response) error {
		publicOrigin, _ := resp.Request.Context().Value(publicOriginContextKey{}).(string)
		if publicOrigin == "" {
			return nil
		}
		rewriteResponseHeaders(resp.Header, rewriter, publicOrigin)
		if !shouldRewriteResponseBody(resp.Header) {
			return nil
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		_ = resp.Body.Close()
		rewrittenText := rewriter.rewrite(string(body), publicOrigin)
		if isHTMLResponse(resp.Header) {
			rewrittenText = injectPublicPreviewRuntime(rewrittenText, publicOrigin, rewriter.rewriteBasePublicOrigin(publicOrigin), rewriter.basePaths, contentSecurityPolicyNonce(resp.Header))
		}
		rewritten := []byte(rewrittenText)
		resp.Body = io.NopCloser(bytes.NewReader(rewritten))
		resp.ContentLength = int64(len(rewritten))
		resp.Header.Set("Content-Length", fmt.Sprint(len(rewritten)))
		return nil
	}
	return proxy
}

func newRoutedHeaderRewriteProxy(target *url.URL, hostHeader string, publicRewrite PublicRewriteConfig, routes []publicProxyRoute) http.Handler {
	ordered := append([]publicProxyRoute(nil), routes...)
	sort.SliceStable(ordered, func(i, j int) bool { return len(ordered[i].Path) > len(ordered[j].Path) })
	type routeHandler struct {
		path    string
		handler http.Handler
	}
	handlers := make([]routeHandler, 0, len(ordered))
	for _, route := range ordered {
		routeTarget, err := url.Parse(route.Target)
		if err != nil || routeTarget.Scheme == "" || routeTarget.Host == "" {
			continue
		}
		routeHost := route.HostHeader
		if routeHost == "" {
			routeHost = hostHeader
		}
		handlers = append(handlers, routeHandler{path: route.Path, handler: newHeaderRewriteProxyWithRoutes(routeTarget, routeHost, hostHeader, "", publicRewrite, ordered)})
	}
	primary := newHeaderRewriteProxyWithRoutes(target, hostHeader, hostHeader, "", publicRewrite, ordered)
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		for _, route := range handlers {
			if publicRoutePathMatches(req.URL.Path, route.path) {
				route.handler.ServeHTTP(w, req)
				return
			}
		}
		primary.ServeHTTP(w, req)
	})
}

func publicRoutePathMatches(requestPath, routePath string) bool {
	if requestPath == routePath {
		return true
	}
	return strings.HasPrefix(requestPath, routePath+"/")
}

type publicPreviewRewriter struct {
	exactOrigins          []string
	encodedOrigins        []encodedOriginRewrite
	exactHosts            []string
	protocolRelativeHosts []string
	basePaths             []string
	replacements          []PublicRewriteTemplate
	routedOrigins         []routedOriginRewrite
	originHost            string
	basePublicOrigin      string
	devOriginRE           *regexp.Regexp
}

type routedOriginRewrite struct {
	From string
	Path string
}

type encodedOriginRewrite struct {
	From string
	To   string
}

func newPublicPreviewRewriter(target *url.URL, hostHeader, basePublicOrigin string, cfg PublicRewriteConfig) publicPreviewRewriter {
	exact := []string{}
	if target != nil && target.Scheme != "" && target.Host != "" {
		exact = append(exact, strings.TrimRight(target.String(), "/"))
	}
	for _, origin := range cfg.Origins {
		origin = strings.TrimRight(strings.TrimSpace(origin), "/")
		if origin != "" {
			exact = append(exact, origin)
		}
	}
	sort.Slice(exact, func(i, j int) bool { return len(exact[i]) > len(exact[j]) })
	exactHosts := []string{}
	for _, host := range cfg.Hosts {
		host = strings.TrimSpace(host)
		if host != "" {
			exactHosts = append(exactHosts, host)
		}
	}
	sort.Slice(exactHosts, func(i, j int) bool { return len(exactHosts[i]) > len(exactHosts[j]) })
	hostname := hostnameOnly(hostHeader)
	encodedOrigins := encodedOriginRewrites(exact, exactHosts, hostname)
	protocolRelativeHosts := protocolRelativeHostRewrites(exactHosts, hostname)
	basePaths := normalizePublicRewriteBasePaths(cfg.BasePaths)
	var re *regexp.Regexp
	if hostname != "" {
		re = regexp.MustCompile(`https?://(?:[A-Za-z0-9-]+\.)?` + regexp.QuoteMeta(hostname) + `(?::\d+)?`)
	}
	return publicPreviewRewriter{exactOrigins: exact, encodedOrigins: encodedOrigins, exactHosts: exactHosts, protocolRelativeHosts: protocolRelativeHosts, basePaths: basePaths, replacements: cfg.Replacements, originHost: hostname, basePublicOrigin: strings.TrimRight(basePublicOrigin, "/"), devOriginRE: re}
}

func (r publicPreviewRewriter) rewrite(input, publicOrigin string) string {
	if input == "" || publicOrigin == "" {
		return input
	}
	publicHost := hostFromOrigin(publicOrigin)
	publicScheme := schemeFromOrigin(publicOrigin)
	templateContext := publicRewriteTemplateContext{
		PublicOrigin:     publicOrigin,
		PublicHost:       publicHost,
		PublicScheme:     publicScheme,
		BasePublicOrigin: r.rewriteBasePublicOrigin(publicOrigin),
	}
	templateContext.BasePublicHost = hostFromOrigin(templateContext.BasePublicOrigin)
	templateContext.BasePublicScheme = schemeFromOrigin(templateContext.BasePublicOrigin)
	out := input
	for _, routed := range r.routedOrigins {
		to := strings.TrimRight(publicOrigin, "/") + routed.Path
		if strings.HasPrefix(strings.ToLower(routed.From), "ws://") {
			to = "ws://" + strings.TrimPrefix(to, "http://")
			if strings.HasPrefix(to, "ws://https://") {
				to = "wss://" + strings.TrimPrefix(to, "ws://https://")
			}
		} else if strings.HasPrefix(strings.ToLower(routed.From), "wss://") {
			to = "wss://" + strings.TrimPrefix(strings.TrimPrefix(to, "http://"), "https://")
		}
		out = strings.ReplaceAll(out, routed.From, to)
	}
	for _, origin := range r.exactOrigins {
		out = strings.ReplaceAll(out, origin, publicOrigin)
	}
	encodedPublicOrigin := url.QueryEscape(publicOrigin)
	encodedPublicOriginSlash := url.QueryEscape(strings.TrimRight(publicOrigin, "/") + "/")
	for _, replacement := range r.encodedOrigins {
		to := strings.ReplaceAll(replacement.To, "{publicOriginSlash}", encodedPublicOriginSlash)
		to = strings.ReplaceAll(to, "{publicOrigin}", encodedPublicOrigin)
		out = strings.ReplaceAll(out, replacement.From, to)
	}
	if r.devOriginRE != nil {
		out = r.rewriteDevOrigins(out, publicOrigin)
	}
	if publicHost != "" {
		for _, host := range r.exactHosts {
			out = strings.ReplaceAll(out, host, publicHost)
		}
		for _, host := range r.protocolRelativeHosts {
			out = replaceProtocolRelativeHost(out, host, publicHost)
		}
	}
	for _, replacement := range r.replacements {
		from := expandPublicRewriteTemplate(replacement.From, templateContext)
		if from == "" {
			continue
		}
		out = strings.ReplaceAll(out, from, expandPublicRewriteTemplate(replacement.To, templateContext))
	}
	if len(r.basePaths) > 0 && templateContext.BasePublicOrigin != "" && templateContext.BasePublicOrigin != publicOrigin {
		out = rewriteRouteBasePaths(out, publicOrigin, templateContext.BasePublicOrigin, r.basePaths)
	}
	out = normalizePublicHostScheme(out, publicHost, publicScheme)
	if templateContext.BasePublicHost != "" && templateContext.BasePublicHost != publicHost {
		out = normalizePublicHostScheme(out, templateContext.BasePublicHost, templateContext.BasePublicScheme)
	}
	return out
}

func (r publicPreviewRewriter) rewriteBasePublicOrigin(publicOrigin string) string {
	if r.basePublicOrigin != "" {
		return r.basePublicOrigin
	}
	return publicOrigin
}

func (r publicPreviewRewriter) rewriteDevOrigins(input, publicOrigin string) string {
	if r.devOriginRE == nil {
		return input
	}
	basePublicOrigin := r.basePublicOrigin
	if basePublicOrigin == "" {
		basePublicOrigin = publicOrigin
	}
	basePublicHost := hostFromOrigin(basePublicOrigin)
	return r.devOriginRE.ReplaceAllStringFunc(input, func(match string) string {
		subdomain, ok := r.devOriginSubdomain(match)
		if !ok {
			return match
		}
		if subdomain == "" {
			return basePublicOrigin
		}
		return publicOriginForDevSubdomain(basePublicOrigin, basePublicHost, subdomain)
	})
}

func (r publicPreviewRewriter) devOriginSubdomain(origin string) (string, bool) {
	parsed, err := url.Parse(origin)
	if err != nil {
		return "", false
	}
	host := strings.ToLower(hostnameOnly(parsed.Host))
	originHost := strings.ToLower(hostnameOnly(r.originHost))
	if host == "" || originHost == "" {
		return "", false
	}
	if host == originHost {
		return "", true
	}
	suffix := "." + originHost
	if !strings.HasSuffix(host, suffix) {
		return "", false
	}
	subdomain := strings.TrimSuffix(host, suffix)
	if subdomain == "" || strings.Contains(subdomain, ".") {
		return "", false
	}
	return subdomain, true
}

func publicOriginForDevSubdomain(publicOrigin, publicHost, subdomain string) string {
	parsed, err := url.Parse(publicOrigin)
	if err != nil {
		return publicOrigin
	}
	host := parsed.Hostname()
	if host == "" {
		host = hostnameOnly(publicHost)
	}
	if !isRoutablePublicHost(host) || strings.HasSuffix(strings.ToLower(host), ".trycloudflare.com") {
		return publicOrigin
	}
	label := publicDNSLabelSlug(subdomain, "host")
	if label == "" || strings.HasPrefix(strings.ToLower(host), label+"-") {
		return publicOrigin
	}
	prefixedHost := label + "-" + host
	if !isDNSHostname(prefixedHost) {
		return publicOrigin
	}
	if port := parsed.Port(); port != "" {
		parsed.Host = net.JoinHostPort(prefixedHost, port)
	} else {
		parsed.Host = prefixedHost
	}
	return strings.TrimRight(parsed.String(), "/")
}

func encodedOriginRewrites(origins, hosts []string, hostHeader string) []encodedOriginRewrite {
	seen := map[string]bool{}
	candidates := []string{}
	for _, origin := range origins {
		if origin = strings.TrimRight(strings.TrimSpace(origin), "/"); origin != "" {
			candidates = append(candidates, origin)
		}
	}
	for _, host := range hosts {
		if host = strings.TrimSpace(host); host != "" {
			candidates = append(candidates, "http://"+host, "https://"+host)
		}
	}
	if hostHeader != "" {
		candidates = append(candidates, "http://"+hostHeader, "https://"+hostHeader)
	}
	out := []encodedOriginRewrite{}
	for _, candidate := range candidates {
		pairs := []encodedOriginRewrite{
			{From: url.QueryEscape(candidate + "/"), To: "{publicOriginSlash}"},
			{From: url.QueryEscape(candidate), To: "{publicOrigin}"},
		}
		for _, pair := range pairs {
			for _, from := range []string{pair.From, strings.ToLower(pair.From)} {
				if from != "" && !seen[from] {
					seen[from] = true
					out = append(out, encodedOriginRewrite{From: from, To: pair.To})
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return len(out[i].From) > len(out[j].From) })
	return out
}

func protocolRelativeHostRewrites(hosts []string, hostHeader string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, host := range append(append([]string{}, hosts...), hostHeader) {
		host = strings.TrimSpace(hostnameOnly(host))
		if host == "" || seen[host] {
			continue
		}
		seen[host] = true
		out = append(out, host)
	}
	sort.Slice(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}

func replaceProtocolRelativeHost(input, fromHost, toHost string) string {
	out := input
	for _, prefix := range []string{"//", `\/\/`, `\\/\\/`} {
		out = strings.ReplaceAll(out, prefix+fromHost, prefix+toHost)
	}
	return out
}

func normalizePublicRewriteBasePaths(paths []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, raw := range paths {
		path := strings.TrimSpace(raw)
		if path == "" {
			continue
		}
		if i := strings.IndexAny(path, "?#"); i >= 0 {
			path = path[:i]
		}
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		if path != "/" {
			path = strings.TrimRight(path, "/")
		}
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		out = append(out, path)
	}
	sort.Slice(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}

func rewriteRouteBasePaths(input, routePublicOrigin, basePublicOrigin string, basePaths []string) string {
	if input == "" || routePublicOrigin == "" || basePublicOrigin == "" || routePublicOrigin == basePublicOrigin || len(basePaths) == 0 {
		return input
	}
	route, err := url.Parse(strings.TrimRight(routePublicOrigin, "/"))
	if err != nil || route.Scheme == "" || route.Host == "" {
		return input
	}
	base, err := url.Parse(strings.TrimRight(basePublicOrigin, "/"))
	if err != nil || base.Scheme == "" || base.Host == "" {
		return input
	}
	routeOrigins := []string{route.Scheme + "://" + route.Host}
	if route.Scheme == "https" {
		routeOrigins = append(routeOrigins, "http://"+route.Host)
	}
	baseOrigins := map[string]string{}
	for _, origin := range routeOrigins {
		baseOrigins[origin] = base.Scheme + "://" + base.Host
	}
	out := input
	for _, basePath := range normalizePublicRewriteBasePaths(basePaths) {
		for _, routeOrigin := range routeOrigins {
			baseOrigin := baseOrigins[routeOrigin]
			for _, backslashes := range []int{0, 1, 2, 4} {
				from := escapedOriginPath(routeOrigin, basePath, backslashes)
				to := escapedOriginPath(baseOrigin, basePath, backslashes)
				if from != "" && to != "" {
					out = replaceRouteBasePathBoundary(out, from, to)
				}
			}
		}
	}
	return out
}

func replaceRouteBasePathBoundary(input, from, to string) string {
	if input == "" || from == "" || from == to {
		return input
	}
	var b strings.Builder
	changed := false
	last := 0
	search := 0
	for search < len(input) {
		idx := strings.Index(input[search:], from)
		if idx < 0 {
			break
		}
		idx += search
		end := idx + len(from)
		if hasRouteBasePathBoundary(input, end) {
			if !changed {
				b.Grow(len(input))
				changed = true
			}
			b.WriteString(input[last:idx])
			b.WriteString(to)
			last = end
		}
		search = end
	}
	if !changed {
		return input
	}
	b.WriteString(input[last:])
	return b.String()
}

func hasRouteBasePathBoundary(input string, pos int) bool {
	if pos >= len(input) {
		return true
	}
	switch input[pos] {
	case '/', '?', '#':
		return true
	case '"', '\'', '`', '<', '>', ' ', '\t', '\n', '\r', ')', ']', '}', ',':
		return true
	case '\\':
		for i := pos; i < len(input) && input[i] == '\\'; i++ {
			if i+1 < len(input) && input[i+1] == '/' {
				return true
			}
		}
	case '%':
		return pos+2 < len(input) && input[pos+1] == '2' && (input[pos+2] == 'f' || input[pos+2] == 'F')
	}
	return false
}

func escapedOriginPath(origin, path string, backslashes int) string {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	slash := strings.Repeat("\\", backslashes) + "/"
	return escapedSchemeHost(parsed.Scheme, parsed.Host, backslashes) + strings.ReplaceAll(path, "/", slash)
}

func normalizePublicHostScheme(input, publicHost, publicScheme string) string {
	if publicHost == "" || publicScheme == "" || publicScheme == "http" {
		return input
	}
	out := input
	for _, backslashes := range []int{0, 1, 2, 4} {
		out = strings.ReplaceAll(out, escapedSchemeHost("http", publicHost, backslashes), escapedSchemeHost(publicScheme, publicHost, backslashes))
	}
	return out
}

func escapedSchemeHost(scheme, host string, backslashes int) string {
	if backslashes <= 0 {
		return scheme + "://" + host
	}
	escapedSlashPrefix := strings.Repeat("\\", backslashes) + "/"
	return scheme + ":" + escapedSlashPrefix + escapedSlashPrefix + host
}

type publicRewriteTemplateContext struct {
	PublicOrigin     string
	PublicHost       string
	PublicScheme     string
	BasePublicOrigin string
	BasePublicHost   string
	BasePublicScheme string
}

func expandPublicRewriteTemplate(template string, ctx publicRewriteTemplateContext) string {
	out := strings.ReplaceAll(template, "{publicOrigin}", ctx.PublicOrigin)
	out = strings.ReplaceAll(out, "{publicHost}", ctx.PublicHost)
	out = strings.ReplaceAll(out, "{publicScheme}", ctx.PublicScheme)
	out = strings.ReplaceAll(out, "{routePublicOrigin}", ctx.PublicOrigin)
	out = strings.ReplaceAll(out, "{routePublicHost}", ctx.PublicHost)
	out = strings.ReplaceAll(out, "{routePublicScheme}", ctx.PublicScheme)
	out = strings.ReplaceAll(out, "{basePublicOrigin}", ctx.BasePublicOrigin)
	out = strings.ReplaceAll(out, "{basePublicHost}", ctx.BasePublicHost)
	out = strings.ReplaceAll(out, "{basePublicScheme}", ctx.BasePublicScheme)
	return out
}

func hostFromOrigin(origin string) string {
	parsed, err := url.Parse(origin)
	if err != nil {
		return ""
	}
	return parsed.Host
}

func schemeFromOrigin(origin string) string {
	parsed, err := url.Parse(origin)
	if err != nil {
		return ""
	}
	return parsed.Scheme
}

func isZeroPublicRewriteConfig(cfg PublicRewriteConfig) bool {
	return len(cfg.Hosts) == 0 && len(cfg.Origins) == 0 && len(cfg.BasePaths) == 0 && len(cfg.Replacements) == 0
}

func rewriteResponseHeaders(headers http.Header, rewriter publicPreviewRewriter, publicOrigin string) {
	for _, name := range []string{"Content-Security-Policy", "Link", "Location", "Refresh"} {
		values := headers.Values(name)
		if len(values) == 0 {
			continue
		}
		headers.Del(name)
		for _, value := range values {
			headers.Add(name, rewriter.rewrite(value, publicOrigin))
		}
	}
}

func shouldRewriteResponseBody(headers http.Header) bool {
	if encoding := headers.Get("Content-Encoding"); encoding != "" && !strings.EqualFold(encoding, "identity") {
		return false
	}
	contentType := strings.ToLower(headers.Get("Content-Type"))
	for _, prefix := range []string{"text/html", "text/css", "text/javascript", "application/javascript", "application/json", "application/manifest+json"} {
		if strings.HasPrefix(contentType, prefix) {
			return true
		}
	}
	return false
}

func isHTMLResponse(headers http.Header) bool {
	return strings.HasPrefix(strings.ToLower(headers.Get("Content-Type")), "text/html")
}

func injectPublicPreviewRuntime(input, publicOrigin, basePublicOrigin string, basePaths []string, nonce string) string {
	publicHost := hostFromOrigin(publicOrigin)
	publicScheme := schemeFromOrigin(publicOrigin)
	if basePublicOrigin == "" {
		basePublicOrigin = publicOrigin
	}
	basePublicHost := hostFromOrigin(basePublicOrigin)
	if input == "" || publicHost == "" || publicScheme != "https" {
		return input
	}
	if strings.Contains(input, "data-vivero-public-preview-runtime") {
		return input
	}
	script := publicPreviewRuntimeScript(publicOrigin, publicHost, basePublicOrigin, basePublicHost, normalizePublicRewriteBasePaths(basePaths), nonce)
	lower := strings.ToLower(input)
	for _, marker := range []string{"</head>", "</body>"} {
		if idx := strings.Index(lower, marker); idx >= 0 {
			return input[:idx] + script + input[idx:]
		}
	}
	return script + input
}

func publicPreviewRuntimeScript(publicOrigin, publicHost, basePublicOrigin, basePublicHost string, basePaths []string, nonce string) string {
	originJSON, _ := json.Marshal(publicOrigin)
	hostJSON, _ := json.Marshal(publicHost)
	baseOriginJSON, _ := json.Marshal(basePublicOrigin)
	baseHostJSON, _ := json.Marshal(basePublicHost)
	basePathsJSON, _ := json.Marshal(normalizePublicRewriteBasePaths(basePaths))
	nonceAttr := ""
	if nonce != "" {
		nonceAttr = ` nonce="` + html.EscapeString(nonce) + `"`
	}
	return "<script data-vivero-public-preview-runtime" + nonceAttr + ">(()=>{" +
		"const basePathPrefixes=" + string(basePathsJSON) + ";" +
		"window.__viveroPublicPreviewRuntime={origin:" + string(originJSON) + ",host:" + string(hostJSON) + ",baseOrigin:" + string(baseOriginJSON) + ",baseHost:" + string(baseHostJSON) + ",basePaths:basePathPrefixes};" +
		"const publicOrigin=" + string(originJSON) + ";" +
		"const publicHost=" + string(hostJSON) + ";" +
		"const basePublicOrigin=" + string(baseOriginJSON) + ";" +
		"const basePublicHost=" + string(baseHostJSON) + ";" +
		"const hostNameOnly=(host)=>(host||\"\").split(\":\")[0];" +
		"const publicHosts=[{origin:publicOrigin,host:publicHost,subdomains:true},{origin:basePublicOrigin,host:basePublicHost,subdomains:false}].filter((item,index,all)=>item.origin&&item.host&&all.findIndex((other)=>other.host===item.host)===index);" +
		"const pathMatchesBase=(path)=>basePathPrefixes.some((prefix)=>path===prefix||path.startsWith(prefix+\"/\"));" +
		"const toBaseIfNeeded=(url)=>{if(!basePublicOrigin||!basePathPrefixes.length||!url||!pathMatchesBase(url.pathname))return null;const hostName=hostNameOnly(publicHost);if(url.origin===publicOrigin||url.host===publicHost||url.hostname===hostName)return basePublicOrigin+url.pathname+url.search+url.hash;return null;};" +
		"const toPublic=(value)=>{if(value==null)return value;const text=String(value);for(const item of publicHosts){const insecureOrigin=\"http://\"+item.host;if(text.startsWith(insecureOrigin))return item.origin+text.slice(insecureOrigin.length);}try{const url=new URL(text,document.baseURI);const baseFixed=toBaseIfNeeded(url);if(baseFixed)return baseFixed;if(url.protocol===\"http:\"){for(const item of publicHosts){const hostName=hostNameOnly(item.host);if(url.host===item.host||url.hostname===hostName||(item.subdomains&&url.hostname.endsWith(\".\"+hostName)))return item.origin+url.pathname+url.search+url.hash;}}}catch{}return value;};" +
		"const attrs=[\"href\",\"src\",\"action\"];" +
		"const valueSelector=\"input,textarea\";" +
		"const selector=attrs.map((attr)=>\"[\"+attr+\"]\").concat(valueSelector).join(\",\");" +
		"const fix=(el)=>{if(!el||el.nodeType!==1)return;for(const attr of attrs){const value=el.getAttribute(attr);const fixed=toPublic(value);if(fixed!==value)el.setAttribute(attr,fixed);}if(\"value\" in el&&typeof el.value===\"string\"){const fixedValue=toPublic(el.value);if(fixedValue!==el.value)el.value=fixedValue;const valueAttr=el.getAttribute&&el.getAttribute(\"value\");const fixedAttr=toPublic(valueAttr);if(fixedAttr!=null&&fixedAttr!==valueAttr)el.setAttribute(\"value\",fixedAttr);}};" +
		"const scan=(root)=>{if(!root)return;if(root.nodeType===1)fix(root);if(root.querySelectorAll)root.querySelectorAll(selector).forEach(fix);};" +
		"const nativeFetch=window.fetch;if(nativeFetch)window.fetch=function(input,init){if(typeof input===\"string\"||input instanceof URL)input=toPublic(input);else if(typeof Request!==\"undefined\"&&input instanceof Request){const fixedURL=toPublic(input.url);if(fixedURL!==input.url)input=new Request(fixedURL,input);}return nativeFetch.call(this,input,init);};" +
		"const nativeXHROpen=window.XMLHttpRequest&&XMLHttpRequest.prototype.open;if(nativeXHROpen)XMLHttpRequest.prototype.open=function(method,url,...rest){return nativeXHROpen.call(this,method,toPublic(url),...rest);};" +
		"const nativeSendBeacon=navigator.sendBeacon&&navigator.sendBeacon.bind(navigator);if(nativeSendBeacon)navigator.sendBeacon=(url,data)=>nativeSendBeacon(toPublic(url),data);" +
		"document.addEventListener(\"click\",(event)=>{const el=event.target&&event.target.closest?event.target.closest(\"a[href]\"):null;if(el)fix(el);},true);" +
		"document.addEventListener(\"submit\",(event)=>{if(event.target)fix(event.target);},true);" +
		"const run=()=>scan(document);" +
		"if(document.readyState===\"loading\")document.addEventListener(\"DOMContentLoaded\",run,{once:true});else run();" +
		"new MutationObserver((mutations)=>{for(const mutation of mutations){if(mutation.type===\"attributes\")fix(mutation.target);else mutation.addedNodes.forEach(scan);}}).observe(document.documentElement,{subtree:true,childList:true,attributes:true,attributeFilter:attrs.concat([\"value\"])});" +
		"})();</script>"
}

func contentSecurityPolicyNonce(headers http.Header) string {
	for _, value := range headers.Values("Content-Security-Policy") {
		matches := scriptNonceRE.FindStringSubmatch(value)
		if len(matches) == 2 {
			return matches[1]
		}
	}
	return ""
}

func rewriteRequestOriginHeaders(headers http.Header, publicOrigin, maskedOrigin string) {
	if publicOrigin == "" || maskedOrigin == "" {
		return
	}
	for _, name := range []string{"Origin", "Referer"} {
		values := headers.Values(name)
		if len(values) == 0 {
			continue
		}
		headers.Del(name)
		for _, value := range values {
			headers.Add(name, rewriteOriginPrefix(value, publicOrigin, maskedOrigin))
		}
	}
}

func rewriteOriginPrefix(value, fromOrigin, toOrigin string) string {
	if value == fromOrigin {
		return toOrigin
	}
	if strings.HasPrefix(value, fromOrigin+"/") {
		return toOrigin + strings.TrimPrefix(value, fromOrigin)
	}
	return value
}

func maskedOriginForIncomingRequest(req *http.Request, hostHeader string) string {
	if hostHeader == "" {
		return ""
	}
	proto := schemeFromOrigin(publicOriginForIncomingRequest(req))
	if proto == "" {
		proto = protoForIncomingRequest(req, hostHeader)
	}
	return proto + "://" + hostHeader
}

func publicOriginForIncomingRequest(req *http.Request) string {
	host := req.Host
	if host == "" && req.URL != nil {
		host = req.URL.Host
	}
	if host == "" {
		host = firstForwardedValue(req.Header.Get("X-Forwarded-Host"))
	}
	if host == "" {
		return ""
	}
	return protoForIncomingRequest(req, host) + "://" + host
}

func protoForIncomingRequest(req *http.Request, host string) string {
	if strings.HasSuffix(hostnameOnly(host), ".trycloudflare.com") {
		return "https"
	}
	proto := firstForwardedValue(req.Header.Get("X-Forwarded-Proto"))
	if proto == "" && req.URL != nil {
		proto = req.URL.Scheme
	}
	if proto == "" {
		if req.TLS != nil {
			proto = "https"
		} else {
			proto = "http"
		}
	}
	return proto
}

func firstForwardedValue(value string) string {
	if value == "" {
		return ""
	}
	return strings.TrimSpace(strings.Split(value, ",")[0])
}

func hostnameOnly(host string) string {
	if host == "" {
		return ""
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		return strings.Trim(h, "[]")
	}
	return strings.Trim(host, "[]")
}

func allocateTCPPortOnHost(host string) (int, error) {
	if strings.TrimSpace(host) == "" {
		host = "127.0.0.1"
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

func (a *App) startHeaderRewriteProxy(previewID, service, runtime, containerID, originURL, hostHeader string, publicRewrite PublicRewriteConfig, routes []publicProxyRoute, h HealthConfig, listenHost string) (string, int, error) {
	return a.startHeaderRewriteProxyAt(previewID, service, runtime, containerID, originURL, hostHeader, publicRewrite, routes, h, listenHost, "", 10*time.Second)
}

func (a *App) startHeaderRewriteProxyAt(previewID, service, runtime, containerID, originURL, hostHeader string, publicRewrite PublicRewriteConfig, routes []publicProxyRoute, h HealthConfig, listenHost, preferredURL string, maxWait time.Duration) (string, int, error) {
	listenHost = strings.TrimSpace(listenHost)
	port := 0
	if preferredURL != "" {
		parsed, parseErr := url.Parse(preferredURL)
		if parseErr == nil {
			port, _ = strconv.Atoi(parsed.Port())
			if listenHost == "" {
				listenHost = parsed.Hostname()
			}
		}
	}
	if listenHost == "" {
		listenHost = "127.0.0.1"
	}
	if port == 0 {
		var err error
		port, err = allocateTCPPortOnHost(listenHost)
		if err != nil {
			return "", 0, err
		}
	}
	listen := net.JoinHostPort(listenHost, fmt.Sprint(port))
	urlHost := listenHost
	if urlHost == "0.0.0.0" || urlHost == "::" {
		urlHost = "127.0.0.1"
	}
	proxyURL := "http://" + net.JoinHostPort(urlHost, fmt.Sprint(port))
	logPath := serviceLogPath(a.Home, previewID, service, ".proxy.log")
	if err := ensureDir(filepath.Dir(logPath)); err != nil {
		return "", 0, err
	}
	lf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return "", 0, err
	}
	exe, err := os.Executable()
	if err != nil {
		lf.Close()
		return "", 0, err
	}
	args := []string{"_proxy", "--listen", listen, "--target", originURL, "--host", hostHeader}
	if !isZeroPublicRewriteConfig(publicRewrite) {
		rewriteJSON, err := json.Marshal(publicRewrite)
		if err != nil {
			lf.Close()
			return "", 0, err
		}
		args = append(args, "--rewrite-json", string(rewriteJSON))
	}
	if len(routes) > 0 {
		routesJSON, err := json.Marshal(routes)
		if err != nil {
			lf.Close()
			return "", 0, err
		}
		args = append(args, "--routes-json", string(routesJSON))
	}
	cmd := exec.Command(exe, args...)
	cmd.Stdout = lf
	cmd.Stderr = lf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		lf.Close()
		return "", 0, err
	}
	go func() { _ = cmd.Wait() }()
	_ = lf.Close()
	pid := cmd.Process.Pid
	proxyTimeout := serviceHealthTimeout(h, maxWait)
	if maxWait > 0 && proxyTimeout > maxWait {
		proxyTimeout = maxWait
	}
	if err := waitHTTPWithCheck(proxyURL, h, proxyTimeout, func() error {
		if !processExists(pid) {
			return fmt.Errorf("header rewrite proxy pid %d exited during startup", pid)
		}
		if err := a.checkServiceRuntime(previewID, service, runtime, containerID); err != nil {
			return err
		}
		return nil
	}); err != nil {
		_ = killProcessGroup(pid)
		return "", pid, fmt.Errorf("header rewrite proxy health failed: %w", err)
	}
	a.recordEvent(previewID, "info", "proxy.started", "header rewrite proxy started", service, map[string]string{"pid": fmt.Sprint(pid), "url": proxyURL, "host": hostHeader, "log": logPath})
	return proxyURL, pid, nil
}

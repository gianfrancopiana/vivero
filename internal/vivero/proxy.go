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
	"strings"
	"syscall"
	"time"
)

type publicOriginContextKey struct{}

var scriptNonceRE = regexp.MustCompile(`'nonce-([^']+)'`)

func runHeaderRewriteProxy(listen, target, hostHeader string, publicRewrite PublicRewriteConfig) error {
	if listen == "" {
		return fmt.Errorf("--listen is required")
	}
	if target == "" {
		return fmt.Errorf("--target is required")
	}
	if hostHeader == "" {
		return fmt.Errorf("--host is required")
	}
	targetURL, err := url.Parse(target)
	if err != nil {
		return err
	}
	server := &http.Server{
		Addr:              listen,
		Handler:           newHeaderRewriteProxy(targetURL, hostHeader, publicRewrite),
		ReadHeaderTimeout: 10 * time.Second,
	}
	return server.ListenAndServe()
}

func newHeaderRewriteProxy(target *url.URL, hostHeader string, publicRewrite PublicRewriteConfig) *httputil.ReverseProxy {
	proxy := httputil.NewSingleHostReverseProxy(target)
	baseDirector := proxy.Director
	rewriter := newPublicPreviewRewriter(target, hostHeader, publicRewrite)
	proxy.Director = func(req *http.Request) {
		publicOrigin := publicOriginForIncomingRequest(req)
		publicScheme := schemeFromOrigin(publicOrigin)
		maskedOrigin := maskedOriginForIncomingRequest(req, hostHeader)
		baseDirector(req)
		if publicOrigin != "" {
			rewriteRequestOriginHeaders(req.Header, publicOrigin, maskedOrigin)
			*req = *req.WithContext(context.WithValue(req.Context(), publicOriginContextKey{}, publicOrigin))
		}
		if publicScheme != "" {
			req.Header.Set("X-Forwarded-Proto", publicScheme)
			if publicScheme == "https" {
				req.Header.Set("X-Forwarded-Port", "443")
			}
		}
		req.Host = hostHeader
		req.Header.Set("X-Forwarded-Host", hostHeader)
		req.Header.Set("X-Forwarded-Server", hostHeader)
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
			rewrittenText = injectPublicPreviewRuntime(rewrittenText, publicOrigin, contentSecurityPolicyNonce(resp.Header))
		}
		rewritten := []byte(rewrittenText)
		resp.Body = io.NopCloser(bytes.NewReader(rewritten))
		resp.ContentLength = int64(len(rewritten))
		resp.Header.Set("Content-Length", fmt.Sprint(len(rewritten)))
		return nil
	}
	return proxy
}

type publicPreviewRewriter struct {
	exactOrigins          []string
	encodedOrigins        []encodedOriginRewrite
	exactHosts            []string
	protocolRelativeHosts []string
	replacements          []PublicRewriteTemplate
	devOriginRE           *regexp.Regexp
}

type encodedOriginRewrite struct {
	From string
	To   string
}

func newPublicPreviewRewriter(target *url.URL, hostHeader string, cfg PublicRewriteConfig) publicPreviewRewriter {
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
	var re *regexp.Regexp
	if hostname != "" {
		re = regexp.MustCompile(`https?://(?:[A-Za-z0-9-]+\.)?` + regexp.QuoteMeta(hostname) + `(?::\d+)?`)
	}
	return publicPreviewRewriter{exactOrigins: exact, encodedOrigins: encodedOrigins, exactHosts: exactHosts, protocolRelativeHosts: protocolRelativeHosts, replacements: cfg.Replacements, devOriginRE: re}
}

func (r publicPreviewRewriter) rewrite(input, publicOrigin string) string {
	if input == "" || publicOrigin == "" {
		return input
	}
	publicHost := hostFromOrigin(publicOrigin)
	publicScheme := schemeFromOrigin(publicOrigin)
	out := input
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
		out = r.devOriginRE.ReplaceAllString(out, publicOrigin)
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
		if replacement.From == "" {
			continue
		}
		out = strings.ReplaceAll(out, replacement.From, expandPublicRewriteTemplate(replacement.To, publicOrigin, publicHost, publicScheme))
	}
	out = normalizePublicHostScheme(out, publicHost, publicScheme)
	return out
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

func expandPublicRewriteTemplate(template, publicOrigin, publicHost, publicScheme string) string {
	out := strings.ReplaceAll(template, "{publicOrigin}", publicOrigin)
	out = strings.ReplaceAll(out, "{publicHost}", publicHost)
	out = strings.ReplaceAll(out, "{publicScheme}", publicScheme)
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
	return len(cfg.Hosts) == 0 && len(cfg.Origins) == 0 && len(cfg.Replacements) == 0
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

func injectPublicPreviewRuntime(input, publicOrigin, nonce string) string {
	publicHost := hostFromOrigin(publicOrigin)
	publicScheme := schemeFromOrigin(publicOrigin)
	if input == "" || publicHost == "" || publicScheme != "https" {
		return input
	}
	if strings.Contains(input, "data-vivero-public-preview-runtime") {
		return input
	}
	script := publicPreviewRuntimeScript(publicOrigin, publicHost, nonce)
	lower := strings.ToLower(input)
	for _, marker := range []string{"</head>", "</body>"} {
		if idx := strings.Index(lower, marker); idx >= 0 {
			return input[:idx] + script + input[idx:]
		}
	}
	return script + input
}

func publicPreviewRuntimeScript(publicOrigin, publicHost, nonce string) string {
	originJSON, _ := json.Marshal(publicOrigin)
	hostJSON, _ := json.Marshal(publicHost)
	nonceAttr := ""
	if nonce != "" {
		nonceAttr = ` nonce="` + html.EscapeString(nonce) + `"`
	}
	return "<script data-vivero-public-preview-runtime" + nonceAttr + ">(()=>{" +
		"window.__viveroPublicPreviewRuntime={origin:" + string(originJSON) + ",host:" + string(hostJSON) + "};" +
		"const publicOrigin=" + string(originJSON) + ";" +
		"const publicHost=" + string(hostJSON) + ";" +
		"const insecureOrigin=\"http://\"+publicHost;" +
		"const toPublic=(value)=>{if(value==null)return value;const text=String(value);if(text.startsWith(insecureOrigin))return publicOrigin+text.slice(insecureOrigin.length);try{const url=new URL(text,document.baseURI);if(url.protocol===\"http:\"&&(url.hostname===publicHost||url.hostname.endsWith(\".\"+publicHost)))return publicOrigin+url.pathname+url.search+url.hash;}catch{}return value;};" +
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

func allocateTCPPort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

func (a *App) startHeaderRewriteProxy(previewID, service, originURL, hostHeader string, publicRewrite PublicRewriteConfig, h HealthConfig) (string, int, error) {
	port, err := allocateTCPPort()
	if err != nil {
		return "", 0, err
	}
	listen := fmt.Sprintf("127.0.0.1:%d", port)
	proxyURL := fmt.Sprintf("http://%s", listen)
	logPath := filepath.Join(a.Home, "logs", previewID, service+".proxy.log")
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
	cmd := exec.Command(exe, args...)
	cmd.Stdout = lf
	cmd.Stderr = lf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		lf.Close()
		return "", 0, err
	}
	_ = lf.Close()
	pid := cmd.Process.Pid
	if err := waitHTTP(proxyURL, h, serviceHealthTimeout(h, 10*time.Second)); err != nil {
		_ = killProcessGroup(pid)
		return "", pid, fmt.Errorf("header rewrite proxy health failed: %w", err)
	}
	a.recordEvent(previewID, "info", "proxy.started", "header rewrite proxy started", service, map[string]string{"pid": fmt.Sprint(pid), "url": proxyURL, "host": hostHeader, "log": logPath})
	return proxyURL, pid, nil
}

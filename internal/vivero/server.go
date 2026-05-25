package vivero

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

func (a *App) Serve(addr string) error {
	if addr == "" {
		addr = "127.0.0.1:7777"
	}
	if err := validateServeAddr(addr); err != nil {
		return err
	}
	srv := &http.Server{Addr: addr, Handler: a.controlPlaneHandler(), ReadHeaderTimeout: 10 * time.Second}
	fmt.Printf("vivero serve listening on http://%s\n", addr)
	return srv.ListenAndServe()
}

func validateServeAddr(addr string) error {
	addr = strings.TrimSpace(addr)
	if addr == "" || strings.HasPrefix(addr, "unix:") {
		return nil
	}
	if os.Getenv("VIVERO_ALLOW_REMOTE_CONTROL") != "" {
		return nil
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil
	}
	host = strings.Trim(strings.ToLower(host), "[]")
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("vivero serve is local-only by default; refusing to bind %q. Use 127.0.0.1 or set VIVERO_ALLOW_REMOTE_CONTROL=1 to expose the unauthenticated control plane deliberately", addr)
}

func (a *App) controlPlaneHandler() http.Handler {
	mux := http.NewServeMux()
	jsonHandler := func(fn func(*http.Request) (any, int, error)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			v, code, err := fn(r)
			if err != nil {
				w.WriteHeader(code)
				writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
				return
			}
			if code == 0 {
				code = 200
			}
			w.WriteHeader(code)
			writeJSON(w, v)
		}
	}
	mux.HandleFunc("GET /capabilities", jsonHandler(func(r *http.Request) (any, int, error) { return a.capabilities(), 200, nil }))
	mux.HandleFunc("GET /commands", jsonHandler(func(r *http.Request) (any, int, error) { return map[string]any{"commands": commandCatalog()}, 200, nil }))
	mux.HandleFunc("GET /schema", jsonHandler(func(r *http.Request) (any, int, error) { return schemaFor(""), 200, nil }))
	mux.HandleFunc("GET /schema/", jsonHandler(func(r *http.Request) (any, int, error) {
		return schemaFor(strings.TrimPrefix(r.URL.Path, "/schema/")), 200, nil
	}))
	mux.HandleFunc("POST /projects/sync", jsonHandler(func(r *http.Request) (any, int, error) {
		var body struct {
			Path string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return nil, 400, err
		}
		rec, err := a.SyncProject(body.Path)
		return map[string]any{"project": rec}, 200, err
	}))
	mux.HandleFunc("GET /projects", jsonHandler(func(r *http.Request) (any, int, error) {
		ps, err := a.listProjects()
		return map[string]any{"projects": ps}, 200, err
	}))
	mux.HandleFunc("GET /projects/", jsonHandler(func(r *http.Request) (any, int, error) {
		name := strings.TrimPrefix(r.URL.Path, "/projects/")
		rec, err := a.getProject(name)
		return map[string]any{"project": rec}, 200, err
	}))
	mux.HandleFunc("POST /previews", jsonHandler(func(r *http.Request) (any, int, error) {
		var body struct {
			Project  string            `json:"project"`
			ID       string            `json:"id"`
			Profile  string            `json:"profile"`
			Sources  map[string]string `json:"sources"`
			Labels   map[string]string `json:"labels"`
			Metadata map[string]string `json:"metadata"`
			Wait     bool              `json:"wait"`
			Timeout  string            `json:"timeout"`
			Public   bool              `json:"public"`
			Reuse    bool              `json:"reuse"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return nil, 400, err
		}
		d, err := durationValue(body.Timeout, "timeout", 5*time.Minute)
		if err != nil {
			return nil, 400, err
		}
		p, err := a.Up(UpRequest{Project: body.Project, ID: body.ID, Profile: body.Profile, Sources: body.Sources, Labels: body.Labels, Metadata: body.Metadata, Wait: body.Wait, Timeout: d, Public: body.Public, Reuse: body.Reuse})
		return map[string]any{"preview": p}, 200, err
	}))
	mux.HandleFunc("GET /previews", jsonHandler(func(r *http.Request) (any, int, error) {
		ps, err := a.listPreviews()
		return map[string]any{"previews": ps}, 200, err
	}))
	mux.HandleFunc("GET /previews/{id}/qa", jsonHandler(func(r *http.Request) (any, int, error) {
		v, err := a.QAPlanWithTarget(r.PathValue("id"), r.URL.Query().Get("scope"), qaTargetFromRequest(r))
		return v, 200, err
	}))
	mux.HandleFunc("POST /previews/{id}/qa/run", jsonHandler(func(r *http.Request) (any, int, error) {
		var body struct {
			Scope         string `json:"scope"`
			Target        string `json:"target,omitempty"`
			Public        bool   `json:"public,omitempty"`
			Origin        bool   `json:"origin,omitempty"`
			Screenshots   *bool  `json:"screenshots,omitempty"`
			NoScreenshots bool   `json:"noScreenshots,omitempty"`
		}
		if err := decodeOptionalJSON(r, &body); err != nil {
			return nil, 400, err
		}
		scope := body.Scope
		if scope == "" {
			scope = r.URL.Query().Get("scope")
		}
		target := qaTargetFromRequest(r)
		if body.Target != "" {
			target = body.Target
		}
		if body.Public {
			target = artifactTargetPublic
		}
		if body.Origin {
			target = artifactTargetOrigin
		}
		screenshots := true
		if body.Screenshots != nil {
			screenshots = *body.Screenshots
		}
		if body.NoScreenshots {
			screenshots = false
		}
		if raw := r.URL.Query().Get("screenshots"); raw != "" {
			parsed, err := strconv.ParseBool(raw)
			if err != nil {
				return nil, 400, fmt.Errorf("screenshots must be a boolean")
			}
			screenshots = parsed
		}
		v, err := a.QARunWithTarget(r.PathValue("id"), scope, target, screenshots)
		return v, 200, err
	}))
	mux.HandleFunc("POST /previews/{id}/qa/record", jsonHandler(func(r *http.Request) (any, int, error) {
		var opts QARecordOptions
		if err := decodeOptionalJSON(r, &opts); err != nil {
			return nil, 400, err
		}
		if opts.Scope == "" {
			opts.Scope = r.URL.Query().Get("scope")
		}
		v, err := a.QARecord(r.PathValue("id"), opts)
		return v, 200, err
	}))
	mux.HandleFunc("POST /previews/{id}/qa/report", jsonHandler(func(r *http.Request) (any, int, error) {
		var body struct {
			Scope  string `json:"scope"`
			Target string `json:"target,omitempty"`
			Public bool   `json:"public,omitempty"`
			Origin bool   `json:"origin,omitempty"`
			Out    string `json:"out"`
		}
		if err := decodeOptionalJSON(r, &body); err != nil {
			return nil, 400, err
		}
		scope := body.Scope
		if scope == "" {
			scope = r.URL.Query().Get("scope")
		}
		target := qaTargetFromRequest(r)
		if body.Target != "" {
			target = body.Target
		}
		if body.Public {
			target = artifactTargetPublic
		}
		if body.Origin {
			target = artifactTargetOrigin
		}
		out := body.Out
		if out == "" {
			out = r.URL.Query().Get("out")
		}
		v, err := a.QAReportWithTarget(r.PathValue("id"), scope, target, out)
		return v, 200, err
	}))
	mux.HandleFunc("GET /previews/", jsonHandler(func(r *http.Request) (any, int, error) {
		id := strings.TrimPrefix(r.URL.Path, "/previews/")
		if strings.HasSuffix(id, "/events") {
			id = strings.TrimSuffix(id, "/events")
			ev, err := a.events(id, 0)
			return map[string]any{"events": ev}, 200, err
		}
		p, err := a.getPreview(id)
		return map[string]any{"preview": p}, 200, err
	}))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.servePublicPreview(w, r) {
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func qaTargetFromRequest(r *http.Request) string {
	q := r.URL.Query()
	target := q.Get("target")
	if public, _ := strconv.ParseBool(q.Get("public")); public {
		target = artifactTargetPublic
	}
	if origin, _ := strconv.ParseBool(q.Get("origin")); origin {
		target = artifactTargetOrigin
	}
	return target
}

func decodeOptionalJSON(r *http.Request, dst any) error {
	if r.Body == nil {
		return nil
	}
	defer r.Body.Close()
	err := json.NewDecoder(r.Body).Decode(dst)
	if err == nil || err == io.EOF {
		return nil
	}
	return err
}

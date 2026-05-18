package vivero

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type App struct {
	Home string
	db   *sql.DB
}

func NewApp() (*App, error) {
	home := defaultHome()
	if err := ensureDir(home); err != nil {
		return nil, err
	}
	for _, d := range []string{"projects", "repos", "worktrees", "logs", "secrets", "patches", "run"} {
		if err := ensureDir(filepath.Join(home, d)); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", filepath.Join(home, "state.db"))
	if err != nil {
		return nil, err
	}
	a := &App{Home: home, db: db}
	if err := a.initDB(); err != nil {
		db.Close()
		return nil, err
	}
	return a, nil
}

func (a *App) Close() error {
	if a.db != nil {
		return a.db.Close()
	}
	return nil
}

func (a *App) initDB() error {
	stmts := []string{
		`PRAGMA journal_mode=WAL;`,
		`CREATE TABLE IF NOT EXISTS projects (name TEXT PRIMARY KEY, path TEXT NOT NULL, config_json TEXT NOT NULL, synced_at TEXT NOT NULL);`,
		`CREATE TABLE IF NOT EXISTS previews (id TEXT PRIMARY KEY, project TEXT NOT NULL, status TEXT NOT NULL, labels_json TEXT, metadata_json TEXT, created_at TEXT NOT NULL, updated_at TEXT NOT NULL);`,
		`CREATE TABLE IF NOT EXISTS preview_sources (preview_id TEXT NOT NULL, name TEXT NOT NULL, mode TEXT NOT NULL, ref TEXT, path TEXT NOT NULL, owned INTEGER NOT NULL, PRIMARY KEY(preview_id, name));`,
		`CREATE TABLE IF NOT EXISTS preview_services (preview_id TEXT NOT NULL, name TEXT NOT NULL, source TEXT, runtime TEXT, container_id TEXT, status TEXT NOT NULL, pid INTEGER, proxy_pid INTEGER, tunnel_pid INTEGER, port INTEGER, url TEXT, origin_url TEXT, proxy_url TEXT, log_path TEXT, tunnel_log_path TEXT, command TEXT, started_at TEXT, last_health TEXT, ports_json TEXT, PRIMARY KEY(preview_id, name));`,
		`CREATE TABLE IF NOT EXISTS preview_events (seq INTEGER PRIMARY KEY AUTOINCREMENT, preview_id TEXT NOT NULL, timestamp TEXT NOT NULL, level TEXT NOT NULL, type TEXT NOT NULL, message TEXT NOT NULL, service_name TEXT, metadata_json TEXT);`,
	}
	for _, s := range stmts {
		if _, err := a.db.Exec(s); err != nil {
			return err
		}
	}
	for _, c := range []struct{ name, def string }{
		{"proxy_pid", "INTEGER"},
		{"proxy_url", "TEXT"},
		{"runtime", "TEXT"},
		{"container_id", "TEXT"},
		{"ports_json", "TEXT"},
	} {
		if _, err := a.db.Exec(fmt.Sprintf(`ALTER TABLE preview_services ADD COLUMN %s %s`, c.name, c.def)); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
			return err
		}
	}
	return nil
}

func (a *App) saveProject(path string, cfg ProjectConfig) (ProjectRecord, error) {
	if cfg.Project.Name == "" {
		return ProjectRecord{}, fmt.Errorf("project.name is required")
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		return ProjectRecord{}, err
	}
	now := nowUTC().Format(time.RFC3339Nano)
	_, err = a.db.Exec(`INSERT INTO projects(name,path,config_json,synced_at) VALUES(?,?,?,?) ON CONFLICT(name) DO UPDATE SET path=excluded.path, config_json=excluded.config_json, synced_at=excluded.synced_at`, cfg.Project.Name, path, string(b), now)
	if err != nil {
		return ProjectRecord{}, err
	}
	return ProjectRecord{Name: cfg.Project.Name, Path: path, Config: cfg, SyncedAt: nowUTC()}, nil
}

func (a *App) getProject(name string) (ProjectRecord, error) {
	var rec ProjectRecord
	var js, synced string
	err := a.db.QueryRow(`SELECT name,path,config_json,synced_at FROM projects WHERE name=?`, name).Scan(&rec.Name, &rec.Path, &js, &synced)
	if err == sql.ErrNoRows {
		return rec, fmt.Errorf("project not found: %s", name)
	}
	if err != nil {
		return rec, err
	}
	if err := json.Unmarshal([]byte(js), &rec.Config); err != nil {
		return rec, err
	}
	rec.SyncedAt, _ = time.Parse(time.RFC3339Nano, synced)
	return rec, nil
}

func (a *App) listProjects() ([]ProjectRecord, error) {
	rows, err := a.db.Query(`SELECT name,path,config_json,synced_at FROM projects ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProjectRecord
	for rows.Next() {
		var rec ProjectRecord
		var js, synced string
		if err := rows.Scan(&rec.Name, &rec.Path, &js, &synced); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(js), &rec.Config)
		rec.SyncedAt, _ = time.Parse(time.RFC3339Nano, synced)
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (a *App) upsertPreview(p PreviewRecord) error {
	if p.CreatedAt.IsZero() {
		p.CreatedAt = nowUTC()
	}
	p.UpdatedAt = nowUTC()
	labels := jsonString(p.Labels)
	meta := jsonString(p.Metadata)
	_, err := a.db.Exec(`INSERT INTO previews(id,project,status,labels_json,metadata_json,created_at,updated_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET project=excluded.project,status=excluded.status,labels_json=excluded.labels_json,metadata_json=excluded.metadata_json,updated_at=excluded.updated_at`, p.ID, p.Project, p.Status, labels, meta, p.CreatedAt.Format(time.RFC3339Nano), p.UpdatedAt.Format(time.RFC3339Nano))
	return err
}

func (a *App) setPreviewStatus(id, status string) error {
	_, err := a.db.Exec(`UPDATE previews SET status=?, updated_at=? WHERE id=?`, status, nowUTC().Format(time.RFC3339Nano), id)
	return err
}

func (a *App) saveSource(previewID string, s PreviewSource) error {
	owned := 0
	if s.Owned {
		owned = 1
	}
	_, err := a.db.Exec(`INSERT INTO preview_sources(preview_id,name,mode,ref,path,owned) VALUES(?,?,?,?,?,?) ON CONFLICT(preview_id,name) DO UPDATE SET mode=excluded.mode,ref=excluded.ref,path=excluded.path,owned=excluded.owned`, previewID, s.Name, s.Mode, s.Ref, s.Path, owned)
	return err
}

func (a *App) saveService(previewID string, s PreviewService) error {
	ports := jsonString(s.Ports)
	_, err := a.db.Exec(`INSERT INTO preview_services(preview_id,name,source,runtime,container_id,status,pid,proxy_pid,tunnel_pid,port,url,origin_url,proxy_url,log_path,tunnel_log_path,command,started_at,last_health,ports_json) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(preview_id,name) DO UPDATE SET source=excluded.source,runtime=excluded.runtime,container_id=excluded.container_id,status=excluded.status,pid=excluded.pid,proxy_pid=excluded.proxy_pid,tunnel_pid=excluded.tunnel_pid,port=excluded.port,url=excluded.url,origin_url=excluded.origin_url,proxy_url=excluded.proxy_url,log_path=excluded.log_path,tunnel_log_path=excluded.tunnel_log_path,command=excluded.command,started_at=excluded.started_at,last_health=excluded.last_health,ports_json=excluded.ports_json`, previewID, s.Name, s.Source, s.Runtime, s.ContainerID, s.Status, s.PID, s.ProxyPID, s.TunnelPID, s.Port, s.URL, s.OriginURL, s.ProxyURL, s.LogPath, s.TunnelLogPath, s.Command, s.StartedAt.Format(time.RFC3339Nano), s.LastHealth, ports)
	return err
}

func (a *App) getPreview(id string) (PreviewRecord, error) {
	var p PreviewRecord
	var labels, meta, created, updated string
	err := a.db.QueryRow(`SELECT id,project,status,labels_json,metadata_json,created_at,updated_at FROM previews WHERE id=?`, id).Scan(&p.ID, &p.Project, &p.Status, &labels, &meta, &created, &updated)
	if err == sql.ErrNoRows {
		return p, fmt.Errorf("preview not found: %s", id)
	}
	if err != nil {
		return p, err
	}
	p.Labels, _ = fromJSONString[map[string]string](labels)
	p.Metadata, _ = fromJSONString[map[string]string](meta)
	p.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	p.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	p.Sources = map[string]PreviewSource{}
	rows, err := a.db.Query(`SELECT name,mode,ref,path,owned FROM preview_sources WHERE preview_id=? ORDER BY name`, id)
	if err != nil {
		return p, err
	}
	for rows.Next() {
		var s PreviewSource
		var owned int
		if err := rows.Scan(&s.Name, &s.Mode, &s.Ref, &s.Path, &owned); err != nil {
			rows.Close()
			return p, err
		}
		s.Owned = owned != 0
		p.Sources[s.Name] = s
	}
	rows.Close()
	p.Services = map[string]PreviewService{}
	rows, err = a.db.Query(`SELECT name,source,COALESCE(runtime,''),COALESCE(container_id,''),status,COALESCE(pid,0),COALESCE(proxy_pid,0),COALESCE(tunnel_pid,0),COALESCE(port,0),COALESCE(url,''),COALESCE(origin_url,''),COALESCE(proxy_url,''),COALESCE(log_path,''),COALESCE(tunnel_log_path,''),COALESCE(command,''),COALESCE(started_at,''),COALESCE(last_health,''),COALESCE(ports_json,'') FROM preview_services WHERE preview_id=? ORDER BY name`, id)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	for rows.Next() {
		var s PreviewService
		var started, ports string
		if err := rows.Scan(&s.Name, &s.Source, &s.Runtime, &s.ContainerID, &s.Status, &s.PID, &s.ProxyPID, &s.TunnelPID, &s.Port, &s.URL, &s.OriginURL, &s.ProxyURL, &s.LogPath, &s.TunnelLogPath, &s.Command, &started, &s.LastHealth, &ports); err != nil {
			return p, err
		}
		s.StartedAt, _ = time.Parse(time.RFC3339Nano, started)
		s.Ports, _ = fromJSONString[map[string]PreviewPort](ports)
		p.Services[s.Name] = s
	}
	return p, rows.Err()
}

func (a *App) listPreviews() ([]PreviewRecord, error) {
	rows, err := a.db.Query(`SELECT id FROM previews ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	var out []PreviewRecord
	for _, id := range ids {
		p, err := a.getPreview(id)
		if err == nil {
			out = append(out, p)
		}
	}
	return out, rows.Err()
}

func (a *App) recordEvent(previewID, level, typ, msg, service string, metadata map[string]string) {
	_, _ = a.db.Exec(`INSERT INTO preview_events(preview_id,timestamp,level,type,message,service_name,metadata_json) VALUES(?,?,?,?,?,?,?)`, previewID, nowUTC().Format(time.RFC3339Nano), level, typ, msg, service, jsonString(metadata))
}

func (a *App) events(previewID string, limit int) ([]Event, error) {
	q := `SELECT seq,preview_id,timestamp,level,type,message,service_name,metadata_json FROM preview_events WHERE preview_id=? ORDER BY seq ASC`
	args := []any{previewID}
	if limit > 0 {
		q = `SELECT seq,preview_id,timestamp,level,type,message,service_name,metadata_json FROM preview_events WHERE preview_id=? ORDER BY seq DESC LIMIT ?`
		args = append(args, limit)
	}
	rows, err := a.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		var ts, meta string
		if err := rows.Scan(&e.Seq, &e.PreviewID, &ts, &e.Level, &e.Type, &e.Message, &e.Service, &meta); err != nil {
			return nil, err
		}
		e.Timestamp, _ = time.Parse(time.RFC3339Nano, ts)
		e.Metadata, _ = fromJSONString[map[string]string](meta)
		out = append(out, e)
	}
	if limit > 0 {
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
	}
	return out, rows.Err()
}

func (a *App) secretFile(project string) string {
	return filepath.Join(a.Home, "secrets", project+".env")
}

func readEnvFile(path string) (map[string]string, error) {
	m := map[string]string{}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			m[k] = v
		}
	}
	return m, nil
}

func writeEnvFile(path string, m map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var b strings.Builder
	for _, k := range sortedMapKeys(m) {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(m[k])
		b.WriteByte('\n')
	}
	return os.WriteFile(path, []byte(b.String()), 0o600)
}

package ui_web

import (
	"compress/gzip"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"meshvianex/core/plugin"
	"meshvianex/core/resources"
)

type Config struct {
	Listen      string `json:"listen"`
	Title       string `json:"title,omitempty"`
	AllowRemote bool   `json:"allow_remote,omitempty"`
}

type SnapshotProvider interface{ Snapshot() any }

// ControlProvider keeps the UI host-neutral. The host validates action names
// and payloads before changing runtime state.
type ControlProvider interface {
	SnapshotProvider
	Control(context.Context, string, string, json.RawMessage) (any, error)
}

type Factory struct{}

func (Factory) Manifest() plugin.Manifest {
	return plugin.Manifest{ID: "io.meshvianex.ui.web", Name: "Meshvianex Web UI", Version: "0.2.0", PluginTypes: []string{"ui_provider"}, RequiresCapabilities: []string{"ui.schema", "event.subscribe"}, Provides: []string{"ui.web", "ui.control"}, ABI: 1}
}

func (Factory) Create(_ context.Context, ictx plugin.InstanceContext, b json.RawMessage) (plugin.Instance, error) {
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	if c.Listen == "" {
		c.Listen = "127.0.0.1:8787"
	}
	if c.Title == "" {
		c.Title = "Meshvianex"
	}
	if !c.AllowRemote && !isLoopbackListen(c.Listen) {
		return nil, fmt.Errorf("refusing non-loopback UI listen without allow_remote=true")
	}
	var provider SnapshotProvider
	if v, ok := ictx.Services["ui.snapshot"]; ok {
		provider, _ = v.(SnapshotProvider)
	}
	var control ControlProvider
	if v, ok := ictx.Services["ui.control"]; ok {
		control, _ = v.(ControlProvider)
	}
	return &Instance{cfg: c, provider: provider, control: control}, nil
}

type Instance struct {
	cfg      Config
	provider SnapshotProvider
	control  ControlProvider
	srv      *http.Server
	ln       net.Listener
	wg       sync.WaitGroup
}

func (i *Instance) URL() string {
	if i.ln != nil {
		return "http://" + i.ln.Addr().String()
	}
	return "http://" + i.cfg.Listen
}

func (i *Instance) Title() string { return i.cfg.Title }

func (i *Instance) Start(context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", i.index)
	mux.Handle("/assets/", http.StripPrefix("/", http.FileServer(http.FS(uiAssets))))
	mux.HandleFunc("/api/status", i.status)
	mux.HandleFunc("/api/schema", i.schema)
	mux.HandleFunc("/api/realms", i.collection)
	mux.HandleFunc("/api/plugins", i.collection)
	mux.HandleFunc("/api/graph", i.collection)
	mux.HandleFunc("/api/connections", i.collection)
	mux.HandleFunc("/api/action", i.action)
	mux.HandleFunc("/robots.txt", i.robots)
	i.srv = &http.Server{Handler: gzipHandler(mux), ReadHeaderTimeout: 5 * time.Second}
	ln, err := net.Listen("tcp", i.cfg.Listen)
	if err != nil {
		return err
	}
	i.ln = ln
	i.wg.Add(1)
	go func() { defer i.wg.Done(); _ = i.srv.Serve(ln) }()
	return nil
}

func (i *Instance) Stop(ctx context.Context) error {
	if i.srv == nil {
		return nil
	}
	err := i.srv.Shutdown(ctx)
	i.wg.Wait()
	return err
}

func (i *Instance) status(w http.ResponseWriter, _ *http.Request) {
	if i.provider == nil {
		writeJSON(w, map[string]any{"status": "running"})
		return
	}
	writeJSON(w, i.provider.Snapshot())
}

func (i *Instance) schema(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{"page": "realm_graph", "title": i.cfg.Title, "fields": []any{
		map[string]any{"id": "realms", "type": "realm_collection", "label": "Realms"},
		map[string]any{"id": "plugins", "type": "plugin_collection", "label": "Plugins"},
		map[string]any{"id": "graph", "type": "graph_editor", "label": "Realm chain"},
	}})
}

func (i *Instance) collection(w http.ResponseWriter, r *http.Request) {
	if i.control == nil {
		writeError(w, http.StatusNotImplemented, "control provider is not configured")
		return
	}
	action := map[string]string{"/api/realms": "list_realms", "/api/plugins": "list_plugins", "/api/graph": "get_graph", "/api/connections": "list_connections"}[r.URL.Path]
	result, err := i.control.Control(r.Context(), action, "", nil)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, result)
}

func (i *Instance) action(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	if i.control == nil {
		writeError(w, http.StatusNotImplemented, "control provider is not configured")
		return
	}
	var req struct {
		Action  string          `json:"action"`
		ID      string          `json:"id"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	result, err := i.control.Control(r.Context(), req.Action, req.ID, req.Payload)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, result)
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": resources.RedactString(message)})
}

func (i *Instance) index(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = page.Execute(w, map[string]string{"Title": template.HTMLEscapeString(i.cfg.Title)})
}

func (i *Instance) robots(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("User-agent: *\nAllow: /\nDisallow: /api/\n"))
}

type gzipResponseWriter struct {
	http.ResponseWriter
	writer *gzip.Writer
}

func (w *gzipResponseWriter) Write(data []byte) (int, error) {
	return w.writer.Write(data)
}

func gzipHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(strings.ToLower(r.Header.Get("Accept-Encoding")), "gzip") {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Add("Vary", "Accept-Encoding")
		w.Header().Del("Content-Length")
		writer := gzip.NewWriter(w)
		defer writer.Close()
		next.ServeHTTP(&gzipResponseWriter{ResponseWriter: w, writer: writer}, r)
	})
}

//go:embed index.html
var pageHTML string

//go:embed assets/*.svg assets/*.png assets/*.css assets/*.js
var uiAssets embed.FS

var page = template.Must(template.New("index").Parse(pageHTML))

func isLoopbackListen(addr string) bool {
	h, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(strings.Trim(h, "[]"))
	return ip != nil && ip.IsLoopback()
}

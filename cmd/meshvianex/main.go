package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"meshvianex/core/event"
	"meshvianex/core/graph"
	"meshvianex/core/health"
	"meshvianex/core/netx"
	"meshvianex/core/platform"
	"meshvianex/core/plugin"
	"meshvianex/core/realm"
	"meshvianex/core/resources"
	"meshvianex/plugins/config_parser"
	"meshvianex/plugins/config_selector"
	"meshvianex/plugins/direct"
	"meshvianex/plugins/dns_system"
	"meshvianex/plugins/health_probe"
	"meshvianex/plugins/http_inbound"
	"meshvianex/plugins/http_upstream"
	"meshvianex/plugins/raw_protocol"
	"meshvianex/plugins/rules_basic"
	"meshvianex/plugins/socks5_inbound"
	"meshvianex/plugins/socks5_outbound"
	"meshvianex/plugins/subscription_basic"
	"meshvianex/plugins/tcp_transport"
	"meshvianex/plugins/transform_basic"
	"meshvianex/plugins/ui_web"
	"meshvianex/plugins/xray_outbound"
)

const appVersion = "v0.2.0-alpha"

type AppConfig struct {
	Realm realm.Config `json:"realm"`
	UI    struct {
		Enabled bool            `json:"enabled"`
		Config  json.RawMessage `json:"config"`
	} `json:"ui"`
	Graph    *graph.Config `json:"graph,omitempty"`
	StateDir string        `json:"state_dir,omitempty"`
}

func main() {
	if os.Getenv(xray_outbound.WorkerModeEnv) == "1" {
		if err := xray_outbound.RunWorker(os.Stdin, os.Stdout); err != nil {
			_, _ = fmt.Fprintf(os.Stdout, "ERROR: %v\n", err)
			log.Print(err)
			os.Exit(1)
		}
		return
	}
	configPath := flag.String("config", "examples/direct.json", "configuration JSON")
	showVersion := flag.Bool("version", false, "print application version and exit")
	validateOnly := flag.Bool("validate", false, "validate configuration and exit")
	traceNode := flag.String("trace", "", "trace graph from node and exit")
	purpose := flag.String("purpose", "user_traffic", "purpose used with -trace")
	stateDir := flag.String("state-dir", "", "persistent subscriptions/configs state directory")
	flag.Parse()
	if *showVersion {
		fmt.Println(appVersion)
		return
	}
	raw, err := os.ReadFile(*configPath)
	if err != nil {
		log.Fatal(err)
	}
	var cfg AppConfig
	if err = json.Unmarshal(raw, &cfg); err != nil {
		log.Fatal(err)
	}
	reg := registry()
	bus := event.NewBus()
	hs := health.NewStore()
	if *stateDir == "" {
		*stateDir = cfg.StateDir
	}
	if *stateDir == "" {
		*stateDir = os.Getenv("MESHVIANEX_STATE_DIR")
	}
	if *stateDir == "" {
		if dir, dirErr := os.UserConfigDir(); dirErr == nil {
			*stateDir = filepath.Join(dir, "meshvianex")
		} else {
			*stateDir = ".meshvianex-state"
		}
	}
	resourceStore := resources.NewStore(*stateDir)
	realmConfigs, graphCfg, err := loadApplicationState(*stateDir, cfg.Realm, cfg.Graph)
	if err != nil {
		log.Fatalf("load persisted state: %v", err)
	}
	eng := graph.New(graphCfg, hs)
	if err := eng.Validate(); err != nil {
		log.Fatalf("graph validation: %v", err)
	}
	if *traceNode != "" {
		hops, err := eng.Trace(*traceNode, *purpose)
		if err != nil {
			log.Fatal(err)
		}
		b, _ := json.MarshalIndent(hops, "", "  ")
		fmt.Println(string(b))
		return
	}
	initial := cfg.Realm
	if len(realmConfigs) > 0 {
		initial = realmConfigs[0]
	}
	control, err := newControlPlaneWithStore(reg, hs, bus, &graphCfg, initial, resourceStore)
	if err != nil {
		log.Fatalf("control plane: %v", err)
	}
	runtimes := make([]*realm.Runtime, 0, len(realmConfigs))
	for _, realmCfg := range realmConfigs {
		rt := realm.NewRuntime(realmCfg, reg, bus)
		rt.SetReferenceResolver(resourceStore)
		if err := rt.ValidateConfig(realmCfg); err != nil {
			log.Fatalf("realm validation (%s): %v", realmCfg.ID, err)
		}
		control.register(rt)
		runtimes = append(runtimes, rt)
	}
	if *validateOnly {
		fmt.Printf("configuration valid: realms=%d plugins=%d\n", len(runtimes), len(reg.List()))
		return
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	control.startRefreshScheduler(ctx)
	defer control.stopRefreshScheduler()
	events, unsubscribe := bus.Subscribe(64)
	defer unsubscribe()
	go func() {
		for e := range events {
			b, _ := json.Marshal(e)
			log.Printf("event %s", b)
		}
	}()
	for _, rt := range runtimes {
		if err := rt.Start(ctx); err != nil {
			log.Fatal(err)
		}
	}
	defer func() {
		for i := len(runtimes) - 1; i >= 0; i-- {
			_ = runtimes[i].Stop(context.Background())
		}
	}()
	{
		listen := strings.TrimSpace(os.Getenv("MESHVIANEX_HTTP_PROXY_LISTEN"))
		if listen == "" {
			listen = "127.0.0.1:0"
		}
		proxy, address, err := startGlobalHTTPProxy(ctx, reg, control, listen)
		if err != nil {
			log.Fatalf("global HTTP proxy: %v", err)
		}
		defer func() {
			stopCtx, stopCancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer stopCancel()
			_ = proxy.Stop(stopCtx)
		}()
		log.Printf("Meshvianex HTTP proxy: http://%s", address)
	}
	var uiInst plugin.Instance
	if cfg.UI.Enabled {
		inst, err := reg.Create(ctx, "io.meshvianex.ui.web", plugin.InstanceContext{InstanceID: "ui-main", Purpose: "application_ui", Services: map[string]any{"ui.snapshot": control, "ui.control": control}}, cfg.UI.Config)
		if err != nil {
			log.Fatal(err)
		}
		if err := inst.Start(ctx); err != nil {
			log.Fatal(err)
		}
		uiInst = inst
		if ui_web.WindowSupported() && os.Getenv("MESHVIANEX_UI_LAUNCHER") != "bat" {
			if ui, ok := inst.(*ui_web.Instance); ok {
				go func() {
					if err := ui_web.OpenWindow(ctx, ui.URL(), ui.Title()); err != nil {
						log.Printf("UI window unavailable: %v; open the dashboard in a browser instead", err)
						return
					}
					cancel()
				}()
			}
		}
		defer func() {
			c, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = uiInst.Stop(c)
		}()
	}
	log.Printf("Meshvianex running; realms=%d", len(runtimes))
	<-ctx.Done()
}

// startGlobalHTTPProxy connects the application-owned HTTP inbound to the live
// Local Source route. A fixed Realm dialer would become stale whenever the
// user edits the Graph or creates/deletes Realms while the application runs.
func startGlobalHTTPProxy(ctx context.Context, reg *plugin.Registry, control *controlPlane, listen string) (plugin.Instance, string, error) {
	if control == nil {
		return nil, "", fmt.Errorf("global HTTP proxy requires a control plane")
	}
	if strings.TrimSpace(listen) == "" {
		listen = "127.0.0.1:0"
	}
	proxyConfig, err := json.Marshal(http_inbound.Config{Listen: listen})
	if err != nil {
		return nil, "", err
	}
	// Resolve the active Graph *for every new connection*. Do not silently
	// fall back to an arbitrary Realm if Local Source is disconnected.
	dialer := netx.DialerFunc(func(dialCtx context.Context, flow netx.Flow) (net.Conn, error) {
		engine := control.graph.Engine()
		if engine == nil {
			return nil, fmt.Errorf("global HTTP proxy has no active graph")
		}
		cfg := engine.Config()
		var sourceID string
		for _, node := range cfg.Nodes {
			if node.Type != "source" {
				continue
			}
			if sourceID == "" || node.ID == "source" {
				sourceID = node.ID
			}
			if node.ID == "source" {
				break
			}
		}
		if sourceID == "" {
			return nil, fmt.Errorf("global HTTP proxy graph has no Local Source")
		}
		flow.RealmID = sourceID
		if flow.Purpose == "" {
			flow.Purpose = "user_traffic"
		}
		hops, err := engine.TraceFlow(flow)
		if err != nil {
			return nil, fmt.Errorf("global HTTP proxy Local Source route: %w", err)
		}
		nodes := make(map[string]graph.Node, len(cfg.Nodes))
		for _, node := range cfg.Nodes {
			nodes[node.ID] = node
		}
		for _, hop := range hops {
			node := nodes[hop.To]
			if node.Type != "realm" {
				continue
			}
			if node.RealmID == "" {
				return nil, fmt.Errorf("global HTTP proxy route contains a Realm without realm_id")
			}
			control.mu.RLock()
			rt := control.realms[node.RealmID]
			control.mu.RUnlock()
			if rt == nil {
				return nil, fmt.Errorf("global HTTP proxy routes to unavailable Realm %s", node.RealmID)
			}
			flow.RealmID = node.RealmID
			flow.InputPort = hop.ToPort
			flow.OutputPort = ""
			return rt.DialContext(dialCtx, flow)
		}
		return nil, fmt.Errorf("global HTTP proxy Local Source has no user_traffic Realm route")
	})
	proxy, err := reg.Create(ctx, "io.meshvianex.inbound.http", plugin.InstanceContext{
		InstanceID: "http-global",
		Purpose:    "application_inbound",
		Services:   map[string]any{"realm.dialer": dialer},
	}, proxyConfig)
	if err != nil {
		return nil, "", err
	}
	if err := proxy.Start(ctx); err != nil {
		return nil, "", err
	}
	address, ok := proxy.(interface{ ListenAddress() string })
	if !ok || address.ListenAddress() == "" {
		_ = proxy.Stop(context.Background())
		return nil, "", fmt.Errorf("global HTTP proxy did not expose its listening address")
	}
	return proxy, address.ListenAddress(), nil
}

func registry() *plugin.Registry {
	grants := plugin.CapabilitySet{"socket.tcp": true, "socket.listen": true, "stream.read": true, "stream.write": true, "dns.resolve": true, "config.read": true, "process.spawn": true, "ui.schema": true, "ui.events": true, "event.subscribe": true}
	p := platform.Detect()
	r := plugin.NewRegistryWithHost(grants, plugin.HostInfo{OS: p.OS, Architecture: p.Architecture, APIs: map[string]int{"plugin": 1, "lifecycle": 1, "config": 1, "stream": 1, "dns": 1, "socket": 1}}, plugin.ResourceLimits{})
	factories := []plugin.Factory{direct.Factory{}, dns_system.Factory{}, rules_basic.Factory{}, raw_protocol.Factory{}, tcp_transport.Factory{}, transform_basic.Factory{}, http_inbound.Factory{}, http_upstream.Factory{}, socks5_inbound.Factory{}, socks5_outbound.Factory{}, xray_outbound.Factory{}, subscription_basic.Factory{}, config_parser.Factory{}, config_selector.Factory{}, health_probe.Factory{}, ui_web.Factory{}}
	for _, f := range factories {
		if err := r.Register(f); err != nil {
			panic(err)
		}
	}
	return r
}

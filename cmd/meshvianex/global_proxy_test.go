package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"meshvianex/core/event"
	"meshvianex/core/graph"
	"meshvianex/core/health"
	"meshvianex/core/netx"
	"meshvianex/core/plugin"
	"meshvianex/core/realm"
	"meshvianex/core/resources"
)

type globalProxyDialFactory struct {
	dials chan<- string
}

type globalProxyDialer struct {
	realmID string
	dials   chan<- string
}

func (f globalProxyDialFactory) Manifest() plugin.Manifest {
	return plugin.Manifest{ID: "test.global_proxy_dialer", Name: "Global proxy test dialer", Version: "1", ABI: 1, PluginTypes: []string{"outbound"}, RequiresCapabilities: []string{"socket.tcp"}}
}

func (f globalProxyDialFactory) Create(_ context.Context, ictx plugin.InstanceContext, _ json.RawMessage) (plugin.Instance, error) {
	return &globalProxyDialer{realmID: ictx.RealmID, dials: f.dials}, nil
}

func (*globalProxyDialer) Start(context.Context) error { return nil }
func (*globalProxyDialer) Stop(context.Context) error  { return nil }

func (d *globalProxyDialer) DialContext(ctx context.Context, flow netx.Flow) (net.Conn, error) {
	d.dials <- d.realmID
	return (&net.Dialer{}).DialContext(ctx, flow.Network, flow.Address)
}

func TestGlobalHTTPProxyConnectsThroughRealmChain(t *testing.T) {
	dials := make(chan string, 4)
	reg := registry()
	if err := reg.Register(globalProxyDialFactory{dials: dials}); err != nil {
		t.Fatal(err)
	}

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "served through leaf Realm")
	}))
	defer target.Close()

	root := realm.Config{ID: "root", Name: "Root", Outbounds: []realm.ComponentRef{{InstanceID: "root-out", Plugin: "test.global_proxy_dialer", Enabled: true}}, DefaultOutbound: "root-out"}
	leaf := realm.Config{ID: "leaf", Name: "Leaf", Outbounds: []realm.ComponentRef{{InstanceID: "leaf-out", Plugin: "test.global_proxy_dialer", Enabled: true}}, DefaultOutbound: "leaf-out"}
	configs := map[string]realm.Config{"root": root, "leaf": leaf}
	graphCfg := chainGraphForRealms([]string{"root", "leaf"}, configs, "test chain", 1)
	bus := event.NewBus()
	control, err := newControlPlaneWithStore(reg, health.NewStore(), bus, &graphCfg, root, resources.NewStore(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}

	runtimes := make([]*realm.Runtime, 0, 2)
	for _, cfg := range []realm.Config{root, leaf} {
		rt := realm.NewRuntime(cfg, reg, bus)
		control.register(rt)
		if err := rt.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		runtimes = append(runtimes, rt)
	}
	defer func() {
		for i := len(runtimes) - 1; i >= 0; i-- {
			_ = runtimes[i].Stop(context.Background())
		}
	}()

	proxy, address, err := startGlobalHTTPProxy(context.Background(), reg, control, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Stop(context.Background())
	if address == "" || strings.HasSuffix(address, ":0") {
		t.Fatalf("global proxy did not bind to a concrete address: %q", address)
	}

	proxyURL := fmt.Sprintf("http://%s", address)
	parsedProxy, err := url.Parse(proxyURL)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(parsedProxy)}}
	response, err := client.Get(target.URL)
	if err != nil {
		t.Fatalf("request through Meshvianex HTTP proxy failed: %v", err)
	}
	body, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if response.StatusCode != http.StatusOK || string(body) != "served through leaf Realm" {
		t.Fatalf("unexpected proxied response: status=%s body=%q", response.Status, body)
	}

	tlsTarget := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "TLS tunneled through leaf Realm")
	}))
	defer tlsTarget.Close()
	tlsClient := tlsTarget.Client()
	tlsClient.Transport.(*http.Transport).Proxy = http.ProxyURL(parsedProxy)
	tlsResponse, err := tlsClient.Get(tlsTarget.URL)
	if err != nil {
		t.Fatalf("HTTPS request through Meshvianex HTTP proxy failed: %v", err)
	}
	tlsBody, readErr := io.ReadAll(tlsResponse.Body)
	_ = tlsResponse.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if tlsResponse.StatusCode != http.StatusOK || string(tlsBody) != "TLS tunneled through leaf Realm" {
		t.Fatalf("unexpected tunneled response: status=%s body=%q", tlsResponse.Status, tlsBody)
	}

	for i := 0; i < 2; i++ {
		select {
		case realmID := <-dials:
			if realmID != "leaf" {
				t.Fatalf("global source bypassed the Realm chain and dialed through %q", realmID)
			}
		default:
			t.Fatal("proxied request did not reach the leaf Realm outbound")
		}
	}
}

func TestGlobalHTTPProxyUsesLocalSourceRealm(t *testing.T) {
	dials := make(chan string, 2)
	reg := registry()
	if err := reg.Register(globalProxyDialFactory{dials: dials}); err != nil {
		t.Fatal(err)
	}

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "served through connected Realm")
	}))
	defer target.Close()

	other := realm.Config{ID: "other", Name: "Other", Outbounds: []realm.ComponentRef{{InstanceID: "other-out", Plugin: "test.global_proxy_dialer", Enabled: true}}, DefaultOutbound: "other-out"}
	connected := realm.Config{ID: "connected", Name: "Connected", Outbounds: []realm.ComponentRef{{InstanceID: "connected-out", Plugin: "test.global_proxy_dialer", Enabled: true}}, DefaultOutbound: "connected-out"}
	configs := map[string]realm.Config{"connected": connected}
	graphCfg := chainGraphForRealms([]string{"connected"}, configs, "Local source route", 1)
	graphCfg.Nodes = append(graphCfg.Nodes, graph.Node{ID: "realm-other", Type: "realm", RealmID: "other", Ports: []graph.Port{
		{Name: "in", Type: "flow", Direction: "input"},
		{Name: "out", Type: "flow", Direction: "output"},
	}})
	graphCfg.Edges = append([]graph.Edge{{
		ID:   "source-other-dns",
		From: graph.Endpoint{Node: "source", Port: "out"},
		To:   graph.Endpoint{Node: "realm-other", Port: "in"},
		Type: "direct",
		When: graph.Condition{Purpose: "dns"},
	}}, graphCfg.Edges...)
	bus := event.NewBus()
	control, err := newControlPlaneWithStore(reg, health.NewStore(), bus, &graphCfg, other, resources.NewStore(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}

	runtimes := make([]*realm.Runtime, 0, 2)
	for _, cfg := range []realm.Config{other, connected} {
		rt := realm.NewRuntime(cfg, reg, bus)
		control.register(rt)
		if err := rt.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		runtimes = append(runtimes, rt)
	}
	defer func() {
		for i := len(runtimes) - 1; i >= 0; i-- {
			_ = runtimes[i].Stop(context.Background())
		}
	}()

	proxy, address, err := startGlobalHTTPProxy(context.Background(), reg, control, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Stop(context.Background())

	proxyURL, err := url.Parse(fmt.Sprintf("http://%s", address))
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	response, err := client.Get(target.URL)
	if err != nil {
		t.Fatalf("request through Meshvianex HTTP proxy failed: %v", err)
	}
	body, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if response.StatusCode != http.StatusOK || string(body) != "served through connected Realm" {
		t.Fatalf("unexpected proxied response: status=%s body=%q", response.Status, body)
	}
	select {
	case realmID := <-dials:
		if realmID != "connected" {
			t.Fatalf("global proxy ignored the Local source Realm and dialed through %q", realmID)
		}
	default:
		t.Fatal("proxied request did not reach the Local source Realm outbound")
	}
}

// The application proxy is created once at startup, but the Local Source is
// editable from the UI. Existing proxy addresses must follow these edits for
// both plain HTTP and CONNECT, without restarting the inbound listener.
func TestGlobalHTTPProxyFollowsLiveLocalSourceGraph(t *testing.T) {
	dials := make(chan string, 12)
	reg := registry()
	if err := reg.Register(globalProxyDialFactory{dials: dials}); err != nil {
		t.Fatal(err)
	}
	left := realm.Config{ID: "left", Name: "Left", Outbounds: []realm.ComponentRef{{InstanceID: "left-out", Plugin: "test.global_proxy_dialer", Enabled: true}}, DefaultOutbound: "left-out"}
	right := realm.Config{ID: "right", Name: "Right", Outbounds: []realm.ComponentRef{{InstanceID: "right-out", Plugin: "test.global_proxy_dialer", Enabled: true}}, DefaultOutbound: "right-out"}
	configs := map[string]realm.Config{"left": left, "right": right}
	graphCfg := chainGraphForRealms([]string{"left"}, configs, "Local Source initial route", 1)
	bus := event.NewBus()
	control, err := newControlPlaneWithStore(reg, health.NewStore(), bus, &graphCfg, left, resources.NewStore(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	for _, cfg := range []realm.Config{left, right} {
		rt := realm.NewRuntime(cfg, reg, bus)
		control.register(rt)
		if err := rt.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		defer rt.Stop(context.Background())
	}
	proxy, addr, err := startGlobalHTTPProxy(context.Background(), reg, control, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Stop(context.Background())
	proxyURL, err := url.Parse("http://" + addr)
	if err != nil {
		t.Fatal(err)
	}
	httpTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer httpTarget.Close()
	tlsTarget := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "secure")
	}))
	defer tlsTarget.Close()

	request := func(https bool, expectedStatus int, expectedRealm string) {
		t.Helper()
		var client *http.Client
		var target string
		if https {
			client = tlsTarget.Client()
			client.Transport.(*http.Transport).Proxy = http.ProxyURL(proxyURL)
			target = tlsTarget.URL
		} else {
			client = &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
			target = httpTarget.URL
		}
		defer client.CloseIdleConnections()
		resp, err := client.Get(target)
		if https && expectedStatus == http.StatusBadGateway {
			// Go reports a non-2xx CONNECT handshake as a client error,
			// unlike the ordinary HTTP proxy response.
			if err == nil || !strings.Contains(err.Error(), "Bad Gateway") {
				t.Fatalf("expected failed CONNECT with Bad Gateway, got response=%v err=%v", resp, err)
			}
			return
		}
		if err != nil {
			t.Fatalf("proxy request to %s: %v", target, err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != expectedStatus {
			t.Fatalf("proxy status %d, want %d", resp.StatusCode, expectedStatus)
		}
		if expectedRealm != "" {
			select {
			case got := <-dials:
				if got != expectedRealm {
					t.Fatalf("Local Source routed to %s, want %s", got, expectedRealm)
				}
			default:
				t.Fatal("HTTP proxy bypassed selected Realm's outbound")
			}
		}
	}

	request(false, http.StatusOK, "left")
	request(true, http.StatusOK, "left")

	// Change the Source -> Realm edge without restarting the global proxy.
	rightGraph := chainGraphForRealms([]string{"right"}, configs, "Local Source moved to right", 2)
	if _, err := control.Control(context.Background(), "set_graph", "", mustJSON(rightGraph)); err != nil {
		t.Fatal(err)
	}
	request(false, http.StatusOK, "right")
	request(true, http.StatusOK, "right")

	// A disconnected Source must not silently use the first available Realm.
	disconnected := chainGraphForRealms(nil, configs, "Local Source disconnected", 3)
	if _, err := control.Control(context.Background(), "set_graph", "", mustJSON(disconnected)); err != nil {
		t.Fatal(err)
	}
	request(false, http.StatusBadGateway, "")
	request(true, http.StatusBadGateway, "")
	select {
	case unexpected := <-dials:
		t.Fatalf("disconnected Local Source unexpectedly used Realm %s", unexpected)
	default:
	}

	if _, err := control.Control(context.Background(), "set_graph", "", mustJSON(graphCfg)); err != nil {
		t.Fatal(err)
	}
	request(false, http.StatusOK, "left")
}

// The proxy listener can exist before any Realm is created; adding a Realm
// later must make the same listener functional without restarting Meshvianex.
func TestGlobalHTTPProxyRecoversAfterRealmIsCreated(t *testing.T) {
	dials := make(chan string, 2)
	reg := registry()
	if err := reg.Register(globalProxyDialFactory{dials: dials}); err != nil {
		t.Fatal(err)
	}
	empty := chainGraphForRealms(nil, nil, "empty", 1)
	bus := event.NewBus()
	control, err := newControlPlaneWithStore(reg, health.NewStore(), bus, &empty, realm.Config{}, resources.NewStore(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	proxy, addr, err := startGlobalHTTPProxy(context.Background(), reg, control, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Stop(context.Background())
	proxyURL, _ := url.Parse("http://" + addr)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer target.Close()
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	defer client.CloseIdleConnections()
	response, err := client.Get(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusBadGateway {
		t.Fatalf("unrouted proxy returned %v", response.Status)
	}

	added := realm.Config{ID: "new", Name: "New", Outbounds: []realm.ComponentRef{{InstanceID: "new-out", Plugin: "test.global_proxy_dialer", Enabled: true}}, DefaultOutbound: "new-out"}
	if _, err := control.Control(context.Background(), "create_realm", "", mustJSON(added)); err != nil {
		t.Fatal(err)
	}
	if _, err := control.Control(context.Background(), "start_realm", added.ID, nil); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = control.Control(context.Background(), "stop_realm", added.ID, nil) }()
	response, err = client.Get(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("proxy did not adopt newly-created Realm: %v", response.Status)
	}
	select {
	case got := <-dials:
		if got != added.ID {
			t.Fatalf("newly connected Local Source used %s, want %s", got, added.ID)
		}
	default:
		t.Fatal("no connection made through added Realm")
	}
}

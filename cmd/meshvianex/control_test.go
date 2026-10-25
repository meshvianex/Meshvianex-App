package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"meshvianex/core/event"
	"meshvianex/core/graph"
	"meshvianex/core/health"
	"meshvianex/core/netx"
	"meshvianex/core/plugin"
	"meshvianex/core/realm"
	"meshvianex/core/resources"
	"strings"
	"testing"
)

type lifecycleTestState struct {
	started         bool
	dialBeforeStart bool
}

type lifecycleTestFactory struct{ state *lifecycleTestState }
type lifecycleTestInstance struct{ state *lifecycleTestState }

func (f lifecycleTestFactory) Manifest() plugin.Manifest {
	return plugin.Manifest{ID: "test.lifecycle.outbound", Name: "Lifecycle test outbound", Version: "1", ABI: 1, PluginTypes: []string{"outbound"}}
}
func (f lifecycleTestFactory) Create(context.Context, plugin.InstanceContext, json.RawMessage) (plugin.Instance, error) {
	return &lifecycleTestInstance{state: f.state}, nil
}
func (i *lifecycleTestInstance) Start(context.Context) error { i.state.started = true; return nil }
func (i *lifecycleTestInstance) Stop(context.Context) error  { i.state.started = false; return nil }
func (i *lifecycleTestInstance) DialContext(context.Context, netx.Flow) (net.Conn, error) {
	if !i.state.started {
		i.state.dialBeforeStart = true
		return nil, fmt.Errorf("DialContext called before Start")
	}
	a, b := net.Pipe()
	_ = b.Close()
	return a, nil
}

func TestControlPlaneResourceLifecycle(t *testing.T) {
	store := resources.NewStore(t.TempDir())
	control, err := newControlPlaneWithStore(registry(), health.NewStore(), event.NewBus(), &graph.Config{
		GraphID:       "test-graph",
		Name:          "test",
		SchemaVersion: 1,
		Revision:      1,
	}, realmConfigForControlTest(), store)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	subscription, err := control.Control(ctx, "create_subscription", "", json.RawMessage(`{"id":"office","name":"Office","source":"direct://\nhttp://127.0.0.1:10808","source_type":"raw","parser_plugin":"io.meshvianex.config.parser.basic","enabled":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if subscription.(resources.Subscription).ID != "office" {
		t.Fatalf("unexpected subscription: %#v", subscription)
	}
	if _, err = control.Control(ctx, "refresh_subscription", "office", nil); err != nil {
		t.Fatal(err)
	}
	testSubscriptionResult, err := control.Control(ctx, "test_subscription", "office", nil)
	if err != nil {
		t.Fatal(err)
	}
	assertConnectivityResultFields(t, testSubscriptionResult)
	configs, err := store.ListConfigs()
	if err != nil {
		t.Fatal(err)
	}
	if len(configs) != 2 {
		t.Fatalf("expected two parsed configs, got %d", len(configs))
	}
	manual, err := control.Control(ctx, "create_config", "", json.RawMessage(`{"name":"Secret SOCKS","plugin_id":"io.meshvianex.outbound.socks5","plugin_type":"outbound","normalized":{"url":"socks5://user:secret@127.0.0.1:10808"},"enabled":true}`))
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(manual)
	if strings.Contains(string(encoded), "secret@") || strings.Contains(string(encoded), `"password":"secret"`) {
		t.Fatalf("resource response leaked credential: %s", encoded)
	}
	snapshot := control.Snapshot()
	encoded, _ = json.Marshal(snapshot)
	if strings.Contains(string(encoded), "secret@") || strings.Contains(string(encoded), `"password":"secret"`) {
		t.Fatalf("snapshot leaked credential: %s", encoded)
	}
	runtime := realm.NewRuntime(realmConfigForControlTest(), control.reg, control.bus)
	control.register(runtime)
	manualConfig := manual.(resources.Config)
	if _, err = control.Control(ctx, "assign_config_to_realm", manualConfig.ID, json.RawMessage(`{"realm_id":"test-realm"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err = control.Control(ctx, "assign_subscription_to_realm", "office", json.RawMessage(`{"realm_id":"test-realm"}`)); err != nil {
		t.Fatal(err)
	}
	assigned := runtime.Config()
	if len(assigned.ConfigRefs) != 3 || len(assigned.Outbounds) != 4 {
		t.Fatalf("resource assignment did not materialize components: refs=%v outbounds=%#v", assigned.ConfigRefs, assigned.Outbounds)
	}
	directResult, err := control.Control(ctx, "create_config", "", json.RawMessage(`{"name":"Test direct","plugin_id":"io.meshvianex.direct","plugin_type":"outbound","normalized":{},"enabled":true}`))
	if err != nil {
		t.Fatal(err)
	}
	testConfigResult, err := control.Control(ctx, "test_config", directResult.(resources.Config).ID, json.RawMessage(`{"target":"127.0.0.1:1"}`))
	if err != nil {
		t.Fatal(err)
	}
	assertConnectivityResultFields(t, testConfigResult)
	if err := runtime.Start(ctx); err != nil {
		t.Fatal(err)
	}
	testRealmResult, err := control.Control(ctx, "test_realm", "test-realm", json.RawMessage(`{"target":"127.0.0.1:1"}`))
	if err != nil {
		t.Fatal(err)
	}
	assertConnectivityResultFields(t, testRealmResult)
	if err := runtime.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}

func assertConnectivityResultFields(t *testing.T, value any) {
	t.Helper()
	result, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("connectivity result has wrong type: %#v", value)
	}
	for _, key := range []string{"success", "stage", "latency_ms", "realm", "outbound", "proxy_type", "destination", "timestamp"} {
		if _, ok := result[key]; !ok {
			t.Fatalf("connectivity result omitted %q: %#v", key, result)
		}
	}
}

func realmConfigForControlTest() realm.Config {
	return realm.Config{ID: "test-realm", Name: "Test Realm", SchemaVersion: 1, Revision: 1, Outbounds: []realm.ComponentRef{{InstanceID: "direct", Plugin: "io.meshvianex.direct", Enabled: true}}, DefaultOutbound: "direct"}
}

func TestConfigReferenceAndSubscriptionDerivedRealmCarryRealTraffic(t *testing.T) {
	proxy, stopProxy := testSOCKS5Proxy(t)
	defer stopProxy()
	target, stopTarget := testEchoTarget(t)
	defer stopTarget()
	store := resources.NewStore(t.TempDir())
	control, err := newControlPlaneWithStore(registry(), health.NewStore(), event.NewBus(), nil, realmConfigForControlTest(), store)
	if err != nil {
		t.Fatal(err)
	}
	rt := realm.NewRuntime(realmConfigForControlTest(), control.reg, control.bus)
	control.register(rt)
	ctx := context.Background()
	manual, err := control.Control(ctx, "create_config", "", json.RawMessage(`{"name":"local proxy","plugin_id":"io.meshvianex.outbound.socks5","plugin_type":"outbound","normalized":{"url":"socks5://127.0.0.1:`+proxy+`"},"enabled":true}`))
	if err != nil {
		t.Fatal(err)
	}
	manualID := manual.(resources.Config).ID
	if _, err = control.Control(ctx, "assign_config_to_realm", manualID, json.RawMessage(`{"realm_id":"test-realm"}`)); err != nil {
		t.Fatal(err)
	}
	if err = rt.Start(ctx); err != nil {
		t.Fatal(err)
	}
	conn, err := rt.DialContext(ctx, netx.NewFlow("test-realm", "tcp", target, "user_traffic"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Write([]byte("config")); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, len("config"))
	if _, err = io.ReadFull(conn, response); err != nil || string(response) != "config" {
		t.Fatalf("config-reference response=%q err=%v", response, err)
	}
	_ = conn.Close()
	if err = rt.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	stopProxy()
	proxy2, stopProxy2 := testSOCKS5Proxy(t)
	defer stopProxy2()
	if _, err = control.Control(ctx, "update_config", manualID, json.RawMessage(`{"id":"`+manualID+`","name":"local proxy updated","plugin_id":"io.meshvianex.outbound.socks5","plugin_type":"outbound","normalized":{"url":"socks5://127.0.0.1:`+proxy2+`"},"enabled":true}`)); err != nil {
		t.Fatal(err)
	}
	if err = rt.Start(ctx); err != nil {
		t.Fatal(err)
	}
	conn, err = rt.DialContext(ctx, netx.NewFlow("test-realm", "tcp", target, "user_traffic"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Write([]byte("updated")); err != nil {
		t.Fatal(err)
	}
	response = make([]byte, len("updated"))
	if _, err = io.ReadFull(conn, response); err != nil || string(response) != "updated" {
		t.Fatalf("updated config-reference response=%q err=%v", response, err)
	}
	_ = conn.Close()
	if err = rt.Stop(ctx); err != nil {
		t.Fatal(err)
	}

	derived, err := control.Control(ctx, "create_subscription", "", json.RawMessage(`{"id":"local-sub","name":"Local feed","source":"socks5://127.0.0.1:`+proxy2+`","source_type":"raw","enabled":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = control.Control(ctx, "refresh_subscription", derived.(resources.Subscription).ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = control.Control(ctx, "assign_subscription_to_realm", "local-sub", json.RawMessage(`{"realm_id":"test-realm"}`)); err != nil {
		t.Fatal(err)
	}
	if err = rt.Start(ctx); err != nil {
		t.Fatal(err)
	}
	conn, err = rt.DialContext(ctx, netx.NewFlow("test-realm", "tcp", target, "user_traffic"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Write([]byte("subscription")); err != nil {
		t.Fatal(err)
	}
	response = make([]byte, len("subscription"))
	if _, err = io.ReadFull(conn, response); err != nil || string(response) != "subscription" {
		t.Fatalf("subscription-derived response=%q err=%v", response, err)
	}
	_ = conn.Close()
	_ = rt.Stop(ctx)
}

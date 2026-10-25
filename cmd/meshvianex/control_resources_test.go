package main

import (
	"context"
	"encoding/json"
	"io"
	"meshvianex/core/event"
	"meshvianex/core/health"
	"meshvianex/core/netx"
	"meshvianex/core/realm"
	"meshvianex/core/resources"
	"strings"
	"testing"
)

func TestConfigComponentSelectsResourceBackedOutbound(t *testing.T) {
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
	rt.SetReferenceResolver(store)
	control.register(rt)
	created, err := control.Control(context.Background(), "create_config", "", json.RawMessage(`{"name":"selected proxy","plugin_id":"io.meshvianex.outbound.socks5","plugin_type":"outbound","normalized":{"url":"socks5://127.0.0.1:`+proxy+`"},"enabled":true}`))
	if err != nil {
		t.Fatal(err)
	}
	item := created.(resources.Config)
	next := rt.Config()
	next.Config = &realm.ConfigSelection{InstanceID: "config-main", Plugin: "io.meshvianex.config.selector.basic", Enabled: true, SourceType: "config", SourceRef: item.ID, Config: json.RawMessage(`{"strategy":"round_robin"}`)}
	if err := rt.Update(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	if err := rt.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer rt.Stop(context.Background())
	conn, err := rt.DialContext(context.Background(), netx.NewFlow("test-realm", "tcp", target, "user_traffic"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("selected")); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, len("selected"))
	if _, err := io.ReadFull(conn, response); err != nil || string(response) != "selected" {
		t.Fatalf("selected config response=%q err=%v", response, err)
	}
	_ = conn.Close()
	stored, err := store.GetConfig(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	stored.Normalized = json.RawMessage(`{"url":"not-a-proxy-url"}`)
	stored.ValidationStatus = "valid"
	if err := store.SaveConfig(stored); err != nil {
		t.Fatal(err)
	}
	if _, err := control.Control(context.Background(), "validate_config", item.ID, nil); err == nil {
		t.Fatal("invalid selected config unexpectedly validated")
	}
	if current := rt.Config().Config; current == nil || current.Enabled {
		t.Fatalf("invalid selected config remained active: %#v", current)
	}
	stored.Normalized = item.Normalized
	stored.ValidationStatus, stored.ValidationError = "valid", ""
	if err := store.SaveConfig(stored); err != nil {
		t.Fatal(err)
	}
	if _, err := control.Control(context.Background(), "validate_config", item.ID, nil); err != nil {
		t.Fatal(err)
	}
	if current := rt.Config().Config; current == nil || !current.Enabled {
		t.Fatalf("revalidated selected config did not reactivate: %#v", current)
	}
}

func TestDeletingSubscriptionUnbindsFirstClassConfigSelector(t *testing.T) {
	store := resources.NewStore(t.TempDir())
	control, err := newControlPlaneWithStore(registry(), health.NewStore(), event.NewBus(), nil, realmConfigForControlTest(), store)
	if err != nil {
		t.Fatal(err)
	}
	rt := realm.NewRuntime(realmConfigForControlTest(), control.reg, control.bus)
	control.register(rt)
	ctx := context.Background()
	created, err := control.Control(ctx, "create_subscription", "", json.RawMessage(`{"id":"selected-sub","name":"Selected feed","source":"direct://","source_type":"raw","enabled":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = control.Control(ctx, "refresh_subscription", created.(resources.Subscription).ID, nil); err != nil {
		t.Fatal(err)
	}
	next := rt.Config()
	next.Config = &realm.ConfigSelection{InstanceID: "config-main", Plugin: "io.meshvianex.config.selector.basic", Enabled: true, SourceType: "subscription", SourceRef: "selected-sub", Config: json.RawMessage(`{"strategy":"round_robin"}`)}
	if err := rt.Update(ctx, next); err != nil {
		t.Fatal(err)
	}
	if _, err := control.Control(ctx, "delete_subscription", "selected-sub", nil); err != nil {
		t.Fatal(err)
	}
	if got := rt.Config().Config; got != nil {
		t.Fatalf("deleted subscription remained bound to Config component: %#v", got)
	}
}

func TestAssignmentPreservesDefaultOutboundUntilExplicitlyChanged(t *testing.T) {
	store := resources.NewStore(t.TempDir())
	control, err := newControlPlaneWithStore(registry(), health.NewStore(), event.NewBus(), nil, realmConfigForControlTest(), store)
	if err != nil {
		t.Fatal(err)
	}
	rt := realm.NewRuntime(realmConfigForControlTest(), control.reg, control.bus)
	control.register(rt)
	created, err := control.Control(context.Background(), "create_config", "", json.RawMessage(`{"name":"second","plugin_id":"io.meshvianex.direct","plugin_type":"outbound","normalized":{},"enabled":true}`))
	if err != nil {
		t.Fatal(err)
	}
	item := created.(resources.Config)
	if _, err := control.Control(context.Background(), "assign_config_to_realm", item.ID, json.RawMessage(`{"realm_id":"test-realm"}`)); err != nil {
		t.Fatal(err)
	}
	if got := rt.Config().DefaultOutbound; got != "direct" {
		t.Fatalf("implicit assignment changed default outbound to %q", got)
	}
	if _, err := control.Control(context.Background(), "set_default_outbound", "test-realm", json.RawMessage(`{"outbound":"resource-`+item.ID+`"}`)); err != nil {
		t.Fatal(err)
	}
	if got := rt.Config().DefaultOutbound; got != "resource-"+item.ID {
		t.Fatalf("explicit default outbound=%q", got)
	}
}

func TestResourceEditsPreserveRedactedSecretsAndUpdateBindings(t *testing.T) {
	store := resources.NewStore(t.TempDir())
	control, err := newControlPlaneWithStore(registry(), health.NewStore(), event.NewBus(), nil, realmConfigForControlTest(), store)
	if err != nil {
		t.Fatal(err)
	}
	rt := realm.NewRuntime(realmConfigForControlTest(), control.reg, control.bus)
	control.register(rt)
	created, err := control.Control(context.Background(), "create_config", "", json.RawMessage(`{"name":"secret config","plugin_id":"io.meshvianex.outbound.socks5","plugin_type":"outbound","normalized":{"url":"socks5://user:secret@127.0.0.1:1080?token=abc","password":"secret"},"enabled":true}`))
	if err != nil {
		t.Fatal(err)
	}
	item := created.(resources.Config)
	if _, err := control.Control(context.Background(), "assign_config_to_realm", item.ID, json.RawMessage(`{"realm_id":"test-realm"}`)); err != nil {
		t.Fatal(err)
	}
	redacted := resources.RedactConfig(item)
	redacted.Name = "edited secret config"
	if _, err := control.Control(context.Background(), "update_config", item.ID, mustJSON(redacted)); err != nil {
		t.Fatal(err)
	}
	stored, err := store.GetConfig(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stored.Normalized), "[redacted]") || !strings.Contains(string(stored.Normalized), "secret") || !hasConfigComponent(rt.Config(), item.ID) {
		t.Fatalf("redacted edit damaged stored config or binding: stored=%s realm=%#v", stored.Normalized, rt.Config())
	}
	if _, err := control.Control(context.Background(), "set_config_enabled", item.ID, json.RawMessage(`{"enabled":false}`)); err != nil {
		t.Fatal(err)
	}
	if hasConfigComponent(rt.Config(), item.ID) || containsString(rt.Config().ConfigRefs, item.ID) {
		t.Fatalf("disabled config remained active: %#v", rt.Config())
	}
	if _, err := control.Control(context.Background(), "set_config_enabled", item.ID, json.RawMessage(`{"enabled":true}`)); err != nil {
		t.Fatal(err)
	}
	if hasConfigComponent(rt.Config(), item.ID) || containsString(rt.Config().ConfigRefs, item.ID) {
		t.Fatalf("enabling a previously unassigned config changed Realm assignment: %#v", rt.Config())
	}
	if _, err := control.Control(context.Background(), "assign_config_to_realm", item.ID, json.RawMessage(`{"realm_id":"test-realm"}`)); err != nil {
		t.Fatal(err)
	}
	if !hasConfigComponent(rt.Config(), item.ID) || !containsString(rt.Config().ConfigRefs, item.ID) {
		t.Fatalf("explicitly re-assigned config was not bound: %#v", rt.Config())
	}
}

func TestDisablingSubscriptionRemovesAndReenablesDerivedBindings(t *testing.T) {
	store := resources.NewStore(t.TempDir())
	control, err := newControlPlaneWithStore(registry(), health.NewStore(), event.NewBus(), nil, realmConfigForControlTest(), store)
	if err != nil {
		t.Fatal(err)
	}
	rt := realm.NewRuntime(realmConfigForControlTest(), control.reg, control.bus)
	control.register(rt)
	ctx := context.Background()
	if _, err := control.Control(ctx, "create_subscription", "", json.RawMessage(`{"id":"toggle-sub","name":"Toggle","source":"direct://","source_type":"raw","enabled":true}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := control.Control(ctx, "refresh_subscription", "toggle-sub", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := control.Control(ctx, "assign_subscription_to_realm", "toggle-sub", json.RawMessage(`{"realm_id":"test-realm"}`)); err != nil {
		t.Fatal(err)
	}
	if len(rt.Config().ConfigRefs) != 1 {
		t.Fatalf("subscription was not bound: %#v", rt.Config())
	}
	if _, err := control.Control(ctx, "set_subscription_enabled", "toggle-sub", json.RawMessage(`{"enabled":false}`)); err != nil {
		t.Fatal(err)
	}
	if len(rt.Config().ConfigRefs) != 0 {
		t.Fatalf("disabled subscription retained active configs: %#v", rt.Config())
	}
	if _, err := control.Control(ctx, "set_subscription_enabled", "toggle-sub", json.RawMessage(`{"enabled":true}`)); err != nil {
		t.Fatal(err)
	}
	if len(rt.Config().ConfigRefs) != 1 {
		t.Fatalf("reenabled subscription was not rebound: %#v", rt.Config())
	}
}

func TestTestConfigStartsOutboundBeforeDial(t *testing.T) {
	state := &lifecycleTestState{}
	reg := registry()
	if err := reg.Register(lifecycleTestFactory{state: state}); err != nil {
		t.Fatal(err)
	}
	control, err := newControlPlaneWithStore(reg, health.NewStore(), event.NewBus(), nil, realmConfigForControlTest(), resources.NewStore(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	created, err := control.Control(context.Background(), "create_config", "", json.RawMessage(`{"name":"lifecycle","plugin_id":"test.lifecycle.outbound","plugin_type":"outbound","normalized":{},"enabled":true}`))
	if err != nil {
		t.Fatal(err)
	}
	result, err := control.Control(context.Background(), "test_config", created.(resources.Config).ID, json.RawMessage(`{"target":"example.test:443"}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.(map[string]any)["success"] != true || state.dialBeforeStart {
		t.Fatalf("lifecycle test result=%#v state=%#v", result, state)
	}
}

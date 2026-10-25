package main

import (
	"context"
	"encoding/json"
	"meshvianex/core/event"
	"meshvianex/core/health"
	"meshvianex/core/realm"
	"meshvianex/core/resources"
	"testing"
	"time"
)

func TestApplicationStateRestartsRealmResourcesAndGraph(t *testing.T) {
	root := t.TempDir()
	store := resources.NewStore(root)
	initial := realmConfigForControlTest()
	graphCfg := chainGraph([]string{initial.ID}, "Persisted topology", 1)
	graphCfg.GraphID = "persisted-graph"
	control, err := newControlPlaneWithStore(registry(), health.NewStore(), event.NewBus(), &graphCfg, initial, store)
	if err != nil {
		t.Fatal(err)
	}
	rt := realm.NewRuntime(initial, control.reg, control.bus)
	control.register(rt)
	ctx := context.Background()
	if _, err := control.Control(ctx, "create_subscription", "", json.RawMessage(`{"id":"restart-sub","name":"Restart feed","source":"direct://","source_type":"raw","enabled":true}`)); err != nil {
		t.Fatal(err)
	}
	created, err := control.Control(ctx, "create_config", "", json.RawMessage(`{"id":"restart-config","name":"Restart config","plugin_id":"io.meshvianex.direct","plugin_type":"outbound","normalized":{},"enabled":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.Control(ctx, "assign_config_to_realm", created.(resources.Config).ID, json.RawMessage(`{"realm_id":"test-realm"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := control.Control(ctx, "assign_subscription_to_realm", "restart-sub", json.RawMessage(`{"realm_id":"test-realm"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := control.Control(ctx, "set_graph", "", mustJSON(graphCfg)); err != nil {
		t.Fatal(err)
	}
	if _, err := control.Control(ctx, "save_changes", "", nil); err != nil {
		t.Fatal(err)
	}
	loadedRealms, loadedGraph, err := loadApplicationState(root, realm.Config{}, nil)
	if err != nil || len(loadedRealms) != 1 {
		t.Fatalf("loaded realms=%#v graph=%#v err=%v", loadedRealms, loadedGraph, err)
	}
	loaded := loadedRealms[0]
	if !containsString(loaded.ConfigRefs, "restart-config") || !containsString(loaded.SubscriptionRefs, "restart-sub") || loaded.DefaultOutbound != "direct" || len(loadedGraph.Edges) == 0 {
		t.Fatalf("persisted state lost bindings or graph: realm=%#v graph=%#v", loaded, loadedGraph)
	}
	fresh, err := newControlPlaneWithStore(registry(), health.NewStore(), event.NewBus(), &loadedGraph, loaded, store)
	if err != nil {
		t.Fatal(err)
	}
	restarted := realm.NewRuntime(loaded, fresh.reg, fresh.bus)
	fresh.register(restarted)
	if err := restarted.Start(ctx); err != nil {
		t.Fatalf("restarted Realm failed to start: %v", err)
	}
	defer restarted.Stop(ctx)
}

func TestAutomaticSubscriptionRefreshRunsAndDoesNotOverlap(t *testing.T) {
	store := resources.NewStore(t.TempDir())
	control, err := newControlPlaneWithStore(registry(), health.NewStore(), event.NewBus(), nil, realmConfigForControlTest(), store)
	if err != nil {
		t.Fatal(err)
	}
	control.register(realm.NewRuntime(realmConfigForControlTest(), control.reg, control.bus))
	if _, err := control.Control(context.Background(), "create_subscription", "", json.RawMessage(`{"id":"scheduled","name":"Scheduled","source":"direct://","source_type":"raw","refresh_interval":"10ms","enabled":true}`)); err != nil {
		t.Fatal(err)
	}
	lock := control.refreshLockFor("scheduled")
	lock.Lock()
	if started := control.scheduleDueRefreshes(context.Background(), time.Now()); started != 0 {
		t.Fatalf("refresh scheduler overlapped an active refresh: started=%d", started)
	}
	lock.Unlock()
	control.startRefreshScheduler(context.Background())
	defer control.stopRefreshScheduler()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		item, getErr := store.GetSubscription("scheduled")
		if getErr == nil && item.LastRefreshStatus == "success" {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	item, _ := store.GetSubscription("scheduled")
	t.Fatalf("automatic refresh did not complete: %#v", item)
}

func TestConfigURIImportUsesRegisteredParser(t *testing.T) {
	control, err := newControlPlaneWithStore(registry(), health.NewStore(), event.NewBus(), nil, realmConfigForControlTest(), resources.NewStore(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	result, err := control.Control(context.Background(), "import_config", "", json.RawMessage(`{"name":"URI import","uri":"direct://","parser_plugin":"io.meshvianex.config.parser.basic","enabled":true}`))
	if err != nil {
		t.Fatal(err)
	}
	values := result.(map[string]any)["configs"].([]resources.Config)
	if len(values) != 1 || values[0].PluginID != "io.meshvianex.direct" {
		t.Fatalf("import result=%#v", result)
	}
	if _, err := control.Control(context.Background(), "create_subscription", "", json.RawMessage(`{"id":"bad-parser","name":"Bad","source":"direct://","parser_plugin":"missing.parser","enabled":true}`)); err == nil {
		t.Fatal("unregistered parser was accepted")
	}
}

func TestConfigURIImportSupportsPerEntryNames(t *testing.T) {
	control, err := newControlPlaneWithStore(registry(), health.NewStore(), event.NewBus(), nil, realmConfigForControlTest(), resources.NewStore(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	result, err := control.Control(context.Background(), "import_config", "", json.RawMessage(`{"uri":"direct://\nsocks5://127.0.0.1:1080","names":["Local direct","Backup proxy"],"parser_plugin":"io.meshvianex.config.parser.basic","enabled":true}`))
	if err != nil {
		t.Fatal(err)
	}
	values := result.(map[string]any)["configs"].([]resources.Config)
	if len(values) != 2 || values[0].Name != "Local direct" || values[1].Name != "Backup proxy" {
		t.Fatalf("per-entry names were not preserved: %#v", values)
	}
	if _, err := control.Control(context.Background(), "import_config", "", json.RawMessage(`{"uri":"direct://","parser_plugin":"io.meshvianex.config.parser.basic","enabled":true}`)); err == nil {
		t.Fatal("unnamed imported config was accepted")
	}
}

func TestSubscriptionRefreshKeepsLKGAndRemovesStaleBindings(t *testing.T) {
	store := resources.NewStore(t.TempDir())
	control, err := newControlPlaneWithStore(registry(), health.NewStore(), event.NewBus(), nil, realmConfigForControlTest(), store)
	if err != nil {
		t.Fatal(err)
	}
	rt := realm.NewRuntime(realmConfigForControlTest(), control.reg, control.bus)
	control.register(rt)
	ctx := context.Background()
	if _, err := control.Control(ctx, "create_subscription", "", json.RawMessage(`{"id":"lkg-sub","name":"LKG","source":"direct://\nsocks5://127.0.0.1:1080","source_type":"raw","enabled":true}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := control.Control(ctx, "refresh_subscription", "lkg-sub", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := control.Control(ctx, "assign_subscription_to_realm", "lkg-sub", json.RawMessage(`{"realm_id":"test-realm"}`)); err != nil {
		t.Fatal(err)
	}
	before, err := store.ListConfigs()
	if err != nil || len(before) != 2 {
		t.Fatalf("initial configs=%#v err=%v", before, err)
	}
	if _, err := control.Control(ctx, "patch_subscription", "lkg-sub", json.RawMessage(`{"source":"direct://","source_type":"raw"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := control.Control(ctx, "refresh_subscription", "lkg-sub", nil); err != nil {
		t.Fatal(err)
	}
	after, err := store.ListConfigs()
	if err != nil {
		t.Fatal(err)
	}
	active := 0
	for _, item := range after {
		if item.SourceSubscriptionID == "lkg-sub" && item.Enabled {
			active++
		}
	}
	if active != 1 || len(rt.Config().ConfigRefs) != 1 {
		t.Fatalf("stale configs or bindings were not removed: configs=%#v realm=%#v", after, rt.Config())
	}
}

func TestSaveChangesPropagatesPersistenceError(t *testing.T) {
	control, err := newControlPlaneWithStore(registry(), health.NewStore(), event.NewBus(), nil, realmConfigForControlTest(), resources.NewStore(""))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.Control(context.Background(), "save_changes", "", nil); err == nil {
		t.Fatal("save_changes swallowed persistence error")
	}
}

func TestDurabilityFailureRestoresRealmAndGraphMemoryState(t *testing.T) {
	initial := realmConfigForControlTest()
	control, err := newControlPlaneWithStore(registry(), health.NewStore(), event.NewBus(), nil, initial, resources.NewStore(""))
	if err != nil {
		t.Fatal(err)
	}
	rt := realm.NewRuntime(initial, control.reg, control.bus)
	control.register(rt)
	next := initial
	next.Name = "changed but not durable"
	if _, err := control.Control(context.Background(), "update_realm", initial.ID, mustJSON(next)); err == nil {
		t.Fatal("Realm update unexpectedly succeeded without a state root")
	}
	if got := rt.Config().Name; got != initial.Name {
		t.Fatalf("Realm memory state changed after failed persistence: %q", got)
	}
	previousGraph := control.graph.Config()
	changedGraph := previousGraph
	changedGraph.Name = "changed topology"
	if _, err := control.Control(context.Background(), "set_graph", "", mustJSON(changedGraph)); err == nil {
		t.Fatal("Graph update unexpectedly succeeded without a state root")
	}
	if got := control.graph.Config().Name; got != previousGraph.Name {
		t.Fatalf("Graph memory state changed after failed persistence: %q", got)
	}
}

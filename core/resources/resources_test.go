package resources

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestRedactionPreservesNormalQueryParameters(t *testing.T) {
	got := redactString("https://user:password@example.test/feed?region=eu&Token=abc&api_key=def&password=pwd&KEY=other&keep=value")
	for _, want := range []string{"region=eu", "keep=value", "Token=%5Bredacted%5D", "api_key=%5Bredacted%5D", "KEY=%5Bredacted%5D", "password=%5Bredacted%5D"} {
		if !strings.Contains(got, want) {
			t.Fatalf("redacted URL %q does not contain %q", got, want)
		}
	}
	if strings.Contains(got, "abc") || strings.Contains(got, "def") || strings.Contains(got, "pwd") || strings.Contains(got, "other") || strings.Contains(got, "password@example") {
		t.Fatalf("secret survived redaction: %q", got)
	}
}

func TestRedactJSONSensitiveKeysAndURLs(t *testing.T) {
	raw := json.RawMessage(`{"headers":{"Authorization":"Bearer secret","x":"ok"},"url":"https://example.test/?access_token=abc&x=1","items":["socks5://u:p@example.test:1"]}`)
	redacted := string(RedactJSON(raw))
	if strings.Contains(redacted, "secret") || strings.Contains(redacted, "abc") || strings.Contains(redacted, ":p@") {
		t.Fatalf("secret survived JSON redaction: %s", redacted)
	}
	if !strings.Contains(redacted, `"x":"ok"`) || !strings.Contains(redacted, `x=1`) {
		t.Fatalf("normal values were not preserved: %s", redacted)
	}
}

func TestMergeRedactedJSONPreservesSecretsAndAcceptsSafeEdits(t *testing.T) {
	original := json.RawMessage(`{"url":"socks5://user:secret@example.test:1080?region=eu&token=abc","password":"hidden","label":"old","nested":{"API_KEY":"key-secret"}}`)
	presented := RedactJSON(original)
	var edited map[string]any
	if err := json.Unmarshal(presented, &edited); err != nil {
		t.Fatal(err)
	}
	edited["label"] = "new"
	editedJSON, _ := json.Marshal(edited)
	merged := MergeRedactedJSON(original, editedJSON)
	var want, got any
	if err := json.Unmarshal(original, &want); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(merged, &got); err != nil {
		t.Fatal(err)
	}
	if got.(map[string]any)["label"] != "new" || !reflect.DeepEqual(want.(map[string]any)["url"], got.(map[string]any)["url"]) || !reflect.DeepEqual(want.(map[string]any)["password"], got.(map[string]any)["password"]) || !reflect.DeepEqual(want.(map[string]any)["nested"], got.(map[string]any)["nested"]) {
		t.Fatalf("redacted edit lost or failed to preserve fields: want=%s got=%s", original, merged)
	}
}

func TestStoreRefreshReconcilesAndReloads(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.SaveSubscription(Subscription{ID: "sub-1", Name: "Office", Source: "socks5://proxy:1080", SourceType: "raw", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	first := []NormalizedConfig{{Name: "First display name", PluginID: "io.meshvianex.direct", PluginType: "outbound", Config: json.RawMessage(`{}`)}, {PluginID: "io.meshvianex.outbound.socks5", PluginType: "outbound", Config: json.RawMessage(`{"url":"socks5://proxy:1080"}`)}}
	if _, err := store.ReconcileSubscription("sub-1", first, "uri_list", time.Now()); err != nil {
		t.Fatal(err)
	}
	configs, err := store.ListConfigs()
	if err != nil || len(configs) != 2 {
		t.Fatalf("initial configs=%d err=%v", len(configs), err)
	}
	stableID := "cfg-" + StableKey(first[0])[:16]
	second := []NormalizedConfig{{Name: "Renamed display name", PluginID: first[0].PluginID, PluginType: first[0].PluginType, Config: first[0].Config}}
	if _, err := store.ReconcileSubscription("sub-1", second, "single_uri", time.Now()); err != nil {
		t.Fatal(err)
	}
	configs, err = store.ListConfigs()
	if err != nil || len(configs) != 2 {
		t.Fatalf("reconciled configs=%d err=%v", len(configs), err)
	}
	if configs[0].ID != stableID && configs[1].ID != stableID {
		t.Fatalf("stable config id was not preserved: %#v", configs)
	}
	for _, item := range configs {
		if item.ID == stableID && item.Name != "Renamed display name" {
			t.Fatalf("stable config identity did not preserve the renamed display label: %#v", item)
		}
	}
	var stale Config
	for _, item := range configs {
		if item.ID != stableID {
			stale = item
		}
	}
	if stale.Enabled || stale.ValidationStatus != "stale" {
		t.Fatalf("removed entry should be retained as stale: %#v", stale)
	}
	reloaded := NewStore(store.Root)
	if got, err := reloaded.GetSubscription("sub-1"); err != nil || got.Name != "Office" {
		t.Fatalf("reload subscription=%#v err=%v", got, err)
	}
}

func TestConfigCandidatesRespectConfigAndSubscriptionLifecycle(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.SaveSubscription(Subscription{ID: "sub-1", Name: "Office", Source: "direct://", SourceType: "raw", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	for _, item := range []Config{
		{ID: "cfg-active", Name: "Active", SourceSubscriptionID: "sub-1", PluginID: "io.meshvianex.direct", PluginType: "outbound", Normalized: json.RawMessage(`{}`), Enabled: true, ValidationStatus: "valid"},
		{ID: "cfg-disabled", Name: "Disabled", SourceSubscriptionID: "sub-1", PluginID: "io.meshvianex.direct", PluginType: "outbound", Normalized: json.RawMessage(`{}`), Enabled: false, ValidationStatus: "valid"},
		{ID: "cfg-stale", Name: "Stale", SourceSubscriptionID: "sub-1", PluginID: "io.meshvianex.direct", PluginType: "outbound", Normalized: json.RawMessage(`{}`), Enabled: true, ValidationStatus: "stale"},
	} {
		if err := store.SaveConfig(item); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := store.ConfigCandidates("config", "cfg-active"); err != nil || !reflect.DeepEqual(got, []string{"cfg-active"}) {
		t.Fatalf("single config candidates=%v err=%v", got, err)
	}
	if got, err := store.ConfigCandidates("subscription", "sub-1"); err != nil || !reflect.DeepEqual(got, []string{"cfg-active"}) {
		t.Fatalf("subscription candidates=%v err=%v", got, err)
	}
	if err := store.SaveSubscription(Subscription{ID: "sub-1", Name: "Office", Source: "direct://", SourceType: "raw", Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ConfigCandidates("subscription", "sub-1"); err == nil {
		t.Fatal("disabled subscription unexpectedly produced candidates")
	}
}

func TestRedactConfigDoesNotExposeSecrets(t *testing.T) {
	item := Config{Normalized: json.RawMessage(`{"username":"alice","password":"do-not-share","nested":{"token":"secret"}}`)}
	redacted := string(RedactConfig(item).Normalized)
	if redacted == "" || containsAny(redacted, "do-not-share", "secret") {
		t.Fatalf("secret leaked in redacted config: %s", redacted)
	}
}

func TestRestartKeepsResourcesAndAtomicPreviousState(t *testing.T) {
	root := t.TempDir()
	first := NewStore(root)
	if err := first.SaveSubscription(Subscription{ID: "sub", Name: "Feed", Source: "socks5://user:secret@127.0.0.1:10808", SourceType: "raw", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := first.SaveConfig(Config{ID: "cfg", Name: "Proxy", PluginID: "io.meshvianex.outbound.socks5", PluginType: "outbound", Normalized: json.RawMessage(`{"url":"socks5://user:secret@127.0.0.1:10808"}`), Enabled: true, ValidationStatus: "valid"}); err != nil {
		t.Fatal(err)
	}
	info, statErr := os.Stat(filepath.Join(root, "configs.json"))
	if statErr != nil {
		t.Fatal(statErr)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("resource file is not private: mode=%v", info.Mode().Perm())
	}
	restarted := NewStore(root)
	sub, err := restarted.GetSubscription("sub")
	if err != nil || sub.Source == "" {
		t.Fatalf("subscription did not survive restart: %#v %v", sub, err)
	}
	cfg, err := restarted.GetConfig("cfg")
	if err != nil || !restarted.HasConfig("cfg") {
		t.Fatalf("config did not survive restart: %#v %v", cfg, err)
	}
	if _, ok := restarted.ResolveConfig("cfg"); !ok {
		t.Fatal("enabled valid config should resolve for runtime")
	}
	if string(RedactConfig(cfg).Normalized) == string(cfg.Normalized) || string(RedactSubscription(sub).Source) == sub.Source {
		t.Fatal("restart-loaded secrets were not redacted at the API boundary")
	}
	if err := restarted.SaveConfig(Config{ID: "cfg", Name: "Proxy", PluginID: "io.meshvianex.outbound.socks5", PluginType: "outbound", Normalized: json.RawMessage(`{"url":"socks5://user:secret@127.0.0.1:10808"}`), Enabled: false, ValidationStatus: "invalid"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := restarted.ResolveConfig("cfg"); ok {
		t.Fatal("disabled config must not resolve for a running Realm")
	}
}

func TestAtomicWriteFailureLeavesPreviousFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	previous := []byte(`{"version":1}`)
	if err := os.WriteFile(path, previous, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(path, make(chan int)); err == nil {
		t.Fatal("expected marshal failure")
	}
	current, err := os.ReadFile(path)
	if err != nil || string(current) != string(previous) {
		t.Fatalf("previous state was not preserved: %q err=%v", current, err)
	}
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		for i := 0; i+len(needle) <= len(value); i++ {
			if value[i:i+len(needle)] == needle {
				return true
			}
		}
	}
	return false
}

func TestStoreCanonicalizesPreReleasePluginMetadata(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	if err := os.WriteFile(filepath.Join(root, "configs.json"), []byte(`[{"id":"legacy","name":"Legacy","format":"json","plugin_id":"io.meshvianex.protocol.raw","plugin_type":"rule_engine","normalized":{},"enabled":true,"validation_status":"valid"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	items, err := store.ListConfigs()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].PluginID != "io.meshvianex.protocol.passthrough" || items[0].PluginType != "routing" {
		t.Fatalf("legacy resource metadata was not canonicalized: %#v", items)
	}
}

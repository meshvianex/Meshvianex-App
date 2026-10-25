package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"meshvianex/core/event"
	"meshvianex/core/health"
	"meshvianex/core/realm"
	"meshvianex/core/resources"
	"testing"
	"time"
)

func mustJSON(value any) json.RawMessage {
	b, _ := json.Marshal(value)
	return b
}

func TestConfigReferenceFailureModesAndChangedSemantics(t *testing.T) {
	store := resources.NewStore(t.TempDir())
	control, err := newControlPlaneWithStore(registry(), health.NewStore(), event.NewBus(), nil, realmConfigForControlTest(), store)
	if err != nil {
		t.Fatal(err)
	}
	rt := realm.NewRuntime(realmConfigForControlTest(), control.reg, control.bus)
	control.register(rt)
	valid := resources.Config{ID: "valid", Name: "Valid", PluginID: "io.meshvianex.direct", PluginType: "outbound", Normalized: json.RawMessage(`{"timeout_seconds":1}`), Enabled: true, ValidationStatus: "valid"}
	if err := store.SaveConfig(valid); err != nil {
		t.Fatal(err)
	}
	cfg := realmConfigForControlTest()
	cfg.Outbounds = append(cfg.Outbounds, realm.ComponentRef{InstanceID: "referenced", Plugin: "io.meshvianex.direct", Enabled: true, ConfigRef: "valid"})
	cfg.ConfigRefs = []string{"valid"}
	if err := rt.ValidateConfig(cfg); err != nil {
		t.Fatalf("valid config reference rejected: %v", err)
	}
	before, ok := store.ResolveConfig("valid")
	if !ok {
		t.Fatal("valid config did not resolve")
	}
	valid.Normalized = json.RawMessage(`{"timeout_seconds":2}`)
	if err := store.SaveConfig(valid); err != nil {
		t.Fatal(err)
	}
	after, ok := store.ResolveConfig("valid")
	if !ok || bytes.Equal(before, after) {
		t.Fatalf("changed referenced config was not visible: before=%s after=%s", before, after)
	}

	cfg.ConfigRefs = append(cfg.ConfigRefs, "missing")
	if err := rt.ValidateConfig(cfg); err == nil {
		t.Fatal("missing config reference unexpectedly validated")
	}
	cfg.ConfigRefs = []string{"valid"}
	valid.Enabled = false
	if err := store.SaveConfig(valid); err != nil {
		t.Fatal(err)
	}
	if err := rt.ValidateConfig(cfg); err == nil {
		t.Fatal("disabled config reference unexpectedly validated")
	}
	valid.Enabled = true
	if err := store.SaveConfig(valid); err != nil {
		t.Fatal(err)
	}
	incompatible := resources.Config{ID: "incompatible", Name: "Inbound", PluginID: "io.meshvianex.inbound.http", PluginType: "inbound", Normalized: json.RawMessage(`{"listen":"127.0.0.1:0"}`), Enabled: true, ValidationStatus: "valid"}
	if err := store.SaveConfig(incompatible); err != nil {
		t.Fatal(err)
	}
	cfg.Outbounds = append(cfg.Outbounds, realm.ComponentRef{InstanceID: "wrong-reference", Plugin: "io.meshvianex.direct", Enabled: true, ConfigRef: "incompatible"})
	if err := rt.ValidateConfig(cfg); err == nil {
		t.Fatal("incompatible config reference unexpectedly validated")
	}
	cfg.Outbounds = cfg.Outbounds[:len(cfg.Outbounds)-1]
	if err := store.DeleteConfig("valid"); err != nil {
		t.Fatal(err)
	}
	if err := rt.ValidateConfig(cfg); err == nil {
		t.Fatal("deleted config reference unexpectedly validated")
	}
}

func TestRealmBindingPersistsAndResolvesAfterRestart(t *testing.T) {
	resourceStore := resources.NewStore(t.TempDir())
	if err := resourceStore.SaveConfig(resources.Config{ID: "persisted", Name: "Persisted direct", PluginID: "io.meshvianex.direct", PluginType: "outbound", Normalized: json.RawMessage(`{"timeout_seconds":2}`), Enabled: true, ValidationStatus: "valid"}); err != nil {
		t.Fatal(err)
	}
	config := realmConfigForControlTest()
	config.Outbounds = []realm.ComponentRef{{InstanceID: "persisted-out", Plugin: "io.meshvianex.direct", Enabled: true, ConfigRef: "persisted"}}
	config.DefaultOutbound = "persisted-out"
	config.ConfigRefs = []string{"persisted"}
	registry := registry()
	rt := realm.NewRuntime(config, registry, event.NewBus())
	rt.SetReferenceResolver(resourceStore)
	if err := rt.ValidateConfig(config); err != nil {
		t.Fatalf("reference did not validate before persistence: %v", err)
	}
	realmStore := realm.Store{Root: t.TempDir()}
	if err := rt.Persist(realmStore); err != nil {
		t.Fatal(err)
	}
	loaded, err := realmStore.Load(config.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ConfigRefs[0] != "persisted" || loaded.Outbounds[0].ConfigRef != "persisted" {
		t.Fatalf("realm binding was not persisted: %#v", loaded)
	}
	restarted := realm.NewRuntime(loaded, registry, event.NewBus())
	restarted.SetReferenceResolver(resourceStore)
	if err := restarted.Start(context.Background()); err != nil {
		t.Fatalf("restart could not resolve persisted config reference: %v", err)
	}
	if err := restarted.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func testEchoTarget(t *testing.T) (string, func()) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go func() { defer conn.Close(); _, _ = io.Copy(conn, conn) }()
		}
	}()
	return listener.Addr().String(), func() { _ = listener.Close() }
}

func testSOCKS5Proxy(t *testing.T) (string, func()) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			client, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go serveTestSOCKS5(client)
		}
	}()
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	return port, func() { _ = listener.Close() }
}

func serveTestSOCKS5(client net.Conn) {
	defer client.Close()
	var header [2]byte
	if _, err := io.ReadFull(client, header[:]); err != nil || header[0] != 5 {
		return
	}
	methods := make([]byte, int(header[1]))
	if _, err := io.ReadFull(client, methods); err != nil {
		return
	}
	if _, err := client.Write([]byte{5, 0}); err != nil {
		return
	}
	var request [4]byte
	if _, err := io.ReadFull(client, request[:]); err != nil || request[0] != 5 || request[1] != 1 {
		return
	}
	var host string
	switch request[3] {
	case 1:
		address := make([]byte, 4)
		if _, err := io.ReadFull(client, address); err != nil {
			return
		}
		host = net.IP(address).String()
	case 3:
		var length [1]byte
		if _, err := io.ReadFull(client, length[:]); err != nil {
			return
		}
		address := make([]byte, int(length[0]))
		if _, err := io.ReadFull(client, address); err != nil {
			return
		}
		host = string(address)
	case 4:
		address := make([]byte, 16)
		if _, err := io.ReadFull(client, address); err != nil {
			return
		}
		host = net.IP(address).String()
	default:
		return
	}
	var portBytes [2]byte
	if _, err := io.ReadFull(client, portBytes[:]); err != nil {
		return
	}
	target := net.JoinHostPort(host, fmt.Sprintf("%d", int(portBytes[0])<<8|int(portBytes[1])))
	upstream, err := net.DialTimeout("tcp", target, time.Second)
	if err != nil {
		_, _ = client.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	defer upstream.Close()
	if _, err = client.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(upstream, client); done <- struct{}{} }()
	go func() { _, _ = io.Copy(client, upstream); done <- struct{}{} }()
	<-done
}

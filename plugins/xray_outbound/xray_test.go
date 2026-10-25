package xray_outbound

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"meshvianex/core/netx"
	"meshvianex/core/plugin"

	"github.com/xtls/xray-core/app/proxyman"
	xraycore "github.com/xtls/xray-core/core"
)

func TestFactoryValidatesXrayConfig(t *testing.T) {
	_, err := (Factory{}).Create(context.Background(), plugin.InstanceContext{}, json.RawMessage("{"))
	if err == nil || !strings.Contains(err.Error(), "xray config") {
		t.Fatalf("expected invalid plugin config error, got %v", err)
	}
	_, err = (Factory{}).Create(context.Background(), plugin.InstanceContext{}, json.RawMessage("{\"inline_config\":null}"))
	if err == nil || !strings.Contains(err.Error(), "JSON object") {
		t.Fatalf("expected JSON object error, got %v", err)
	}
	_, err = (Factory{}).Create(context.Background(), plugin.InstanceContext{}, mustJSON(map[string]any{
		"inline_config": map[string]any{"outbounds": []any{}},
	}))
	if err == nil || !strings.Contains(err.Error(), "non-empty array") {
		t.Fatalf("expected missing outbound error, got %v", err)
	}
	_, err = (Factory{}).Create(context.Background(), plugin.InstanceContext{}, mustJSON(map[string]any{
		"inline_config": map[string]any{
			"inbounds":  []any{map[string]any{"listen": "0.0.0.0", "port": 1080, "protocol": "socks"}},
			"outbounds": []any{map[string]any{"protocol": "freedom"}},
		},
	}))
	if err == nil || !strings.Contains(err.Error(), "user-provided inbounds") {
		t.Fatalf("expected managed-inbound error, got %v", err)
	}
}

func TestFactoryUsesDefaultInlineConfig(t *testing.T) {
	created, err := (Factory{}).Create(context.Background(), plugin.InstanceContext{}, json.RawMessage("{}"))
	if err != nil {
		t.Fatal(err)
	}
	instance := created.(*Instance)
	var config struct {
		Outbounds []struct {
			Protocol string "json:\"protocol\""
		} "json:\"outbounds\""
	}
	if err := json.Unmarshal(instance.cfg.InlineConfig, &config); err != nil {
		t.Fatal(err)
	}
	if len(config.Outbounds) != 1 || config.Outbounds[0].Protocol != "freedom" {
		t.Fatalf("unexpected default Xray outbounds: %+v", config.Outbounds)
	}
}

func TestConfigPathIsLoaded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "xray.json")
	config := []byte("{\"outbounds\":[{\"protocol\":\"freedom\"}]}")
	if err := os.WriteFile(path, config, 0600); err != nil {
		t.Fatal(err)
	}
	created, err := (Factory{}).Create(context.Background(), plugin.InstanceContext{}, mustJSON(Config{ConfigPath: path}))
	if err != nil {
		t.Fatal(err)
	}
	if len(created.(*Instance).source["outbounds"]) == 0 {
		t.Fatal("config_path contents were not loaded")
	}
}

func TestRuntimeConfigAddsPrivateSOCKSInbound(t *testing.T) {
	instance := newTestInstance(t)
	encoded, err := instance.runtimeConfig(23456)
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Inbounds []struct {
			Listen   string         "json:\"listen\""
			Port     int            "json:\"port\""
			Protocol string         "json:\"protocol\""
			Settings map[string]any "json:\"settings\""
		} "json:\"inbounds\""
		Outbounds []json.RawMessage "json:\"outbounds\""
	}
	if err := json.Unmarshal(encoded, &config); err != nil {
		t.Fatal(err)
	}
	if len(config.Inbounds) != 1 || config.Inbounds[0].Listen != "127.0.0.1" || config.Inbounds[0].Port != 23456 || config.Inbounds[0].Protocol != "socks" {
		t.Fatalf("unexpected managed inbound: %+v", config.Inbounds)
	}
	if udp, ok := config.Inbounds[0].Settings["udp"].(bool); !ok || udp {
		t.Fatalf("expected UDP disabled, got %#v", config.Inbounds[0].Settings["udp"])
	}
	if len(config.Outbounds) != 1 {
		t.Fatalf("expected configured outbound to be preserved, got %d", len(config.Outbounds))
	}
}

func TestRuntimeConfigLoadsManagedInbound(t *testing.T) {
	instance := newTestInstance(t)
	encoded, err := instance.runtimeConfig(23456)
	if err != nil {
		t.Fatal(err)
	}
	config, err := xraycore.LoadConfig("json", bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Inbound) != 1 {
		t.Fatalf("expected one parsed Xray inbound, got %d", len(config.Inbound))
	}
	receiverMessage, err := config.Inbound[0].ReceiverSettings.GetInstance()
	if err != nil {
		t.Fatal(err)
	}
	receiver := receiverMessage.(*proxyman.ReceiverConfig)
	if receiver.Listen.AsAddress().String() != "127.0.0.1" || len(receiver.PortList.Ports()) != 1 || int(receiver.PortList.Ports()[0]) != 23456 {
		t.Fatalf("unexpected parsed listener: %v:%v", receiver.Listen, receiver.PortList)
	}
}

func TestStartAndDialContextUsesEmbeddedXray(t *testing.T) {
	target, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	go func() {
		for {
			conn, acceptErr := target.Accept()
			if acceptErr != nil {
				return
			}
			go func() {
				defer conn.Close()
				_, _ = io.Copy(conn, conn)
			}()
		}
	}()

	instance := newTestInstance(t)
	if err := instance.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := instance.Stop(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	port := instance.port
	if err := instance.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if instance.port != port {
		t.Fatalf("repeated Start changed the loopback port: %d != %d", instance.port, port)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	flow := netx.NewFlow("xray-test", "tcp", target.Addr().String(), "user_traffic")
	conn, err := instance.DialContext(ctx, flow)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("meshvianex")); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, len("meshvianex"))
	if _, err := io.ReadFull(conn, response); err != nil {
		t.Fatal(err)
	}
	if string(response) != "meshvianex" {
		t.Fatalf("unexpected response %q", response)
	}
}

func TestMultipleEmbeddedXrayInstancesRunAtATime(t *testing.T) {
	first := newTestInstance(t)
	if err := first.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Stop(context.Background()) }()

	second := newTestInstance(t)
	if err := second.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Stop(context.Background()) }()
}

func TestXrayWorkerProcess(t *testing.T) {
	if os.Getenv(WorkerModeEnv) != "1" {
		return
	}
	if err := RunWorker(os.Stdin, os.Stdout); err != nil {
		_, _ = fmt.Fprintf(os.Stdout, "ERROR: %v\n", err)
		t.Fatal(err)
	}
}

func newTestInstance(t *testing.T) *Instance {
	t.Helper()
	raw := mustJSON(Config{InlineConfig: mustJSON(map[string]any{
		"outbounds": []any{map[string]any{"protocol": "freedom", "tag": "meshvianex-test"}},
	})})
	created, err := (Factory{}).Create(context.Background(), plugin.InstanceContext{}, raw)
	if err != nil {
		t.Fatal(err)
	}
	return created.(*Instance)
}

func mustJSON(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}

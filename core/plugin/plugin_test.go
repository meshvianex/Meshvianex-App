package plugin

import (
	"context"
	"encoding/json"
	"testing"
)

type tf struct{}
type ti struct{}

type schemaFactory struct{}

func (tf) Manifest() Manifest {
	return Manifest{ID: "x", ABI: 1, RequiresCapabilities: []string{"socket.tcp"}}
}
func (tf) Create(context.Context, InstanceContext, json.RawMessage) (Instance, error) {
	return ti{}, nil
}
func (ti) Start(context.Context) error { return nil }
func (ti) Stop(context.Context) error  { return nil }

func (schemaFactory) Manifest() Manifest {
	return Manifest{ID: "schema", ABI: 1}
}
func (schemaFactory) Create(context.Context, InstanceContext, json.RawMessage) (Instance, error) {
	return ti{}, nil
}
func (schemaFactory) ConfigSchema() ConfigSchema {
	return ConfigSchema{Title: "Schema", Fields: []ConfigField{{ID: "endpoint", Type: "text"}}}
}

func TestCapabilityDenied(t *testing.T) {
	r := NewRegistry(CapabilitySet{})
	_ = r.Register(tf{})
	if _, err := r.Create(context.Background(), "x", InstanceContext{}, nil); err == nil {
		t.Fatal("expected capability denial")
	}
}

func TestConfigSchemaProvider(t *testing.T) {
	r := NewRegistry(CapabilitySet{})
	if err := r.Register(schemaFactory{}); err != nil {
		t.Fatal(err)
	}
	schema, ok := r.ConfigSchema("schema")
	if !ok || schema.Title != "Schema" || len(schema.Fields) != 1 {
		t.Fatalf("unexpected config schema: %+v", schema)
	}
}

type canonicalAliasFactory struct{}

func (canonicalAliasFactory) Manifest() Manifest {
	return Manifest{ID: "io.meshvianex.protocol.passthrough", ABI: 1, PluginTypes: []string{"protocol"}}
}
func (canonicalAliasFactory) Create(context.Context, InstanceContext, json.RawMessage) (Instance, error) {
	return ti{}, nil
}

func TestRegistryAcceptsPreReleasePluginAliasWithoutListingDuplicate(t *testing.T) {
	r := NewRegistry(CapabilitySet{})
	if err := r.Register(canonicalAliasFactory{}); err != nil {
		t.Fatal(err)
	}
	manifest, ok := r.Manifest("io.meshvianex.protocol.raw")
	if !ok || manifest.ID != "io.meshvianex.protocol.passthrough" {
		t.Fatalf("legacy alias did not resolve: manifest=%#v ok=%v", manifest, ok)
	}
	if _, err := r.Create(context.Background(), "io.meshvianex.protocol.raw", InstanceContext{}, nil); err != nil {
		t.Fatalf("legacy alias create failed: %v", err)
	}
	if got := r.List(); len(got) != 1 || got[0].ID != "io.meshvianex.protocol.passthrough" {
		t.Fatalf("registry exposed a duplicate legacy plugin: %#v", got)
	}
}

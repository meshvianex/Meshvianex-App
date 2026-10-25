package rules_basic

import (
	"context"
	"encoding/json"
	"meshvianex/core/netx"
	"meshvianex/core/plugin"
	"testing"
)

func TestSuffixRule(t *testing.T) {
	raw := json.RawMessage(`{"default":"direct","rules":[{"domain_suffix":".example","outbound":"proxy"}]}`)
	inst, err := (Factory{}).Create(context.Background(), plugin.InstanceContext{}, raw)
	if err != nil {
		t.Fatal(err)
	}
	d, err := inst.(*Instance).Select(context.Background(), netx.NewFlow("r", "tcp", "api.example:443", "user_traffic"))
	if err != nil || d.Outbound != "proxy" {
		t.Fatalf("decision=%+v err=%v", d, err)
	}
}

package transfer

import "testing"

func TestSmartPlanOnlyTransfersMissingObjects(t *testing.T) {
	p, err := BuildPlan(Smart, Manifest{Plugin: "demo", Version: "1", Objects: []string{"b", "a"}}, NewInventory([]string{"a"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Transfer) != 1 || p.Transfer[0] != "b" {
		t.Fatalf("unexpected plan: %#v", p.Transfer)
	}
}

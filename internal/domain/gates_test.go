package domain

import "testing"

func TestOrgParents(t *testing.T) {
	a, b, c, d := NewID(), NewID(), NewID(), NewID()
	// a <- b <- c ; d alone
	o := OrgParents{a: nil, b: &a, c: &b, d: nil}
	tests := []struct {
		name         string
		id, parent   ID
		wantCycle    bool
		ancestor, of ID
		wantDesc     bool
	}{
		{"self", a, a, true, a, a, false},
		{"child as parent", a, b, true, a, b, true},
		{"grandchild as parent", a, c, true, a, c, true},
		{"sibling tree is fine", c, d, false, d, c, false},
		{"move up", c, a, false, b, a, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := o.WouldCycle(tc.id, tc.parent); got != tc.wantCycle {
				t.Errorf("WouldCycle = %v, want %v", got, tc.wantCycle)
			}
			if got := o.IsDescendant(tc.ancestor, tc.of); got != tc.wantDesc {
				t.Errorf("IsDescendant = %v, want %v", got, tc.wantDesc)
			}
		})
	}
	if !o.CanDelegateTo(a, a) || !o.CanDelegateTo(a, c) || o.CanDelegateTo(c, a) || o.CanDelegateTo(b, d) {
		t.Error("CanDelegateTo")
	}
	// corrupt data (a loop that never reaches the target) must terminate
	loop := OrgParents{a: &b, b: &a}
	if loop.IsDescendant(d, a) || loop.WouldCycle(d, a) {
		t.Error("a loop elsewhere in the chart must not be reported against an unrelated persona")
	}
}

func TestGateEnums(t *testing.T) {
	if !GateApproval.Valid() || GateKind("x").Valid() || !GateApproval.Blocking() || GateTaskReview.Blocking() {
		t.Error("GateKind")
	}
	if !GateApproved.Decision() || GatePending.Decision() || GateExpired.Decision() || !GateExpired.Valid() {
		t.Error("GateStatus")
	}
}

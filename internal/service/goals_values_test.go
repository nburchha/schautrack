package service

import "testing"

func TestGoalSlotsAreKeyedAndOrdered(t *testing.T) {
	var v GoalValues
	slots := v.Slots()
	if len(slots) != len(GoalKeys) {
		t.Fatalf("got %d slots, want %d", len(slots), len(GoalKeys))
	}
	for i, s := range slots {
		if s.Key != GoalKeys[i] {
			t.Errorf("slot %d key = %q, want %q", i, s.Key, GoalKeys[i])
		}
		n := i + 1
		*s.Dst = &n
		if got := *v.Slot(s.Key); got == nil || *got != n {
			t.Errorf("Slot(%q) does not alias Slots()[%d]", s.Key, i)
		}
	}
	if v.Calories == nil || v.Sugar == nil || *v.Calories != 1 || *v.Sugar != 6 {
		t.Errorf("Slots did not write through to the struct fields: %+v", v)
	}
	if v.Slot("nope") != nil {
		t.Error("unknown key should have no slot")
	}
}

func TestGoalBounds(t *testing.T) {
	if lo, hi := GoalBounds("calories"); lo != 1 || hi != MaxGoalCalories {
		t.Errorf("calories bounds = %d..%d", lo, hi)
	}
	for _, k := range MacroKeys {
		if lo, hi := GoalBounds(k); lo != 0 || hi != MaxGoalMacro {
			t.Errorf("%s bounds = %d..%d", k, lo, hi)
		}
	}
}

func TestCaloriesEnabledAndMacroSet(t *testing.T) {
	cases := []struct {
		name string
		me   map[string]any
		want bool
	}{
		{"unset", map[string]any{}, true},
		{"true", map[string]any{"calories": true}, true},
		{"false", map[string]any{"calories": false}, false},
	}
	for _, c := range cases {
		if got := CaloriesEnabled(MacroUser{MacrosEnabled: c.me}); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
	set := EnabledMacroSet(MacroUser{MacrosEnabled: map[string]any{"protein": true, "fat": false}})
	if !set["protein"] || set["fat"] || len(set) != 1 {
		t.Errorf("unexpected set %v", set)
	}
}

package gtd

import (
	"testing"

	"github.com/kevinywlui/nix-config/apps/gtd/internal/todotxt"
)

func parseAll(lines ...string) []Item {
	var ts []todotxt.Task
	for _, l := range lines {
		if t, ok := todotxt.Parse(l); ok {
			ts = append(ts, t)
		}
	}
	return Items(ts)
}

func TestClassification(t *testing.T) {
	const today = "2026-06-15"
	items := parseAll(
		"Capture me @inbox",                   // 0 inbox
		"Call dentist @calls",                 // 1 next action
		"Email report @computer +Q3",          // 2 next action, project Q3
		"Waiting on invoice @waiting for:bob", // 3 waiting
		"Deferred thing @home t:2026-07-01",   // 4 dormant (future threshold)
		"x 2026-06-14 Done thing @errands",    // 5 done
		"Untagged floating task",              // 6 not a next action (no context)
	)

	if got := len(Inbox(items)); got != 1 {
		t.Errorf("Inbox count = %d, want 1", got)
	}
	if got := len(Waiting(items)); got != 1 {
		t.Errorf("Waiting count = %d, want 1", got)
	}
	na := NextActions(items, today, "")
	if got := len(na); got != 2 {
		t.Errorf("NextActions count = %d, want 2", got)
	}

	// The future-threshold task must not be a next action yet.
	if IsNextAction(items[4].Task, today) {
		t.Error("future-threshold task should not be a next action")
	}
	// ... but becomes one once the threshold passes.
	if !IsNextAction(items[4].Task, "2026-07-02") {
		t.Error("threshold task should be a next action after its t: date")
	}

	if got := len(NextActions(items, today, "calls")); got != 1 {
		t.Errorf("calls next actions = %d, want 1", got)
	}
}

func TestContextsAndProjects(t *testing.T) {
	const today = "2026-06-15"
	items := parseAll(
		"A @computer +site",
		"B @computer +site",
		"C @errands",
		"Stalled project has no action +oldproj @inbox", // inbox => not a next action
	)
	ctxs := Contexts(items, today)
	if len(ctxs) != 2 {
		t.Fatalf("Contexts = %v, want 2", ctxs)
	}
	if ctxs[0].Name != "computer" || ctxs[0].Count != 2 {
		t.Errorf("first context = %+v, want computer/2", ctxs[0])
	}

	projs := Projects(items, today)
	var site, old *Project
	for i := range projs {
		switch projs[i].Name {
		case "site":
			site = &projs[i]
		case "oldproj":
			old = &projs[i]
		}
	}
	if site == nil || site.Actions != 2 {
		t.Errorf("site project = %+v, want 2 actions", site)
	}
	if old == nil || old.Actions != 0 {
		t.Errorf("oldproj should be stalled (0 actions), got %+v", old)
	}
}

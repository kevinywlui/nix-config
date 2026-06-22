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
	na := NextActions(items, today, "", "")
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

	if got := len(NextActions(items, today, "calls", "")); got != 1 {
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
	if site.Parked() || site.Stalled() {
		t.Errorf("site has next actions; should be neither parked nor stalled: %+v", site)
	}
	if !old.Stalled() {
		t.Errorf("oldproj (only an @inbox task) should be stalled: %+v", old)
	}
}

// A task with after: pointing at an active task's id: is blocked (kept out of
// next actions) and unblocks once that prerequisite leaves the active list.
func TestBlockedDependencies(t *testing.T) {
	const today = "2026-06-15"

	// A is the prerequisite (id:k1); B waits on it (after:k1).
	items := parseAll("A @home id:k1 +Reno", "B @home after:k1 +Reno")
	na := NextActions(items, today, "", "")
	if len(na) != 1 || na[0].Task.Text != "A @home id:k1 +Reno" {
		t.Fatalf("while A is active, only A is a next action; got %v", na)
	}
	p := Projects(items, today)[0]
	if p.Actions != 1 || p.Blocked != 1 {
		t.Errorf("project should be 1 action + 1 blocked, got %+v", p)
	}
	if p.Stalled() || p.Parked() {
		t.Errorf("a project with an available action is neither stalled nor parked: %+v", p)
	}

	// Once A is done (no active task carries id:k1), B unblocks.
	done := parseAll("x 2026-06-15 A id:k1 +Reno", "B @home after:k1 +Reno")
	na = NextActions(done, today, "", "")
	if len(na) != 1 || na[0].Task.Text != "B @home after:k1 +Reno" {
		t.Fatalf("after A completes, B should be the next action; got %v", na)
	}
}

// Next actions come back ordered: due-dated first (earliest, so overdue leads),
// then undated in their original order.
func TestNextActionsSortedByUrgency(t *testing.T) {
	const today = "2026-06-15"
	items := parseAll(
		"undated first @computer",
		"due next week @computer due:2026-06-22",
		"overdue @calls due:2026-06-01",
		"undated second @home",
		"due today @errands due:2026-06-15",
	)
	na := NextActions(items, today, "", "")
	got := make([]string, len(na))
	for i, it := range na {
		got[i] = it.Task.Tag("due")
	}
	// overdue, due-today, due-next-week, then the two undated (stable order).
	want := []string{"2026-06-01", "2026-06-15", "2026-06-22", "", ""}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("urgency order = %v, want %v", got, want)
		}
	}
	if na[3].Task.Text != "undated first @computer" || na[4].Task.Text != "undated second @home" {
		t.Errorf("undated actions should keep original order, got %q then %q", na[3].Task.Text, na[4].Task.Text)
	}
}

// LandscapeFor buckets next actions by tickler-activation and hard-due date.
func TestLandscapeFor(t *testing.T) {
	const today, horizon = "2026-06-15", "2026-06-22"
	items := parseAll(
		"woke today @home t:2026-06-15",                 // activated (t == today)
		"overdue bill @calls due:2026-06-10",            // overdue
		"due now @computer due:2026-06-15",              // due today
		"soon @errands due:2026-06-20",                  // due soon (within horizon)
		"later @home due:2026-07-30",                    // beyond horizon -> no bucket
		"deferred @home t:2099-01-01 due:2026-06-10",    // dormant: not a next action, excluded
		"x 2026-06-15 done @calls due:2026-06-01",       // done: excluded
	)
	l := LandscapeFor(items, today, horizon)
	if len(l.Activated) != 1 || l.Activated[0].Task.Text != "woke today @home t:2026-06-15" {
		t.Errorf("Activated = %v, want the t:today task", l.Activated)
	}
	if len(l.Overdue) != 1 || l.Overdue[0].Task.Tag("due") != "2026-06-10" {
		t.Errorf("Overdue = %v, want the 06-10 task", l.Overdue)
	}
	if len(l.DueToday) != 1 {
		t.Errorf("DueToday = %v, want 1", l.DueToday)
	}
	if len(l.DueSoon) != 1 || l.DueSoon[0].Task.Tag("due") != "2026-06-20" {
		t.Errorf("DueSoon = %v, want the 06-20 task", l.DueSoon)
	}
	if l.Total() != 4 || l.Empty() {
		t.Errorf("Total = %d (empty=%v), want 4", l.Total(), l.Empty())
	}
}

// GroupByContext lists each next action under every real context it carries.
func TestGroupByContext(t *testing.T) {
	const today = "2026-06-15"
	items := parseAll(
		"A @computer",
		"B @computer @errands", // appears under both
		"C @calls",
	)
	groups := GroupByContext(NextActions(items, today, "", ""))
	got := map[string]int{}
	for _, g := range groups {
		got[g.Name] = len(g.Items)
	}
	if got["computer"] != 2 || got["errands"] != 1 || got["calls"] != 1 {
		t.Errorf("group counts = %v, want computer:2 errands:1 calls:1", got)
	}
	// First-seen order: computer, errands, calls.
	if len(groups) != 3 || groups[0].Name != "computer" || groups[2].Name != "calls" {
		t.Errorf("group order = %v, want computer,errands,calls", groups)
	}
}

// A project with no next action is "stalled" only if nothing is waiting or
// deferred; a waiting-only or deferred-only project is "parked", not stalled.
func TestProjectParkedVsStalled(t *testing.T) {
	const today = "2026-06-15"
	items := parseAll(
		"call the vendor @waiting for:acme +deal",   // waiting-only
		"kick off later t:2099-01-01 @home +reno",   // deferred-only
		"abandoned thing +ghost",                    // no context => genuinely stuck
	)
	got := map[string]Project{}
	for _, p := range Projects(items, today) {
		got[p.Name] = p
	}
	if p := got["deal"]; !p.Parked() || p.Stalled() || p.Waiting != 1 {
		t.Errorf("deal should be parked (waiting), got %+v", p)
	}
	if p := got["reno"]; !p.Parked() || p.Stalled() || p.Deferred != 1 {
		t.Errorf("reno should be parked (deferred), got %+v", p)
	}
	if p := got["ghost"]; !p.Stalled() {
		t.Errorf("ghost should be stalled, got %+v", p)
	}
}

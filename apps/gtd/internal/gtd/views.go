// Package gtd layers Getting Things Done semantics onto plain todo.txt tasks.
//
// Conventions (see apps/gtd/README.md for the full rationale):
//   - @inbox        unprocessed capture; cleared during Clarify.
//   - @waiting      delegated / awaiting someone else (pair with for:<person>).
//   - a task with a real @context and a reached threshold IS a next action.
//   - t:YYYY-MM-DD  threshold/start date: the task is dormant until then.
//   - due:YYYY-MM-DD hard due date (the "hard landscape").
//   - +project      a multi-step outcome. Someday/Maybe lives in someday.txt,
//     reference material in reference.txt — separate files, excluded from the
//     active lists by construction.
//
// Note we deliberately do NOT use the (A) priority to mean "next action":
// priority in GTD is contextual and decided at engagement time, and the
// todo.txt spec drops priority on completion, which would make that meaning
// lossy. Next-action-ness is derived from "has a context and is actionable now".
package gtd

import (
	"sort"

	"github.com/kevinywlui/nix-config/apps/gtd/internal/todotxt"
)

// Reserved context names with special GTD meaning.
const (
	ContextInbox   = "inbox"
	ContextWaiting = "waiting"
)

// Item pairs a task with its stable ID (its index in the active file).
type Item struct {
	ID   int
	Task todotxt.Task
}

// Items wraps a slice of tasks as identified Items, ID = slice index.
func Items(tasks []todotxt.Task) []Item {
	out := make([]Item, len(tasks))
	for i, t := range tasks {
		out[i] = Item{ID: i, Task: t}
	}
	return out
}

func reserved(name string) bool {
	return name == ContextInbox || name == ContextWaiting
}

// thresholdReached reports whether a task's t: start date (if any) has arrived.
// ISO-8601 dates compare correctly as strings.
func thresholdReached(t todotxt.Task, today string) bool {
	th := t.Tag("t")
	return th == "" || th <= today
}

// IsInbox reports an unprocessed capture.
func IsInbox(t todotxt.Task) bool { return t.HasContext(ContextInbox) }

// IsWaiting reports a delegated / awaiting item.
func IsWaiting(t todotxt.Task) bool { return t.HasContext(ContextWaiting) }

// HasRealContext reports whether the task carries at least one non-reserved
// @context — the contexts a next action can live on. Used when restoring a
// completed task to decide whether it can rejoin the next-actions lists or
// should fall back to the inbox.
func HasRealContext(t todotxt.Task) bool { return len(realContexts(t)) > 0 }

// ActiveIDs returns the set of id: tags carried by the not-done items — the
// prerequisites a task's after: tag can still be waiting on.
func ActiveIDs(items []Item) map[string]bool {
	ids := map[string]bool{}
	for _, it := range items {
		if it.Task.Done {
			continue
		}
		if id := it.Task.Tag("id"); id != "" {
			ids[id] = true
		}
	}
	return ids
}

// IsBlocked reports a task whose after: prerequisite is still an active task.
// Once that prerequisite is completed (and leaves the active list) its id is no
// longer in activeIDs, so the task unblocks automatically. A dangling after:
// (prerequisite never existed or already gone) is treated as satisfied.
func IsBlocked(t todotxt.Task, activeIDs map[string]bool) bool {
	a := t.Tag("after")
	return a != "" && activeIDs[a]
}

// realContexts returns the task's contexts excluding the reserved ones.
func realContexts(t todotxt.Task) []string {
	var out []string
	for _, c := range t.Contexts() {
		if !reserved(c) {
			out = append(out, c)
		}
	}
	return out
}

// IsNextAction reports an actionable, processed, non-delegated task whose
// threshold has arrived and which is parked on at least one real context.
func IsNextAction(t todotxt.Task, today string) bool {
	return !t.Done && !IsInbox(t) && !IsWaiting(t) &&
		thresholdReached(t, today) && len(realContexts(t)) > 0
}

// Inbox returns the unprocessed items.
func Inbox(items []Item) []Item {
	return filter(items, func(t todotxt.Task) bool { return !t.Done && IsInbox(t) })
}

// Waiting returns the delegated / awaiting items.
func Waiting(items []Item) []Item {
	return filter(items, func(t todotxt.Task) bool { return !t.Done && IsWaiting(t) })
}

// NextActions returns next actions, optionally filtered to a context and/or a
// project (each empty string means "don't filter on that dimension"). When both
// are set the filters intersect. Tasks blocked by an unfinished prerequisite
// (after:) are excluded — they aren't actionable yet.
func NextActions(items []Item, today, context, project string) []Item {
	ids := ActiveIDs(items)
	out := filter(items, func(t todotxt.Task) bool {
		if !IsNextAction(t, today) || IsBlocked(t, ids) {
			return false
		}
		if context != "" && !t.HasContext(context) {
			return false
		}
		if project != "" && !t.HasProject(project) {
			return false
		}
		return true
	})
	sortByUrgency(out)
	return out
}

// sortByUrgency orders next actions for the Engage view: anything with a hard
// due date comes first, earliest date at the top (so overdue items lead);
// undated actions keep their original relative order after them. Stable, so equal
// keys never shuffle and the order stays deterministic for the JSON API too.
func sortByUrgency(items []Item) {
	sort.SliceStable(items, func(i, j int) bool {
		di, dj := items[i].Task.Tag("due"), items[j].Task.Tag("due")
		if (di == "") != (dj == "") {
			return di != "" // a dated action sorts before an undated one
		}
		return di < dj // both dated: ascending; both undated: equal (keep order)
	})
}

// Context is a context name with the count of next actions parked on it.
type Context struct {
	Name  string
	Count int
}

// Contexts returns the distinct real contexts across current next actions,
// each with its action count, in stable (insertion) order.
func Contexts(items []Item, today string) []Context {
	ids := ActiveIDs(items)
	order := []string{}
	count := map[string]int{}
	for _, it := range items {
		if !IsNextAction(it.Task, today) || IsBlocked(it.Task, ids) {
			continue
		}
		for _, c := range realContexts(it.Task) {
			if _, seen := count[c]; !seen {
				order = append(order, c)
			}
			count[c]++
		}
	}
	out := make([]Context, len(order))
	for i, c := range order {
		out[i] = Context{Name: c, Count: count[c]}
	}
	return out
}

// Project is a project name with a breakdown of its active (not-done) tasks:
// Actions (next actions advancing it now), Waiting (delegated/awaiting),
// Deferred (a real next step dormant until its t: date), and Blocked (waiting on
// a prerequisite via after:). The split lets a review tell these states apart —
// see Stalled/Parked.
type Project struct {
	Name     string
	Actions  int
	Waiting  int
	Deferred int
	Blocked  int
}

// Stalled reports a project that genuinely needs attention: it has active tasks
// but not one is a next action, waiting, deferred, or blocked on a prerequisite
// — so nothing will ever move it. This is the highest-value signal a GTD review
// surfaces. (A project whose tasks are all done simply drops off the list.)
func (p Project) Stalled() bool {
	return p.Actions == 0 && p.Waiting == 0 && p.Deferred == 0 && p.Blocked == 0
}

// Parked reports a project with no next action right now but which is legitimately
// in motion — waiting on someone, deferred to a date, or blocked on a
// prerequisite step. Amber, not red.
func (p Project) Parked() bool { return p.Actions == 0 && !p.Stalled() }

// Projects returns the distinct +projects across all active (not-done) tasks,
// each annotated with its next/waiting/deferred/blocked task counts.
func Projects(items []Item, today string) []Project {
	ids := ActiveIDs(items)
	order := []string{}
	acc := map[string]*Project{}
	for _, it := range items {
		t := it.Task
		if t.Done {
			continue
		}
		for _, name := range t.Projects() {
			p := acc[name]
			if p == nil {
				p = &Project{Name: name}
				acc[name] = p
				order = append(order, name)
			}
			switch {
			case IsBlocked(t, ids):
				p.Blocked++
			case IsNextAction(t, today):
				p.Actions++
			case IsWaiting(t):
				p.Waiting++
			case !IsInbox(t) && len(realContexts(t)) > 0 && !thresholdReached(t, today):
				p.Deferred++
			}
		}
	}
	out := make([]Project, len(order))
	for i, name := range order {
		out[i] = *acc[name]
	}
	return out
}

// Landscape buckets the time-sensitive next actions a daily dashboard and the
// weekly review need to surface: ones whose tickler (t:) date is today — they've
// just woken from the tickler and would otherwise rejoin Next with no signal —
// and the hard-due buckets relative to today. Only actionable, unblocked next
// actions are considered (the things you can actually act on).
type Landscape struct {
	Activated []Item // t: == today: just emerged from the tickler
	Overdue   []Item // due: < today
	DueToday  []Item // due: == today
	DueSoon   []Item // today < due: <= the horizon (typically a week out)
}

// Total is the number of items across every bucket; Empty reports none, so a
// dashboard can decide whether to show the hard-landscape banner at all.
func (l Landscape) Total() int {
	return len(l.Activated) + len(l.Overdue) + len(l.DueToday) + len(l.DueSoon)
}

// Empty reports a landscape with nothing time-sensitive to surface.
func (l Landscape) Empty() bool { return l.Total() == 0 }

// LandscapeFor computes the hard landscape as of today. horizon is the ISO date
// bounding DueSoon (the caller, which has a clock, passes today+7 or similar);
// ISO-8601 dates compare correctly as plain strings, so this stays a pure
// derivation with no time dependency. Each due bucket is sorted by date ascending.
func LandscapeFor(items []Item, today, horizon string) Landscape {
	ids := ActiveIDs(items)
	var l Landscape
	for _, it := range items {
		t := it.Task
		if !IsNextAction(t, today) || IsBlocked(t, ids) {
			continue
		}
		if t.Tag("t") == today {
			l.Activated = append(l.Activated, it)
		}
		switch due := t.Tag("due"); {
		case due == "":
		case due < today:
			l.Overdue = append(l.Overdue, it)
		case due == today:
			l.DueToday = append(l.DueToday, it)
		case due <= horizon:
			l.DueSoon = append(l.DueSoon, it)
		}
	}
	sortByDue(l.Overdue)
	sortByDue(l.DueToday)
	sortByDue(l.DueSoon)
	return l
}

// sortByDue orders items by their due: date ascending (stable).
func sortByDue(items []Item) {
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].Task.Tag("due") < items[j].Task.Tag("due")
	})
}

// ContextGroup is a context name with the next actions parked on it, for the
// grouped Next view.
type ContextGroup struct {
	Name  string
	Items []Item
}

// GroupByContext arranges already-selected next actions under each real context
// they carry, in first-seen order, so the unfiltered Next page can show
// per-context subheadings instead of one long list. An action on two contexts
// appears under both (it genuinely belongs to each). Input is assumed already
// filtered to next actions and ordered as the caller wants within each group.
func GroupByContext(actions []Item) []ContextGroup {
	idx := map[string]int{}
	var groups []ContextGroup
	for _, it := range actions {
		for _, c := range realContexts(it.Task) {
			i, ok := idx[c]
			if !ok {
				i = len(groups)
				idx[c] = i
				groups = append(groups, ContextGroup{Name: c})
			}
			groups[i].Items = append(groups[i].Items, it)
		}
	}
	return groups
}

func filter(items []Item, pred func(todotxt.Task) bool) []Item {
	var out []Item
	for _, it := range items {
		if pred(it.Task) {
			out = append(out, it)
		}
	}
	return out
}

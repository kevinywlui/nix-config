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

import "github.com/kevinywlui/nix-config/apps/gtd/internal/todotxt"

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

// NextActions returns next actions; if context != "" it filters to that context.
func NextActions(items []Item, today, context string) []Item {
	return filter(items, func(t todotxt.Task) bool {
		if !IsNextAction(t, today) {
			return false
		}
		return context == "" || t.HasContext(context)
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
	order := []string{}
	count := map[string]int{}
	for _, it := range items {
		if !IsNextAction(it.Task, today) {
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

// Project is a project name with the count of associated next actions. A count
// of zero marks a stalled project (one with no next action) — the highest-value
// signal a GTD review can surface.
type Project struct {
	Name    string
	Actions int
}

// Projects returns the distinct +projects across all active (not-done) tasks,
// each annotated with how many current next actions advance it.
func Projects(items []Item, today string) []Project {
	order := []string{}
	actions := map[string]int{}
	seen := map[string]bool{}
	for _, it := range items {
		if it.Task.Done {
			continue
		}
		next := IsNextAction(it.Task, today)
		for _, p := range it.Task.Projects() {
			if !seen[p] {
				seen[p] = true
				order = append(order, p)
				actions[p] = 0
			}
			if next {
				actions[p]++
			}
		}
	}
	out := make([]Project, len(order))
	for i, p := range order {
		out[i] = Project{Name: p, Actions: actions[p]}
	}
	return out
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

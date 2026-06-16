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

// HasRealContext reports whether the task carries at least one non-reserved
// @context — the contexts a next action can live on. Used when restoring a
// completed task to decide whether it can rejoin the next-actions lists or
// should fall back to the inbox.
func HasRealContext(t todotxt.Task) bool { return len(realContexts(t)) > 0 }

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
// are set the filters intersect.
func NextActions(items []Item, today, context, project string) []Item {
	return filter(items, func(t todotxt.Task) bool {
		if !IsNextAction(t, today) {
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

// Project is a project name with a breakdown of its active (not-done) tasks:
// Actions (next actions advancing it), Waiting (delegated/awaiting), and
// Deferred (a real next step, just dormant until its t: date). The split lets a
// review tell three very different states apart — see Stalled/Parked.
type Project struct {
	Name     string
	Actions  int
	Waiting  int
	Deferred int
}

// Stalled reports a project that genuinely needs attention: it has active tasks
// but not one of them is a next action, a waiting item, or a deferred step — so
// nothing will ever move it. This is the highest-value signal a GTD review
// surfaces. (A project whose tasks are all done simply drops off the list.)
func (p Project) Stalled() bool { return p.Actions == 0 && p.Waiting == 0 && p.Deferred == 0 }

// Parked reports a project with no next action right now but which is legitimately
// in motion — waiting on someone or deferred to a future date. Amber, not red.
func (p Project) Parked() bool { return p.Actions == 0 && !p.Stalled() }

// Projects returns the distinct +projects across all active (not-done) tasks,
// each annotated with its next/waiting/deferred task counts.
func Projects(items []Item, today string) []Project {
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

func filter(items []Item, pred func(todotxt.Task) bool) []Item {
	var out []Item
	for _, it := range items {
		if pred(it.Task) {
			out = append(out, it)
		}
	}
	return out
}

// Package history owns the on-disk format for a durable record of one
// engine.Execute call: a per-run directory containing a single
// append-only events.jsonl file, written incrementally so a process killed
// mid-run leaves everything that resolved before the kill genuinely
// readable from disk afterward.
package history

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/Ordivyn/ordivyn/internal/engine"
)

// eventsFileName is the one file every run directory contains.
const eventsFileName = "events.jsonl"

// runIDLayout is a nanosecond-precision UTC timestamp: sortable, and, for a
// single local CLI process running one workflow at a time, unique in
// practice. See New's collision retry for the rare case it isn't.
const runIDLayout = "20060102T150405.000000000Z"

// Run is one durable, on-disk record of one engine.Execute call, appended
// to incrementally — never buffered in memory and flushed once — so a
// process killed mid-run leaves everything that resolved before the kill
// genuinely readable from disk afterward.
type Run struct {
	dir      string // e.g. .ordivyn/runs/20260906T153000.123456789Z
	f        *os.File
	warnings []string // one entry per swallowed failure; see Warnings()
}

type runStartedEvent struct {
	Event     string   `json:"event"` // "run_started"
	StartedAt string   `json:"started_at"`
	Workflow  string   `json:"workflow"`
	Limit     int      `json:"limit"`
	Nodes     []string `json:"nodes"` // every declared node id, sorted
}

type nodeStartedEvent struct {
	Event     string `json:"event"` // "node_started"
	NodeID    string `json:"node_id"`
	StartedAt string `json:"started_at"`
}

type nodeResultEvent struct {
	Event      string `json:"event"` // "node_result"
	NodeID     string `json:"node_id"`
	Status     string `json:"status"` // "ok" | "failed" | "skipped"
	Err        string `json:"err,omitempty"`
	Output     any    `json:"output,omitempty"` // res.Output, marshaled as-is
	ResolvedAt string `json:"resolved_at"`      // pairs with node_started's started_at; no precomputed duration field
}

type runFinishedEvent struct {
	Event      string `json:"event"` // "run_finished"
	FinishedAt string `json:"finished_at"`
	OK         int    `json:"ok"`
	Failed     int    `json:"failed"`
	Skipped    int    `json:"skipped"`
	Error      string `json:"error,omitempty"` // Execute's own returned error, if any
}

// New creates root/<generated id>/, opens its events.jsonl for append, and
// writes one run_started line naming workflowPath, limit, and every node
// id in g (sorted) — so even a record that captures zero node_result lines
// still names what the run was supposed to do. A non-nil error here means
// no directory could be created or the first line couldn't be written;
// the caller decides whether to run anyway with a nil observer.
func New(root, workflowPath string, limit int, g engine.Graph) (*Run, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("history: resolving root %q: %w", root, err)
	}
	if err := os.MkdirAll(absRoot, 0o755); err != nil {
		return nil, fmt.Errorf("history: creating root %q: %w", root, err)
	}

	base := time.Now().UTC().Format(runIDLayout)
	const maxAttempts = 8
	var dir string
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		id := base
		if attempt > 1 {
			id = fmt.Sprintf("%s-%d", base, attempt)
		}
		candidate := filepath.Join(absRoot, id)
		if err := os.Mkdir(candidate, 0o755); err != nil {
			if os.IsExist(err) && attempt < maxAttempts {
				continue
			}
			return nil, fmt.Errorf("history: creating run directory: %w", err)
		}
		dir = candidate
		break
	}

	f, err := os.OpenFile(filepath.Join(dir, eventsFileName), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("history: opening events file: %w", err)
	}

	r := &Run{dir: dir, f: f}

	ids := make([]string, 0, len(g.Nodes))
	for _, n := range g.Nodes {
		ids = append(ids, string(n.ID))
	}
	sort.Strings(ids)

	if err := r.writeLine(runStartedEvent{
		Event:     "run_started",
		StartedAt: nowString(),
		Workflow:  workflowPath,
		Limit:     limit,
		Nodes:     ids,
	}); err != nil {
		f.Close()
		return nil, fmt.Errorf("history: writing run_started event: %w", err)
	}

	return r, nil
}

// OnNodeStart appends one node_started line. Bound to a specific *Run,
// this is the literal value cmd/ordivyn passes as engine.Execute's new
// onStart parameter. Same never-returns-an-error, never-panics contract as
// OnNodeResult, for the same reason: engine's onStart has nowhere to send
// one either.
func (r *Run) OnNodeStart(id engine.NodeID) {
	if err := r.writeLine(nodeStartedEvent{
		Event:     "node_started",
		NodeID:    string(id),
		StartedAt: nowString(),
	}); err != nil {
		r.warn(fmt.Sprintf("node_started for %q: %v", id, err))
	}
}

// OnNodeResult appends one node_result line. Bound to a specific *Run,
// this is the literal value cmd/ordivyn passes as engine.Execute's new
// onResult parameter. Never returns an error and never panics: a write
// failure is recorded via warn and otherwise ignored, because the
// function this satisfies — engine's onResult — has nowhere to send an
// error, by design.
func (r *Run) OnNodeResult(res engine.Result) {
	ev := nodeResultEvent{
		Event:      "node_result",
		NodeID:     string(res.NodeID),
		Status:     statusString(res.Status),
		Output:     res.Output,
		ResolvedAt: nowString(),
	}
	if res.Err != nil {
		ev.Err = res.Err.Error()
	}
	if err := r.writeLine(ev); err != nil {
		r.warn(fmt.Sprintf("node_result for %q: %v", res.NodeID, err))
	}
}

// Finish appends one run_finished line (counts derived from results, plus
// execErr's message if Execute itself returned an error) and closes
// events.jsonl. Called exactly once, after Execute returns, whatever it
// returned. Its own returned error is informational only — the caller
// logs it, never treats it as run failure.
func (r *Run) Finish(results map[engine.NodeID]engine.Result, execErr error) error {
	var ok, failed, skipped int
	for _, res := range results {
		switch res.Status {
		case engine.StatusOK:
			ok++
		case engine.StatusFailed:
			failed++
		case engine.StatusSkipped:
			skipped++
		}
	}

	ev := runFinishedEvent{
		Event:      "run_finished",
		FinishedAt: nowString(),
		OK:         ok,
		Failed:     failed,
		Skipped:    skipped,
	}
	if execErr != nil {
		ev.Error = execErr.Error()
	}

	writeErr := r.writeLine(ev)
	closeErr := r.f.Close()
	if writeErr != nil {
		return fmt.Errorf("history: writing run_finished event: %w", writeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("history: closing events file: %w", closeErr)
	}
	return nil
}

// EventsPath returns the absolute path to this run's events.jsonl, for
// the caller to print or open.
func (r *Run) EventsPath() string {
	return filepath.Join(r.dir, eventsFileName)
}

// Warnings returns every write failure OnNodeResult/Finish swallowed, one
// entry per failure, in the order they happened. Not capped: a run's node
// count is already small (tens, not millions), so the number of possible
// OnNodeResult calls, and therefore warnings, is bounded by the graph
// itself. A cap would be solving an overflow that can't happen.
func (r *Run) Warnings() []string {
	return r.warnings
}

func (r *Run) warn(msg string) {
	r.warnings = append(r.warnings, msg)
}

// writeLine marshals v and appends it as one line to events.jsonl via a
// single unbuffered os.File.Write (no bufio.Writer, nothing to flush):
// each event is small and infrequent, so buffering would add a "did I
// flush before the kill" question this design doesn't need to answer. A
// plain Write reaching the kernel's own page cache already survives the
// process dying — the failure mode this package exists for — without
// needing fsync, which would additionally survive an OS crash/power-loss
// nothing here asks it to survive.
func (r *Run) writeLine(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	_, err = r.f.Write(b)
	return err
}

func nowString() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

// statusString mirrors cmd/ordivyn's own private statusString: kept as a
// second copy rather than a shared engine.Status.String() method, because
// adding that method isn't required to solve this package's actual
// problem.
func statusString(s engine.Status) string {
	switch s {
	case engine.StatusOK:
		return "ok"
	case engine.StatusFailed:
		return "failed"
	case engine.StatusSkipped:
		return "skipped"
	default:
		return "unknown"
	}
}

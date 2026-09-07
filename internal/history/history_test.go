package history

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Ordivyn/ordivyn/internal/engine"
)

// readEvents reads path back as newline-delimited JSON objects, one map
// per line, in file order.
func readEvents(t *testing.T, path string) []map[string]any {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("opening %s: %v", path, err)
	}
	defer f.Close()

	var events []map[string]any
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(line, &m); err != nil {
			t.Fatalf("line %q is not valid JSON: %v", line, err)
		}
		events = append(events, m)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scanning %s: %v", path, err)
	}
	return events
}

func TestNew_WritesRunStartedEventWithSortedNodeIDs(t *testing.T) {
	g := engine.Graph{Nodes: []engine.Node{
		{ID: "zeta"},
		{ID: "alpha"},
		{ID: "mid"},
	}}
	r, err := New(t.TempDir(), "workflow.yaml", 4, g)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := r.Finish(nil, nil); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	events := readEvents(t, r.EventsPath())
	if len(events) == 0 {
		t.Fatal("no events written")
	}
	first := events[0]
	if first["event"] != "run_started" {
		t.Fatalf("first event = %v, want run_started", first["event"])
	}
	nodesAny, ok := first["nodes"].([]any)
	if !ok {
		t.Fatalf("nodes field is %T, want []any", first["nodes"])
	}
	var got []string
	for _, n := range nodesAny {
		got = append(got, n.(string))
	}
	want := []string{"alpha", "mid", "zeta"}
	if len(got) != len(want) {
		t.Fatalf("nodes = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("nodes = %v, want %v", got, want)
		}
	}
}

func TestOnNodeStart_AppendsOneLinePerCall(t *testing.T) {
	g := engine.Graph{Nodes: []engine.Node{{ID: "a"}, {ID: "b"}, {ID: "c"}}}
	r, err := New(t.TempDir(), "workflow.yaml", 1, g)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ids := []engine.NodeID{"a", "b", "c"}
	for _, id := range ids {
		r.OnNodeStart(id)
	}
	if err := r.Finish(nil, nil); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	events := readEvents(t, r.EventsPath())
	var started []map[string]any
	for _, e := range events {
		if e["event"] == "node_started" {
			started = append(started, e)
		}
	}
	if len(started) != 3 {
		t.Fatalf("got %d node_started lines, want 3", len(started))
	}
	for i, id := range ids {
		if started[i]["node_id"] != string(id) {
			t.Errorf("node_started[%d].node_id = %v, want %q", i, started[i]["node_id"], id)
		}
	}
}

func TestOnNodeResult_AppendsOneLinePerCall(t *testing.T) {
	g := engine.Graph{Nodes: []engine.Node{{ID: "a"}, {ID: "b"}, {ID: "c"}}}
	r, err := New(t.TempDir(), "workflow.yaml", 1, g)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	results := []engine.Result{
		{NodeID: "a", Status: engine.StatusOK},
		{NodeID: "b", Status: engine.StatusFailed, Err: errors.New("boom")},
		{NodeID: "c", Status: engine.StatusSkipped},
	}
	for _, res := range results {
		r.OnNodeResult(res)
	}
	if err := r.Finish(nil, nil); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	events := readEvents(t, r.EventsPath())
	var got []map[string]any
	for _, e := range events {
		if e["event"] == "node_result" {
			got = append(got, e)
		}
	}
	if len(got) != 3 {
		t.Fatalf("got %d node_result lines, want 3", len(got))
	}
	for i, res := range results {
		if got[i]["node_id"] != string(res.NodeID) {
			t.Errorf("node_result[%d].node_id = %v, want %q", i, got[i]["node_id"], res.NodeID)
		}
	}
}

func TestOnNodeResult_MarshalsArbitraryOutputGenerically(t *testing.T) {
	g := engine.Graph{Nodes: []engine.Node{{ID: "a"}}}
	r, err := New(t.TempDir(), "workflow.yaml", 1, g)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	type localOutput struct{ Foo string }
	r.OnNodeResult(engine.Result{NodeID: "a", Status: engine.StatusOK, Output: localOutput{Foo: "bar"}})
	if err := r.Finish(nil, nil); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	events := readEvents(t, r.EventsPath())
	var resultEvent map[string]any
	for _, e := range events {
		if e["event"] == "node_result" {
			resultEvent = e
		}
	}
	if resultEvent == nil {
		t.Fatal("no node_result event found")
	}
	output, ok := resultEvent["output"].(map[string]any)
	if !ok {
		t.Fatalf("output field is %T, want map[string]any", resultEvent["output"])
	}
	if output["Foo"] != "bar" {
		t.Errorf("output.Foo = %v, want %q", output["Foo"], "bar")
	}
	if len(r.Warnings()) != 0 {
		t.Errorf("Warnings() = %v, want none", r.Warnings())
	}
}

func TestOnNodeResult_NonMarshalableOutputWarnsAndSkipsLineWithoutPanicking(t *testing.T) {
	g := engine.Graph{Nodes: []engine.Node{{ID: "a"}}}
	r, err := New(t.TempDir(), "workflow.yaml", 1, g)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	func() {
		defer func() {
			if p := recover(); p != nil {
				t.Fatalf("OnNodeResult panicked: %v", p)
			}
		}()
		r.OnNodeResult(engine.Result{NodeID: "a", Status: engine.StatusOK, Output: func() {}})
	}()

	if err := r.Finish(nil, nil); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	events := readEvents(t, r.EventsPath())
	for _, e := range events {
		if e["event"] == "node_result" {
			t.Errorf("expected no node_result line for a non-marshalable Output, got %v", e)
		}
	}
	if len(r.Warnings()) != 1 {
		t.Fatalf("Warnings() = %v, want exactly 1 entry", r.Warnings())
	}
}

func TestOnNodeResult_WriteFailureIsRecordedNotPanicked(t *testing.T) {
	g := engine.Graph{Nodes: []engine.Node{{ID: "a"}}}
	r, err := New(t.TempDir(), "workflow.yaml", 1, g)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Close the underlying file out from under the Run so the next write
	// fails, without OnNodeResult having any way to know in advance.
	if err := r.f.Close(); err != nil {
		t.Fatalf("closing underlying file: %v", err)
	}

	func() {
		defer func() {
			if p := recover(); p != nil {
				t.Fatalf("OnNodeResult panicked: %v", p)
			}
		}()
		r.OnNodeResult(engine.Result{NodeID: "a", Status: engine.StatusOK})
	}()

	if len(r.Warnings()) != 1 {
		t.Fatalf("Warnings() = %v, want exactly 1 entry", r.Warnings())
	}
}

func TestFinish_WritesFinishedEventWithCorrectCountsAndClosesFile(t *testing.T) {
	g := engine.Graph{Nodes: []engine.Node{{ID: "a"}, {ID: "b"}, {ID: "c"}}}
	r, err := New(t.TempDir(), "workflow.yaml", 1, g)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	results := map[engine.NodeID]engine.Result{
		"a": {NodeID: "a", Status: engine.StatusOK},
		"b": {NodeID: "b", Status: engine.StatusFailed, Err: errors.New("boom")},
		"c": {NodeID: "c", Status: engine.StatusSkipped},
	}
	if err := r.Finish(results, nil); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	events := readEvents(t, r.EventsPath())
	var finished map[string]any
	for _, e := range events {
		if e["event"] == "run_finished" {
			finished = e
		}
	}
	if finished == nil {
		t.Fatal("no run_finished event found")
	}
	if finished["ok"] != float64(1) || finished["failed"] != float64(1) || finished["skipped"] != float64(1) {
		t.Errorf("run_finished counts = %v, want ok=1 failed=1 skipped=1", finished)
	}

	// The file is now closed; a follow-up write must be caught as a
	// warning, never a panic.
	func() {
		defer func() {
			if p := recover(); p != nil {
				t.Fatalf("OnNodeResult panicked after Finish: %v", p)
			}
		}()
		r.OnNodeResult(engine.Result{NodeID: "late", Status: engine.StatusOK})
	}()
	if len(r.Warnings()) != 1 {
		t.Fatalf("Warnings() after post-Finish write = %v, want exactly 1 entry", r.Warnings())
	}
}

func TestNew_TwoRunsGetDistinctDirectoriesEvenCalledBackToBack(t *testing.T) {
	root := t.TempDir()
	g := engine.Graph{Nodes: []engine.Node{{ID: "a"}}}
	r1, err := New(root, "workflow.yaml", 1, g)
	if err != nil {
		t.Fatalf("New (1st): %v", err)
	}
	r2, err := New(root, "workflow.yaml", 1, g)
	if err != nil {
		t.Fatalf("New (2nd): %v", err)
	}
	if r1.dir == r2.dir {
		t.Errorf("both runs got the same dir: %q", r1.dir)
	}
	r1.Finish(nil, nil)
	r2.Finish(nil, nil)
}

func TestNew_ReturnsErrorWhenRootCannotBeCreated(t *testing.T) {
	parent := t.TempDir()
	blocker := filepath.Join(parent, "blocker")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("os.WriteFile: %v", err)
	}
	root := filepath.Join(blocker, "runs") // MkdirAll must fail: blocker is a regular file

	g := engine.Graph{Nodes: []engine.Node{{ID: "a"}}}
	r, err := New(root, "workflow.yaml", 1, g)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if r != nil {
		t.Errorf("expected nil *Run on error, got %+v", r)
	}
	if _, statErr := os.Stat(root); statErr == nil {
		t.Errorf("expected no directory left behind at %q", root)
	}
}

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestRun_MidProcessKillLeavesPartialUsefulHistoryOnDisk is the scenario
// this brick exists to make true: a real ordivyn binary, running a real
// workflow, genuinely SIGKILLed mid-run, still leaves a partial,
// genuinely useful events.jsonl behind — proving persistence survives the
// one failure mode (a process dying) that no amount of end-of-run dumping
// could ever help with. An in-process fake can't honestly prove this: it
// would only prove the file format tolerates omission, not that a real
// kill leaves the file in a readable state, so this test pays for a real
// `go build` and a real child process.
func TestRun_MidProcessKillLeavesPartialUsefulHistoryOnDisk(t *testing.T) {
	binPath := filepath.Join(t.TempDir(), "ordivyn")
	build := exec.Command("go", "build", "-o", binPath, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	workDir := t.TempDir()
	workflowPath := filepath.Join(workDir, "workflow.yaml")
	workflow := `nodes:
  - id: fast
    type: shell
    command: "echo fast-done"
  - id: slow
    type: shell
    command: "sleep 5"
`
	if err := os.WriteFile(workflowPath, []byte(workflow), 0o644); err != nil {
		t.Fatalf("os.WriteFile: %v", err)
	}

	// Isolate the subprocess's global history root (~/.ordivyn/runs) into a
	// temp HOME so this test never touches the real one.
	homeDir := t.TempDir()
	cmd := exec.Command(binPath, "run", "-limit", "2", "workflow.yaml")
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), "HOME="+homeDir)
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting subprocess: %v", err)
	}

	runsDir := filepath.Join(homeDir, ".ordivyn", "runs")
	eventsPath := waitForRunDir(t, runsDir)

	// Poll events.jsonl until fast's node_result line has actually
	// landed, rather than sleeping a fixed guess: a flat sleep just long
	// enough on a fast machine is flaky under load (a slower process
	// spawn for "sh -c echo fast-done" can occasionally miss a short fixed
	// window). Either way this is nowhere near slow's full 5-second sleep,
	// so slow is still genuinely in flight the moment Kill fires.
	deadline := time.Now().Add(3 * time.Second)
	for {
		if hasFastResult(t, eventsPath) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fast's node_result never landed within 3s")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("killing subprocess: %v", err)
	}
	_ = cmd.Wait() // reap it; a killed process's own exit status isn't the point of this test

	f, err := os.Open(eventsPath)
	if err != nil {
		t.Fatalf("opening %s: %v", eventsPath, err)
	}
	defer f.Close()

	var events []map[string]any
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if len(sc.Bytes()) == 0 {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("line %q is not valid JSON — a kill must never leave a truncated/corrupt trailing line: %v", sc.Text(), err)
		}
		events = append(events, m)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scanning %s: %v", eventsPath, err)
	}
	t.Logf("events.jsonl after kill:\n%s", mustReread(t, eventsPath))

	var sawRunStarted bool
	startedFor := map[string]bool{}
	resultFor := map[string]map[string]any{}
	var sawRunFinished bool
	for _, e := range events {
		switch e["event"] {
		case "run_started":
			sawRunStarted = true
		case "node_started":
			startedFor[e["node_id"].(string)] = true
		case "node_result":
			resultFor[e["node_id"].(string)] = e
		case "run_finished":
			sawRunFinished = true
		}
	}

	if !sawRunStarted {
		t.Error("missing run_started line")
	}
	if !startedFor["fast"] {
		t.Error("missing node_started line for fast")
	}
	if !startedFor["slow"] {
		t.Error("missing node_started line for slow — proves slow was genuinely dispatched and in flight, not merely queued, at the moment of the kill")
	}
	if len(resultFor) != 1 {
		t.Errorf("node_result lines = %v, want exactly 1 (fast only)", resultFor)
	}
	if fastResult, ok := resultFor["fast"]; !ok {
		t.Error("missing node_result line for fast")
	} else if fastResult["status"] != "ok" {
		t.Errorf("fast's node_result status = %v, want ok", fastResult["status"])
	}
	if _, ok := resultFor["slow"]; ok {
		t.Error("found a node_result line for slow — it should never have finished before the kill")
	}
	if sawRunFinished {
		t.Error("found a run_finished line — the process was killed before Execute could return")
	}
}

// waitForRunDir polls runsDir until history.New has created its one run
// subdirectory and returns the path to its events.jsonl. history.New runs
// synchronously before Execute, so this should resolve almost immediately;
// it's polled rather than assumed instantaneous purely to avoid a race
// against the subprocess's own startup scheduling.
func waitForRunDir(t *testing.T, runsDir string) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		entries, err := os.ReadDir(runsDir)
		if err == nil && len(entries) == 1 {
			return filepath.Join(runsDir, entries[0].Name(), "events.jsonl")
		}
		if time.Now().After(deadline) {
			t.Fatalf("no run directory appeared under %s within 3s (last err: %v)", runsDir, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// hasFastResult reports whether eventsPath already contains fast's
// node_result line.
func hasFastResult(t *testing.T, eventsPath string) bool {
	t.Helper()
	b, err := os.ReadFile(eventsPath)
	if err != nil {
		return false
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		if len(sc.Bytes()) == 0 {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			continue // a line mid-write; try again on the next poll
		}
		if m["event"] == "node_result" && m["node_id"] == "fast" {
			return true
		}
	}
	return false
}

func mustReread(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("re-reading %s for logging: %v", path, err)
	}
	return b
}

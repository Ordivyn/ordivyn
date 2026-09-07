package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ordivyn/ordivyn/internal/engine"
	"github.com/Ordivyn/ordivyn/internal/schema"
)

// writeWorkflow writes contents to a file inside t.TempDir() and returns its
// path — every CLI test needs one on-disk file to point run() at.
func writeWorkflow(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "workflow.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("os.WriteFile: %v", err)
	}
	return path
}

func TestCLI_ValidateValidWorkflowPrintsOK(t *testing.T) {
	path := writeWorkflow(t, `nodes:
  - id: a
    type: shell
    command: "echo a"
  - id: b
    type: shell
    depends_on: [a]
    command: "echo b"
`)
	var out, errOut bytes.Buffer
	code := run([]string{"validate", path}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, errOut.String())
	}
	if out.String() != "ok\n" {
		t.Errorf("stdout = %q, want %q", out.String(), "ok\n")
	}
}

func TestCLI_ValidateInvalidWorkflowPrintsErrorAndExitsNonZero(t *testing.T) {
	path := writeWorkflow(t, `nodes:
  - id: a
    type: shell
    command: "echo a"
    depends_on: [b]
  - id: b
    type: shell
    command: "echo b"
    depends_on: [a]
`)
	var out, errOut bytes.Buffer
	code := run([]string{"validate", path}, &out, &errOut)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "invalid workflow") {
		t.Errorf("stderr = %q, want it to contain %q", errOut.String(), "invalid workflow")
	}
}

func TestCLI_ValidateMissingFileArgPrintsUsageAndExits2(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run([]string{"validate"}, &out, &errOut)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(errOut.String(), "usage") {
		t.Errorf("stderr = %q, want a usage hint", errOut.String())
	}
}

func TestCLI_RunExecutesAndPrintsPerNodeResults(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // run() writes ~/.ordivyn/runs; isolate it
	path := writeWorkflow(t, `nodes:
  - id: a
    type: shell
    command: "echo a"
  - id: b
    type: shell
    depends_on: [a]
    command: "echo b"
`)
	var out, errOut bytes.Buffer
	code := run([]string{"run", path}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, errOut.String())
	}
	if !strings.Contains(out.String(), "a\tok") {
		t.Errorf("stdout = %q, want it to tag node a ok", out.String())
	}
	if !strings.Contains(out.String(), "b\tok") {
		t.Errorf("stdout = %q, want it to tag node b ok", out.String())
	}
}

func TestCLI_RunFailedNodeReportedAndExitsNonZero(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // run() writes ~/.ordivyn/runs; isolate it
	path := writeWorkflow(t, `nodes:
  - id: a
    type: shell
    command: "exit 1"
`)
	var out, errOut bytes.Buffer
	code := run([]string{"run", path}, &out, &errOut)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(out.String(), "a\tfailed") {
		t.Errorf("stdout = %q, want it to tag node a failed", out.String())
	}
}

func TestCLI_RunRejectsNonPositiveLimit(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // run() writes ~/.ordivyn/runs; isolate it
	path := writeWorkflow(t, `nodes:
  - id: a
    type: shell
    command: "echo a"
`)
	var out, errOut bytes.Buffer
	code := run([]string{"run", "-limit", "0", path}, &out, &errOut)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "limit must be >= 1") {
		t.Errorf("stderr = %q, want engine's own limit error text", errOut.String())
	}
}

func TestCLI_RunPrintsCapturedShellOutput(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // run() writes ~/.ordivyn/runs; isolate it
	path := writeWorkflow(t, `nodes:
  - id: a
    type: shell
    command: "echo marker-xyz-123"
`)
	var out, errOut bytes.Buffer
	code := run([]string{"run", path}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, errOut.String())
	}
	if !strings.Contains(out.String(), "marker-xyz-123") {
		t.Errorf("stdout = %q, want it to contain the captured marker", out.String())
	}
}

func TestCLI_UnknownCommandPrintsUsageAndExits2(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run([]string{"frobnicate"}, &out, &errOut)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(errOut.String(), "usage") {
		t.Errorf("stderr = %q, want a usage hint", errOut.String())
	}
}

func TestCLI_LoadErrorSurfacesBeforeExecute(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run([]string{"run", "/nonexistent/path/to/workflow.yaml"}, &out, &errOut)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "schema:") {
		t.Errorf("stderr = %q, want it to contain the Load error's own %q prefix", errOut.String(), "schema:")
	}
}

func TestCLI_ValidateAgentWorkflowPrintsOK(t *testing.T) {
	path := writeWorkflow(t, `nodes:
  - id: a
    type: agent
    prompt: "do the thing"
`)
	var out, errOut bytes.Buffer
	code := run([]string{"validate", path}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, errOut.String())
	}
	if out.String() != "ok\n" {
		t.Errorf("stdout = %q, want %q", out.String(), "ok\n")
	}
}

func TestCLI_RunRejectsAgentNodeMissingPromptBeforeExecute(t *testing.T) {
	path := writeWorkflow(t, `nodes:
  - id: a
    type: agent
`)
	var out, errOut bytes.Buffer
	code := run([]string{"run", path}, &out, &errOut)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), `requires field "prompt"`) {
		t.Errorf("stderr = %q, want it to contain the Load decode error, proving rejection happens before Execute", errOut.String())
	}
}

func TestCLI_PrintResultsShowsAgentText(t *testing.T) {
	var out, errOut bytes.Buffer
	results := map[engine.NodeID]engine.Result{
		"a": {Status: engine.StatusOK, Output: schema.AgentOutput{Text: "the answer"}},
	}
	printResults(&out, &errOut, results)
	if !strings.Contains(out.String(), "the answer") {
		t.Errorf("stdout = %q, want it to contain %q", out.String(), "the answer")
	}
}

func TestCLI_PrintResultsFallsBackToRawStdoutOnAgentFailure(t *testing.T) {
	var out, errOut bytes.Buffer
	results := map[engine.NodeID]engine.Result{
		"a": {Status: engine.StatusFailed, Output: schema.AgentOutput{Stdout: "Error: model not found"}},
	}
	printResults(&out, &errOut, results)
	if !strings.Contains(out.String(), "Error: model not found") {
		t.Errorf("stdout = %q, want it to contain the raw Stdout since Text is empty", out.String())
	}
}

// --- history wiring ---

// runsRoot is the run-history root under a given (test-overridden) home dir,
// mirroring main's historyDir().
func runsRoot(home string) string {
	return filepath.Join(home, ".ordivyn", "runs")
}

// onlyRunDir returns the single run subdirectory under home's history root,
// failing the test if there isn't exactly one.
func onlyRunDir(t *testing.T, home string) string {
	t.Helper()
	runsDir := runsRoot(home)
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		t.Fatalf("reading %s: %v", runsDir, err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries in %s = %v, want exactly 1", runsDir, entries)
	}
	return filepath.Join(runsDir, entries[0].Name())
}

func readEventLines(t *testing.T, path string) []map[string]any {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("opening %s: %v", path, err)
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
			t.Fatalf("line %q not valid JSON: %v", sc.Text(), err)
		}
		events = append(events, m)
	}
	return events
}

func TestCLI_RunWritesRunStartedNodeStartedAndNodeResultEvents(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	path := writeWorkflow(t, `nodes:
  - id: a
    type: shell
    command: "echo a"
  - id: b
    type: shell
    depends_on: [a]
    command: "echo b"
`)
	var out, errOut bytes.Buffer
	code := run([]string{"run", path}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, errOut.String())
	}

	runDir := onlyRunDir(t, tmp)
	events := readEventLines(t, filepath.Join(runDir, "events.jsonl"))

	var kinds []string
	var aStartIdx, aResultIdx, bStartIdx, bResultIdx = -1, -1, -1, -1
	nodeStarted, nodeResult := 0, 0
	for i, e := range events {
		kinds = append(kinds, e["event"].(string))
		switch e["event"] {
		case "node_started":
			nodeStarted++
			if e["node_id"] == "a" {
				aStartIdx = i
			}
			if e["node_id"] == "b" {
				bStartIdx = i
			}
		case "node_result":
			nodeResult++
			if e["node_id"] == "a" {
				aResultIdx = i
			}
			if e["node_id"] == "b" {
				bResultIdx = i
			}
			if e["status"] != "ok" {
				t.Errorf("node_result for %v: status = %v, want ok", e["node_id"], e["status"])
			}
		}
	}
	if kinds[0] != "run_started" {
		t.Errorf("first event = %v, want run_started", kinds[0])
	}
	if kinds[len(kinds)-1] != "run_finished" {
		t.Errorf("last event = %v, want run_finished", kinds[len(kinds)-1])
	}
	if nodeStarted != 2 {
		t.Errorf("node_started count = %d, want 2", nodeStarted)
	}
	if nodeResult != 2 {
		t.Errorf("node_result count = %d, want 2", nodeResult)
	}
	if aStartIdx == -1 || aResultIdx == -1 || bStartIdx == -1 || bResultIdx == -1 {
		t.Fatalf("missing expected events: %v", kinds)
	}
	if !(aStartIdx < bStartIdx && aResultIdx < bStartIdx) {
		t.Errorf("a's events (start=%d, result=%d) should both precede b's start (%d), since b depends on a", aStartIdx, aResultIdx, bStartIdx)
	}
}

func TestCLI_RunPrintsHistoryPathToStderr(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	path := writeWorkflow(t, `nodes:
  - id: a
    type: shell
    command: "echo a"
`)
	var out, errOut bytes.Buffer
	code := run([]string{"run", path}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "run recorded at") {
		t.Fatalf("stderr = %q, want it to contain %q", errOut.String(), "run recorded at")
	}
	runDir := onlyRunDir(t, tmp)
	eventsPath := filepath.Join(runDir, "events.jsonl")
	if !strings.Contains(errOut.String(), eventsPath) {
		t.Errorf("stderr = %q, want it to contain the printed path %q", errOut.String(), eventsPath)
	}
	if _, err := os.Stat(eventsPath); err != nil {
		t.Errorf("printed path does not exist: %v", err)
	}
}

func TestCLI_RunStillSucceedsWhenHistoryDirUnwritable(t *testing.T) {
	workflow := `nodes:
  - id: a
    type: shell
    command: "echo a"
`
	// Clean run, for comparison.
	cleanTmp := t.TempDir()
	t.Setenv("HOME", cleanTmp)
	cleanPath := writeWorkflow(t, workflow)
	var cleanOut, cleanErrOut bytes.Buffer
	cleanCode := run([]string{"run", cleanPath}, &cleanOut, &cleanErrOut)

	// Same workflow, but ~/.ordivyn is a regular file, so history.New's
	// os.MkdirAll must fail.
	blockedTmp := t.TempDir()
	t.Setenv("HOME", blockedTmp)
	if err := os.WriteFile(filepath.Join(blockedTmp, ".ordivyn"), []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("os.WriteFile: %v", err)
	}
	blockedPath := writeWorkflow(t, workflow)
	var blockedOut, blockedErrOut bytes.Buffer
	blockedCode := run([]string{"run", blockedPath}, &blockedOut, &blockedErrOut)

	if blockedCode != cleanCode {
		t.Errorf("exit code = %d, want %d (same as clean run)", blockedCode, cleanCode)
	}
	if blockedOut.String() != cleanOut.String() {
		t.Errorf("stdout = %q, want %q (identical to clean run)", blockedOut.String(), cleanOut.String())
	}
	if !strings.Contains(blockedErrOut.String(), "run history disabled") {
		t.Errorf("stderr = %q, want it to contain a history-disabled warning", blockedErrOut.String())
	}
}

func TestCLI_ValidateNeverWritesHistory(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	path := writeWorkflow(t, `nodes:
  - id: a
    type: shell
    command: "echo a"
`)
	var out, errOut bytes.Buffer
	code := run([]string{"validate", path}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, errOut.String())
	}
	if _, err := os.Stat(runsRoot(tmp)); !os.IsNotExist(err) {
		t.Errorf("expected %s to not exist after validate, stat err = %v", runsRoot(tmp), err)
	}
}

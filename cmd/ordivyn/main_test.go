package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

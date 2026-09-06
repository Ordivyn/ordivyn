package schema

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Ordivyn/ordivyn/internal/engine"
)

func TestAgentFunc_CapturesText(t *testing.T) {
	out, err := newAgentFunc("sh", []string{"-c", `cat >/dev/null; printf '{"result":"hi","is_error":false}'`}, "irrelevant")(context.Background(), nil)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	ao, ok := out.(AgentOutput)
	if !ok {
		t.Fatalf("out is %T, want AgentOutput", out)
	}
	if ao.Text != "hi" {
		t.Errorf("Text = %q, want %q", ao.Text, "hi")
	}
	if ao.IsError {
		t.Errorf("IsError = true, want false")
	}
}

func TestAgentFunc_PromptSentViaStdinNotArgv(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "out.txt")
	prompt := "line one with a ' single quote\nline two with a ` backtick\nline three with $(touch /tmp/should-not-exist)"

	// Not asserting on the returned error here: the fake CLI writes the
	// prompt to tmpFile and produces no JSON on stdout, so the JSON-decode
	// step legitimately fails. What this test proves is the prompt's exact
	// bytes crossed the process boundary as inert stdin data, never
	// re-parsed by a shell — the JSON contract is covered elsewhere.
	newAgentFunc("sh", []string{"-c", "cat > " + tmpFile}, prompt)(context.Background(), nil)

	got, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("os.ReadFile: %v", err)
	}
	if string(got) != prompt {
		t.Errorf("stdin bytes = %q, want %q (prompt must cross as inert stdin data, never re-parsed)", string(got), prompt)
	}
	if _, statErr := os.Stat("/tmp/should-not-exist"); statErr == nil {
		t.Error("/tmp/should-not-exist was created, want the $(...) never executed")
		os.Remove("/tmp/should-not-exist")
	}
}

func TestAgentFunc_IsErrorTrueFailsNodeButKeepsText(t *testing.T) {
	out, err := newAgentFunc("sh", []string{"-c", `cat >/dev/null; printf '{"result":"partial","is_error":true}'`}, "irrelevant")(context.Background(), nil)
	if err == nil {
		t.Fatal("err = nil, want a non-nil error when is_error is true")
	}
	ao, ok := out.(AgentOutput)
	if !ok {
		t.Fatalf("out is %T, want AgentOutput", out)
	}
	if !ao.IsError {
		t.Errorf("IsError = false, want true")
	}
	if ao.Text != "partial" {
		t.Errorf("Text = %q, want %q (not discarded just because the node failed)", ao.Text, "partial")
	}
}

func TestAgentFunc_InvalidJSONReturnsErrorWithRawStdoutPreserved(t *testing.T) {
	out, err := newAgentFunc("sh", []string{"-c", `cat >/dev/null; printf 'not json'`}, "irrelevant")(context.Background(), nil)
	if err == nil {
		t.Fatal("err = nil, want a non-nil error naming the parse failure")
	}
	ao, ok := out.(AgentOutput)
	if !ok {
		t.Fatalf("out is %T, want AgentOutput", out)
	}
	if ao.Stdout != "not json" {
		t.Errorf("Stdout = %q, want %q", ao.Stdout, "not json")
	}
	if ao.Text != "" {
		t.Errorf("Text = %q, want empty (no JSON parse succeeded)", ao.Text)
	}
}

func TestAgentFunc_NonZeroExitReturnsErrorWithOutputPreserved(t *testing.T) {
	out, err := newAgentFunc("sh", []string{"-c", "echo oops 1>&2; exit 5"}, "irrelevant")(context.Background(), nil)
	if err == nil {
		t.Fatal("err = nil, want a non-nil error for a non-zero exit")
	}
	ao, ok := out.(AgentOutput)
	if !ok {
		t.Fatalf("out is %T, want AgentOutput", out)
	}
	if ao.ExitCode != 5 {
		t.Errorf("ExitCode = %d, want 5", ao.ExitCode)
	}
	if !strings.Contains(ao.Stderr, "oops") {
		t.Errorf("Stderr = %q, want it to contain %q", ao.Stderr, "oops")
	}
	if !strings.Contains(err.Error(), "5") {
		t.Errorf("err = %v, want it to name exit status 5", err)
	}
	if ao.Text != "" {
		t.Errorf("Text = %q, want empty (no JSON parse attempted on a failed exit)", ao.Text)
	}
}

func TestAgentFunc_RespectsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	_, err := newAgentFunc("sh", []string{"-c", "sleep 5"}, "irrelevant")(ctx, nil)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("err = nil, want a non-nil error from a canceled context")
	}
	if elapsed > 3*time.Second {
		t.Errorf("call took %v, want it bounded by the canceled context, not the full sleep", elapsed)
	}
}

func TestAgentFunc_IgnoresDependencyInputs(t *testing.T) {
	inputs := map[engine.NodeID]any{"dep": "should be ignored"}
	out, err := newAgentFunc("sh", []string{"-c", `cat >/dev/null; printf '{"result":"hi","is_error":false}'`}, "irrelevant")(context.Background(), inputs)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	ao := out.(AgentOutput)
	if ao.Text != "hi" {
		t.Errorf("Text = %q, want %q (inputs must not affect the executed command)", ao.Text, "hi")
	}
}

func TestAgentFunc_MissingBinaryReturnsError(t *testing.T) {
	out, err := newAgentFunc("/nonexistent-ordivyn-test-binary", nil, "irrelevant")(context.Background(), nil)
	if err == nil {
		t.Fatal("err = nil, want a non-nil error for a missing binary")
	}
	ao, ok := out.(AgentOutput)
	if !ok {
		t.Fatalf("out is %T, want AgentOutput", out)
	}
	if ao.ExitCode != -1 {
		t.Errorf("ExitCode = %d, want -1", ao.ExitCode)
	}
}

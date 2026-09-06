package schema

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Ordivyn/ordivyn/internal/engine"
)

func TestShellFunc_CapturesStdoutAndExitZero(t *testing.T) {
	out, err := newShellFunc("echo hi")(context.Background(), nil)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	so, ok := out.(ShellOutput)
	if !ok {
		t.Fatalf("out is %T, want ShellOutput", out)
	}
	if so.Stdout != "hi\n" {
		t.Errorf("Stdout = %q, want %q", so.Stdout, "hi\n")
	}
	if so.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", so.ExitCode)
	}
}

func TestShellFunc_CapturesStderrSeparately(t *testing.T) {
	out, err := newShellFunc("echo err-marker 1>&2")(context.Background(), nil)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	so := out.(ShellOutput)
	if so.Stdout != "" {
		t.Errorf("Stdout = %q, want empty", so.Stdout)
	}
	if !strings.Contains(so.Stderr, "err-marker") {
		t.Errorf("Stderr = %q, want it to contain %q", so.Stderr, "err-marker")
	}
}

func TestShellFunc_NonZeroExitReturnsErrorWithOutputPreserved(t *testing.T) {
	out, err := newShellFunc("exit 3")(context.Background(), nil)
	if err == nil {
		t.Fatal("err = nil, want a non-nil error for a non-zero exit")
	}
	so, ok := out.(ShellOutput)
	if !ok {
		t.Fatalf("out is %T, want ShellOutput", out)
	}
	if so.ExitCode != 3 {
		t.Errorf("ExitCode = %d, want 3", so.ExitCode)
	}
}

func TestShellFunc_RespectsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := newShellFunc("sleep 5")(ctx, nil)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("err = nil, want a non-nil error from a canceled context")
	}
	if elapsed > 3*time.Second {
		t.Errorf("Run took %v, want it bounded by the context's cancellation, not the full sleep", elapsed)
	}
}

func TestShellFunc_IgnoresDependencyInputs(t *testing.T) {
	inputs := map[engine.NodeID]any{"dep": "should be ignored"}
	out, err := newShellFunc("echo hi")(context.Background(), inputs)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	so := out.(ShellOutput)
	if so.Stdout != "hi\n" {
		t.Errorf("Stdout = %q, want %q (inputs must not affect the executed command)", so.Stdout, "hi\n")
	}
}

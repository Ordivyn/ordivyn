package schema

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"

	"github.com/Ordivyn/ordivyn/internal/engine"
)

// ShellOutput is the Output value a shell node's engine.Result carries:
// stdout and stderr captured separately, plus the process's exit code.
// Exported so a caller that already knows it's looking at a shell node's
// Result — here, the CLI — can type-assert it out of the opaque
// Result.Output without engine ever knowing this type exists.
type ShellOutput struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// newShellFunc closes over one already-validated command string and
// returns the engine.NodeFunc that runs it. It ignores the inputs map
// entirely — dependency outputs are not interpolated into the command.
// depends_on gates *when* this node may start; it is not a data-flow
// channel into what it runs. Templating a dependency's stdout into a
// command string would mean evaluating an expression embedded in the
// workflow file, which this format deliberately does not support.
func newShellFunc(command string) engine.NodeFunc {
	return func(ctx context.Context, _ map[engine.NodeID]any) (any, error) {
		// ponytail: exec.CommandContext kills only this sh process on
		// cancellation, not grandchildren it spawned (e.g. a backgrounded
		// sleep &). Setpgid + process-group kill is the upgrade path, add
		// when an orphaned-process report actually shows up.
		cmd := exec.CommandContext(ctx, "sh", "-c", command)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr

		runErr := cmd.Run()
		out := ShellOutput{Stdout: stdout.String(), Stderr: stderr.String()}

		var exitErr *exec.ExitError
		switch {
		case runErr == nil:
			out.ExitCode = 0
			return out, nil
		case errors.As(runErr, &exitErr):
			out.ExitCode = exitErr.ExitCode()
			return out, fmt.Errorf("command exited with status %d: %s", out.ExitCode, command)
		default:
			// couldn't even start (e.g. sh missing), or ctx was canceled
			// before the process started: runErr already describes it. A
			// cancellation *while* the process is running instead surfaces
			// as a real *exec.ExitError ("signal: killed") and takes the
			// case above, not this one.
			out.ExitCode = -1
			return out, runErr
		}
	}
}

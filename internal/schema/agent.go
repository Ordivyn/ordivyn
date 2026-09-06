package schema

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/Ordivyn/ordivyn/internal/engine"
)

// agentBin and agentArgs are the exact, hardcoded invocation every "agent"
// node runs. Not a YAML field: exactly one backing CLI exists today, so
// there is nothing yet to choose between. Package-level, and threaded
// through newAgentFunc as parameters rather than read directly inside it,
// purely so tests can substitute a fake CLI — no automated test ever
// invokes the real `claude` binary, spends a token, or needs an API key.
var (
	agentBin  = "claude"
	agentArgs = []string{"-p", "--output-format", "json", "--dangerously-skip-permissions"}
)

// AgentOutput is the Output value an agent node's engine.Result carries.
// Stdout, Stderr, and ExitCode are always populated, exactly like
// ShellOutput. Text and IsError are populated only when Stdout parsed as
// the CLI's --output-format json contract; they stay at their zero value
// otherwise, and Stdout itself is never discarded, so a decode failure is
// still debuggable straight from the Result. Other fields the CLI's JSON
// object carries (session id, cost, ...) are not decoded here — nothing in
// this brick reads them; add them when something does.
type AgentOutput struct {
	Text     string
	IsError  bool
	Stdout   string
	Stderr   string
	ExitCode int
}

// claudeResult mirrors the subset of `claude -p --output-format json`'s
// JSON object this node relies on — just the two fields AgentOutput
// actually uses. Unexported: a decode shape, not part of schema's API.
type claudeResult struct {
	Result  string `json:"result"`
	IsError bool   `json:"is_error"`
}

// newAgentFunc closes over one already-validated prompt string and returns
// the engine.NodeFunc that pipes it to bin's stdin (never interpolated
// into a shell string — bin is exec'd directly) and decodes its stdout as
// claudeResult. decodeNode always calls this with agentBin/agentArgs;
// tests call it with a fake bin/args directly.
func newAgentFunc(bin string, args []string, prompt string) engine.NodeFunc {
	return func(ctx context.Context, _ map[engine.NodeID]any) (any, error) {
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Stdin = strings.NewReader(prompt)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr

		runErr := cmd.Run()
		out := AgentOutput{Stdout: stdout.String(), Stderr: stderr.String()}

		var exitErr *exec.ExitError
		switch {
		case runErr == nil:
			out.ExitCode = 0
			var cr claudeResult
			if jsonErr := json.Unmarshal(stdout.Bytes(), &cr); jsonErr != nil {
				// Raw Stdout is already in out — a malformed-JSON result is
				// still fully debuggable, not silently swallowed.
				return out, fmt.Errorf("agent: could not parse JSON output: %w", jsonErr)
			}
			out.Text, out.IsError = cr.Result, cr.IsError
			if out.IsError {
				// The CLI's own report of failure, distinct from a nonzero
				// exit code, still means this node did not succeed.
				return out, fmt.Errorf("agent: reported an error")
			}
			return out, nil
		case errors.As(runErr, &exitErr):
			out.ExitCode = exitErr.ExitCode()
			return out, fmt.Errorf("agent command exited with status %d", out.ExitCode)
		default:
			// couldn't even start (bin missing), or ctx already done before
			// the process started. Mid-run cancellation surfaces as a real
			// *exec.ExitError ("signal: killed") and takes the case above.
			out.ExitCode = -1
			return out, runErr
		}
	}
}

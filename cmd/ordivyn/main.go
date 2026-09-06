package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/Ordivyn/ordivyn/internal/engine"
	"github.com/Ordivyn/ordivyn/internal/schema"
)

const usage = `usage:
  ordivyn validate <file>
  ordivyn run <file> [-limit N]
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is main's testable core: parse argv, dispatch, write to out/errOut,
// return the process exit code. Split from main so tests call it directly
// — no subprocess, no os.Exit inside the tested path.
func run(args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(errOut, usage)
		return 2
	}
	switch args[0] {
	case "validate":
		return runValidate(args[1:], out, errOut)
	case "run":
		return runRun(args[1:], out, errOut)
	case "-h", "--help":
		fmt.Fprint(out, usage)
		return 0
	default:
		fmt.Fprintf(errOut, "ordivyn: unknown command %q\n%s", args[0], usage)
		return 2
	}
}

func runValidate(args []string, out, errOut io.Writer) int {
	if len(args) != 1 {
		fmt.Fprint(errOut, "usage: ordivyn validate <file>\n")
		return 2
	}
	g, err := schema.Load(args[0])
	if err != nil {
		fmt.Fprintf(errOut, "ordivyn: %v\n", err)
		return 1
	}
	if err := engine.Validate(g); err != nil {
		fmt.Fprintf(errOut, "ordivyn: invalid workflow: %v\n", err)
		return 1
	}
	fmt.Fprintln(out, "ok")
	return 0
}

func runRun(args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(errOut)
	limit := fs.Int("limit", 4, "max concurrent nodes")
	if err := fs.Parse(args); err != nil {
		return 2 // flag already printed its own error + usage to errOut
	}
	if fs.NArg() != 1 {
		fmt.Fprint(errOut, "usage: ordivyn run <file> [-limit N]\n")
		return 2
	}
	g, err := schema.Load(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(errOut, "ordivyn: %v\n", err)
		return 1
	}
	results, err := engine.Execute(context.Background(), g, *limit)
	if err != nil {
		fmt.Fprintf(errOut, "ordivyn: %v\n", err)
		return 1
	}
	printResults(out, errOut, results)
	for _, r := range results {
		if r.Status != engine.StatusOK {
			return 1
		}
	}
	return 0
}

// printResults sorts by NodeID before printing — results is a Go map,
// iteration order is random, and reproducible output is part of the same
// determinism story engine already guarantees for dispatch.
func printResults(out io.Writer, errOut io.Writer, results map[engine.NodeID]engine.Result) {
	ids := make([]engine.NodeID, 0, len(results))
	for id := range results {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	for _, id := range ids {
		r := results[id]
		fmt.Fprintf(out, "%s\t%s\n", id, statusString(r.Status))
		if r.Err != nil {
			fmt.Fprintf(out, "%s\terr: %v\n", id, r.Err)
		}
		if so, ok := r.Output.(schema.ShellOutput); ok {
			if so.Stdout != "" {
				fmt.Fprint(out, so.Stdout)
			}
			if so.Stderr != "" {
				fmt.Fprint(errOut, so.Stderr)
			}
		}
		if ao, ok := r.Output.(schema.AgentOutput); ok {
			if ao.Text != "" {
				fmt.Fprint(out, ao.Text)
			} else if ao.Stdout != "" {
				// Text is only set once JSON decoding succeeds. On a
				// nonzero exit or a decode failure, whatever the CLI
				// printed is still the only clue why - never discard it.
				fmt.Fprint(out, ao.Stdout)
			}
			if ao.Stderr != "" {
				fmt.Fprint(errOut, ao.Stderr)
			}
		}
	}
}

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

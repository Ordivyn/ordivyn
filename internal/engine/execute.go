package engine

import (
	"context"
	"fmt"
	"sort"
)

// Execute validates g, then runs it to completion: a node starts only once
// every dependency it declared has resolved (success, failure, or skip);
// independent nodes run concurrently, never more than limit at once; a
// failed or skipped dependency skips only that node's real descendants.
// Returns exactly one Result per node in g.Nodes, always — never returns a
// partial map. The returned error is non-nil only if the graph is invalid
// or limit < 1; in both cases no node is ever invoked.
//
// Concurrency model: exactly one goroutine ever touches scheduler state
// (indegree, dependents, results, taintCause, ready, running, remaining).
// Worker goroutines do nothing but call node.Run and send their outcome on
// an unbuffered done chan Result; they never read or write scheduler state
// directly.
//
// onStart and onResult, each nilable, are called synchronously on the same
// single goroutine that already owns all scheduler state.
//
// onStart is called exactly once per node that is actually dispatched,
// immediately before that node's goroutine is launched. It is never
// called for a node that is skipped — a skipped node's Run is never
// invoked either, so there is nothing that "started."
//
// onResult is called exactly once per node, immediately after that node's
// Result is finalized and written into the map Execute will eventually
// return, and before dispatch() is asked to fill the concurrency slot
// that just freed up. Called for a skipped node exactly as it is for one
// that actually ran: a skip is as much "what happened" as a success or a
// failure.
//
// Neither is called before both of Execute's own pre-checks (limit >= 1,
// Validate(g) == nil) have passed, and neither is ever called concurrently
// with itself or with the other. A panic inside either is recovered here,
// the same containment Execute already gives a node's own Run panicking —
// a caller-supplied observer's bug can cost that one event, never the
// run's results map or the process.
func Execute(ctx context.Context, g Graph, limit int, onStart func(NodeID), onResult func(Result)) (map[NodeID]Result, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("engine: limit must be >= 1, got %d", limit)
	}
	if err := Validate(g); err != nil {
		return nil, err
	}
	byID, indegree, dependents, _ := buildGraph(g) // error impossible: Validate above already passed

	results := make(map[NodeID]Result, len(byID))
	taintCause := make(map[NodeID]error, len(byID))
	done := make(chan Result)
	running, remaining := 0, len(byID)

	var ready []NodeID
	for id, d := range indegree {
		if d == 0 {
			ready = append(ready, id)
		}
	}

	// resolve finalizes one node's outcome — real or skipped — and hands
	// its newly-unblocked children into `ready`. Called only from this one
	// goroutine.
	resolve := func(r Result) {
		results[r.NodeID] = r
		remaining--
		if onResult != nil {
			observe(onResult, r)
		}
		failed := r.Status != StatusOK
		for _, child := range dependents[r.NodeID] {
			indegree[child]--
			if failed {
				// Record the actual cause, not just a bool, so a skip is
				// traceable back to it (see ErrSkipped's doc comment).
				// First-writer-wins if several of child's dependencies fail
				// concurrently: which one gets attributed isn't a
				// determinism guarantee this plan makes — only that some
				// real, traceable cause is always recorded.
				if _, already := taintCause[child]; !already {
					taintCause[child] = r.Err
				}
			}
			if indegree[child] == 0 {
				ready = append(ready, child)
			}
		}
	}

	// dispatch is the ONE place a "who runs next" decision is made. It is
	// called synchronously, once at start and once after every completion,
	// from this single goroutine — there is no second call site and no
	// concurrent caller, so nothing ever contends for the same free slot.
	dispatch := func() {
		// Phase 1: drain every ready node that will never actually run —
		// a tainted dependency, or a run whose context is already done —
		// without spending a concurrency slot on it. resolve() may append
		// newly-unblocked children to `ready`; the loop re-reads
		// len(ready) every iteration, so a cascade of taint (e.g. through
		// a diamond) is fully drained within this one pass.
		for i := 0; i < len(ready); {
			id := ready[i]
			rootCause, isTainted := taintCause[id]
			if isTainted || ctx.Err() != nil {
				ready = append(ready[:i], ready[i+1:]...) // remove id; next element slides into i
				var cause error
				if isTainted {
					// Wraps the actual failing dependency's own Result.Err.
					// If that dependency was itself a skip, its Err already
					// wraps ErrSkipped around *its* cause, so this chains
					// transitively back to the original root-cause error
					// through any number of skip layers — not just one hop.
					cause = fmt.Errorf("%w: dependency did not succeed: %w", ErrSkipped, rootCause)
				} else {
					cause = fmt.Errorf("%w: %w", ErrSkipped, ctx.Err())
				}
				resolve(Result{NodeID: id, Status: StatusSkipped, Err: cause})
				continue // i unchanged on purpose
			}
			i++
		}

		// Phase 2: fill whatever's left of the concurrency budget,
		// smallest NodeID first — the deterministic tie-break.
		sort.Slice(ready, func(a, b int) bool { return ready[a] < ready[b] })
		for len(ready) > 0 && running < limit {
			id := ready[0]
			ready = ready[1:]
			node := byID[id]
			inputs := make(map[NodeID]any, len(node.DependsOn))
			for _, dep := range node.DependsOn {
				inputs[dep] = results[dep].Output // safe: only reached once dep has resolved
			}
			running++
			if onStart != nil {
				observe(onStart, id)
			}
			go func(n Node, in map[NodeID]any) {
				defer func() {
					if p := recover(); p != nil {
						done <- Result{NodeID: n.ID, Status: StatusFailed,
							Err: fmt.Errorf("engine: panic in node %q: %v", n.ID, p)}
					}
				}()
				out, err := n.Run(ctx, in)
				st := StatusOK
				if err != nil {
					st = StatusFailed
				}
				done <- Result{NodeID: n.ID, Status: st, Output: out, Err: err}
			}(node, inputs)
		}
	}

	dispatch()
	for remaining > 0 {
		r := <-done
		running--
		resolve(r)
		dispatch()
	}
	return results, nil
}

// observe calls fn with v, recovering any panic so a bug in caller-
// supplied persistence code can never crash Execute or discard results
// that already resolved correctly. Generic over the one-argument shape
// both onStart (func(NodeID)) and onResult (func(Result)) share, rather
// than two near-identical copies of the same three lines.
func observe[T any](fn func(T), v T) {
	defer func() { recover() }()
	fn(v)
}

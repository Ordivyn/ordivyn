package engine

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// runWithTimeout calls Execute in its own goroutine and fails the test if it
// doesn't return within d — the guard against a hang, per the plan's own
// "-timeout rather than hanging" verification approach.
func runWithTimeout(t *testing.T, ctx context.Context, g Graph, limit int, d time.Duration) (map[NodeID]Result, error) {
	t.Helper()
	type out struct {
		results map[NodeID]Result
		err     error
	}
	ch := make(chan out, 1)
	go func() {
		r, err := Execute(ctx, g, limit, nil, nil)
		ch <- out{r, err}
	}()
	select {
	case o := <-ch:
		return o.results, o.err
	case <-time.After(d):
		t.Fatal("Execute did not return in time")
		return nil, nil
	}
}

func wantStatus(t *testing.T, results map[NodeID]Result, id NodeID, want Status) {
	t.Helper()
	r, ok := results[id]
	if !ok {
		t.Fatalf("no result for node %q", id)
	}
	if r.Status != want {
		t.Errorf("node %q: status = %v, want %v (err: %v)", id, r.Status, want, r.Err)
	}
}

// --- Execute: structural gating ---

func TestExecute_InvalidGraphRunsNothing(t *testing.T) {
	var calls atomic.Int32
	g := Graph{Nodes: []Node{
		{ID: "A", DependsOn: []NodeID{"Z"}, Run: func(ctx context.Context, in map[NodeID]any) (any, error) {
			calls.Add(1)
			return nil, nil
		}},
	}}
	results, err := Execute(context.Background(), g, 1, nil, nil)
	if err == nil {
		t.Fatal("expected error for invalid graph, got nil")
	}
	if results != nil {
		t.Errorf("expected nil results for invalid graph, got: %v", results)
	}
	if calls.Load() != 0 {
		t.Errorf("expected zero invocations, got %d", calls.Load())
	}
}

func TestExecute_RejectsNonPositiveLimit(t *testing.T) {
	for _, limit := range []int{0, -1} {
		var calls atomic.Int32
		g := Graph{Nodes: []Node{
			{ID: "A", Run: func(ctx context.Context, in map[NodeID]any) (any, error) {
				calls.Add(1)
				return nil, nil
			}},
		}}
		results, err := Execute(context.Background(), g, limit, nil, nil)
		if err == nil {
			t.Fatalf("limit=%d: expected error, got nil", limit)
		}
		if results != nil {
			t.Errorf("limit=%d: expected nil results, got: %v", limit, results)
		}
		if calls.Load() != 0 {
			t.Errorf("limit=%d: expected zero invocations, got %d", limit, calls.Load())
		}
	}
}

func TestExecute_EmptyGraphReturnsEmptyResults(t *testing.T) {
	results, err := Execute(context.Background(), Graph{}, 1, nil, nil)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected empty results, got: %v", results)
	}
}

// --- Execute: dependency ordering ---

func TestExecute_NodeWaitsForBothDependencies(t *testing.T) {
	chanA := make(chan struct{})
	chanB := make(chan struct{})
	var mainStarted atomic.Bool

	g := Graph{Nodes: []Node{
		{ID: "DepA", Run: func(ctx context.Context, in map[NodeID]any) (any, error) {
			<-chanA
			return "A-out", nil
		}},
		{ID: "DepB", Run: func(ctx context.Context, in map[NodeID]any) (any, error) {
			<-chanB
			return "B-out", nil
		}},
		{ID: "Main", DependsOn: []NodeID{"DepA", "DepB"}, Run: func(ctx context.Context, in map[NodeID]any) (any, error) {
			mainStarted.Store(true)
			return nil, nil
		}},
	}}

	type out struct {
		results map[NodeID]Result
		err     error
	}
	done := make(chan out, 1)
	go func() {
		r, err := Execute(context.Background(), g, 2, nil, nil)
		done <- out{r, err}
	}()

	close(chanA)
	time.Sleep(50 * time.Millisecond)
	if mainStarted.Load() {
		t.Fatal("Main started after only one of its two dependencies resolved")
	}

	close(chanB)

	select {
	case o := <-done:
		if o.err != nil {
			t.Fatalf("unexpected error: %v", o.err)
		}
		if !mainStarted.Load() {
			t.Error("Main never started after both dependencies resolved")
		}
		wantStatus(t, o.results, "Main", StatusOK)
	case <-time.After(2 * time.Second):
		t.Fatal("Execute did not return in time")
	}
}

func TestExecute_LinearChainRespectsOrder(t *testing.T) {
	var aFinished atomic.Bool
	g := Graph{Nodes: []Node{
		{ID: "A", Run: func(ctx context.Context, in map[NodeID]any) (any, error) {
			time.Sleep(10 * time.Millisecond)
			aFinished.Store(true)
			return "A-output", nil
		}},
		{ID: "B", DependsOn: []NodeID{"A"}, Run: func(ctx context.Context, in map[NodeID]any) (any, error) {
			if !aFinished.Load() {
				t.Error("B started before A finished")
			}
			if in["A"] != "A-output" {
				t.Errorf("B's inputs[A] = %v, want %q", in["A"], "A-output")
			}
			return "B-output", nil
		}},
		{ID: "C", DependsOn: []NodeID{"B"}, Run: func(ctx context.Context, in map[NodeID]any) (any, error) {
			if in["B"] != "B-output" {
				t.Errorf("C's inputs[B] = %v, want %q", in["B"], "B-output")
			}
			return "C-output", nil
		}},
	}}
	results, err := Execute(context.Background(), g, 2, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantStatus(t, results, "A", StatusOK)
	wantStatus(t, results, "B", StatusOK)
	wantStatus(t, results, "C", StatusOK)
}

func TestExecute_DiamondJoinReceivesBothOutputs(t *testing.T) {
	g := Graph{Nodes: []Node{
		{ID: "A", Run: func(ctx context.Context, in map[NodeID]any) (any, error) {
			return "A-out", nil
		}},
		{ID: "B", DependsOn: []NodeID{"A"}, Run: func(ctx context.Context, in map[NodeID]any) (any, error) {
			return "B-out", nil
		}},
		{ID: "C", DependsOn: []NodeID{"A"}, Run: func(ctx context.Context, in map[NodeID]any) (any, error) {
			return "C-out", nil
		}},
		{ID: "D", DependsOn: []NodeID{"B", "C"}, Run: func(ctx context.Context, in map[NodeID]any) (any, error) {
			if in["B"] != "B-out" || in["C"] != "C-out" {
				t.Errorf("D's inputs = %v, want B-out/C-out", in)
			}
			return "D-out", nil
		}},
	}}
	results, err := Execute(context.Background(), g, 2, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, id := range []NodeID{"A", "B", "C", "D"} {
		wantStatus(t, results, id, StatusOK)
	}
}

// --- Execute: sibling independence and failure propagation ---

func TestExecute_SiblingIndependenceAcrossDisjointBranches(t *testing.T) {
	errFail := errors.New("f failed")
	g := Graph{Nodes: []Node{
		{ID: "F", Run: func(ctx context.Context, in map[NodeID]any) (any, error) {
			return nil, errFail
		}},
		{ID: "X", Run: func(ctx context.Context, in map[NodeID]any) (any, error) {
			return "x", nil
		}},
		{ID: "Y", DependsOn: []NodeID{"X"}, Run: func(ctx context.Context, in map[NodeID]any) (any, error) {
			return "y", nil
		}},
		{ID: "Z", DependsOn: []NodeID{"Y"}, Run: func(ctx context.Context, in map[NodeID]any) (any, error) {
			return "z", nil
		}},
	}}
	results, err := Execute(context.Background(), g, 2, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantStatus(t, results, "F", StatusFailed)
	wantStatus(t, results, "X", StatusOK)
	wantStatus(t, results, "Y", StatusOK)
	wantStatus(t, results, "Z", StatusOK)
}

func TestExecute_FailureSkipsOnlyRealDescendants(t *testing.T) {
	errA := errors.New("a failed")
	var bCalled, cCalled atomic.Bool
	g := Graph{Nodes: []Node{
		{ID: "A", Run: func(ctx context.Context, in map[NodeID]any) (any, error) {
			return nil, errA
		}},
		{ID: "B", DependsOn: []NodeID{"A"}, Run: func(ctx context.Context, in map[NodeID]any) (any, error) {
			bCalled.Store(true)
			return nil, nil
		}},
		{ID: "C", DependsOn: []NodeID{"B"}, Run: func(ctx context.Context, in map[NodeID]any) (any, error) {
			cCalled.Store(true)
			return nil, nil
		}},
		{ID: "D", Run: func(ctx context.Context, in map[NodeID]any) (any, error) {
			return "d", nil
		}},
	}}
	results, err := Execute(context.Background(), g, 2, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantStatus(t, results, "A", StatusFailed)
	wantStatus(t, results, "B", StatusSkipped)
	wantStatus(t, results, "C", StatusSkipped)
	wantStatus(t, results, "D", StatusOK)
	if bCalled.Load() {
		t.Error("B.Run was invoked despite its dependency failing")
	}
	if cCalled.Load() {
		t.Error("C.Run was invoked despite its dependency chain failing")
	}
}

func TestExecute_PartialFailureInDiamondStillSkipsJoin(t *testing.T) {
	errB := errors.New("b failed")
	g := Graph{Nodes: []Node{
		{ID: "A", Run: func(ctx context.Context, in map[NodeID]any) (any, error) {
			return "a", nil
		}},
		{ID: "B", DependsOn: []NodeID{"A"}, Run: func(ctx context.Context, in map[NodeID]any) (any, error) {
			return nil, errB
		}},
		{ID: "C", DependsOn: []NodeID{"A"}, Run: func(ctx context.Context, in map[NodeID]any) (any, error) {
			return "c", nil
		}},
		{ID: "D", DependsOn: []NodeID{"B", "C"}, Run: func(ctx context.Context, in map[NodeID]any) (any, error) {
			return "d", nil
		}},
	}}
	results, err := Execute(context.Background(), g, 2, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantStatus(t, results, "A", StatusOK)
	wantStatus(t, results, "B", StatusFailed)
	wantStatus(t, results, "C", StatusOK)
	wantStatus(t, results, "D", StatusSkipped)
}

func TestExecute_SkipErrorIsTraceableToRootCause(t *testing.T) {
	rootErr := errors.New("root cause")
	g := Graph{Nodes: []Node{
		{ID: "A", Run: func(ctx context.Context, in map[NodeID]any) (any, error) {
			return nil, rootErr
		}},
		{ID: "B", DependsOn: []NodeID{"A"}, Run: func(ctx context.Context, in map[NodeID]any) (any, error) {
			return nil, nil
		}},
		{ID: "C", DependsOn: []NodeID{"B"}, Run: func(ctx context.Context, in map[NodeID]any) (any, error) {
			return nil, nil
		}},
	}}
	results, err := Execute(context.Background(), g, 1, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantStatus(t, results, "C", StatusSkipped)
	c := results["C"]
	if !errors.Is(c.Err, ErrSkipped) {
		t.Errorf("C's error does not wrap ErrSkipped: %v", c.Err)
	}
	if !errors.Is(c.Err, rootErr) {
		t.Errorf("C's error is not traceable to the root cause: %v", c.Err)
	}
}

func TestExecute_AllNodesAlwaysAccountedFor(t *testing.T) {
	errB := errors.New("b failed")
	g := Graph{Nodes: []Node{
		{ID: "A", Run: func(ctx context.Context, in map[NodeID]any) (any, error) {
			return "a", nil
		}},
		{ID: "B", Run: func(ctx context.Context, in map[NodeID]any) (any, error) {
			return nil, errB
		}},
		{ID: "C", DependsOn: []NodeID{"B"}, Run: func(ctx context.Context, in map[NodeID]any) (any, error) {
			return nil, nil
		}},
		{ID: "D", Run: func(ctx context.Context, in map[NodeID]any) (any, error) {
			return "d", nil
		}},
	}}
	results, err := Execute(context.Background(), g, 2, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != len(g.Nodes) {
		t.Errorf("len(results) = %d, want %d", len(results), len(g.Nodes))
	}
}

// --- Execute: concurrency bound and determinism ---

func TestExecute_ConcurrencyLimitExactlyReached(t *testing.T) {
	for _, limit := range []int{3, 5} {
		var current, highWater atomic.Int32
		const n = 20
		nodes := make([]Node, n)
		for i := 0; i < n; i++ {
			id := NodeID(rune('A' + i))
			nodes[i] = Node{ID: id, Run: func(ctx context.Context, in map[NodeID]any) (any, error) {
				cur := current.Add(1)
				for {
					hw := highWater.Load()
					if cur <= hw || highWater.CompareAndSwap(hw, cur) {
						break
					}
				}
				time.Sleep(20 * time.Millisecond)
				current.Add(-1)
				return nil, nil
			}}
		}
		results, err := Execute(context.Background(), Graph{Nodes: nodes}, limit, nil, nil)
		if err != nil {
			t.Fatalf("limit=%d: unexpected error: %v", limit, err)
		}
		if len(results) != n {
			t.Fatalf("limit=%d: len(results) = %d, want %d", limit, len(results), n)
		}
		if got := int(highWater.Load()); got != limit {
			t.Errorf("limit=%d: high-water mark = %d, want exactly %d", limit, got, limit)
		}
	}
}

func TestExecute_ConcurrencyIsRealParallelism(t *testing.T) {
	const n = 6
	const k = 3

	// A barrier that only opens once exactly k goroutines are concurrently
	// blocked inside it: each arrival is counted and cannot un-arrive before
	// the barrier releases, so reaching k structurally proves k nodes were
	// in flight at once — no polling/sampling window to alias, unlike a
	// design where each goroutine independently re-checks a shared counter.
	var arrived atomic.Int32
	release := make(chan struct{})
	var closeOnce sync.Once

	nodes := make([]Node, n)
	for i := 0; i < n; i++ {
		id := NodeID(rune('A' + i))
		nodes[i] = Node{ID: id, Run: func(ctx context.Context, in map[NodeID]any) (any, error) {
			if arrived.Add(1) == k {
				closeOnce.Do(func() { close(release) })
			}
			select {
			case <-release:
			case <-time.After(3 * time.Second):
				return nil, errors.New("timed out waiting for k concurrent arrivals")
			}
			return nil, nil
		}}
	}
	results, err := runWithTimeout(t, context.Background(), Graph{Nodes: nodes}, k, 5*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, node := range nodes {
		wantStatus(t, results, node.ID, StatusOK)
	}
}

func TestExecute_DeterministicDispatchOrderAtLimitOne(t *testing.T) {
	ids := []NodeID{"A", "B", "C", "D", "E"}
	makeGraph := func(order *[]NodeID, mu *sync.Mutex) Graph {
		nodes := make([]Node, len(ids))
		for i, id := range ids {
			id := id
			nodes[i] = Node{ID: id, Run: func(ctx context.Context, in map[NodeID]any) (any, error) {
				mu.Lock()
				*order = append(*order, id)
				mu.Unlock()
				return nil, nil
			}}
		}
		return Graph{Nodes: nodes}
	}

	var firstOrder []NodeID
	for run := 0; run < 20; run++ {
		var order []NodeID
		var mu sync.Mutex
		g := makeGraph(&order, &mu)
		results, err := Execute(context.Background(), g, 1, nil, nil)
		if err != nil {
			t.Fatalf("run %d: unexpected error: %v", run, err)
		}
		if len(results) != len(ids) {
			t.Fatalf("run %d: len(results) = %d, want %d", run, len(results), len(ids))
		}
		if run == 0 {
			firstOrder = order
			continue
		}
		if len(order) != len(firstOrder) {
			t.Fatalf("run %d: order length %d, want %d", run, len(order), len(firstOrder))
		}
		for i := range order {
			if order[i] != firstOrder[i] {
				t.Fatalf("run %d: dispatch order = %v, want %v (run 0's order)", run, order, firstOrder)
			}
		}
	}
	// The graph has no edges, so with limit=1 the schedule must be exactly
	// ascending NodeID order — the deterministic tie-break.
	want := []NodeID{"A", "B", "C", "D", "E"}
	for i := range want {
		if firstOrder[i] != want[i] {
			t.Errorf("dispatch order = %v, want %v", firstOrder, want)
		}
	}
}

// --- Execute: opacity, panics, cancellation ---

type opaqueVal struct {
	f func() int
}

func TestExecute_OutputIsOpaque(t *testing.T) {
	const marker = 424242
	g := Graph{Nodes: []Node{
		{ID: "A", Run: func(ctx context.Context, in map[NodeID]any) (any, error) {
			return opaqueVal{f: func() int { return marker }}, nil
		}},
		{ID: "B", DependsOn: []NodeID{"A"}, Run: func(ctx context.Context, in map[NodeID]any) (any, error) {
			got, ok := in["A"].(opaqueVal)
			if !ok {
				t.Errorf("inputs[A] has wrong type: %T", in["A"])
				return nil, nil
			}
			if got.f() != marker {
				t.Error("inputs[A] arrived changed — output was not passed through verbatim")
			}
			return nil, nil
		}},
	}}
	results, err := Execute(context.Background(), g, 1, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error (Execute should never inspect opaque output): %v", err)
	}
	wantStatus(t, results, "A", StatusOK)
	wantStatus(t, results, "B", StatusOK)
}

func TestExecute_PanicIsContained(t *testing.T) {
	g := Graph{Nodes: []Node{
		{ID: "P", Run: func(ctx context.Context, in map[NodeID]any) (any, error) {
			panic("boom")
		}},
		{ID: "D", DependsOn: []NodeID{"P"}, Run: func(ctx context.Context, in map[NodeID]any) (any, error) {
			return nil, nil
		}},
		{ID: "S", Run: func(ctx context.Context, in map[NodeID]any) (any, error) {
			return "s", nil
		}},
	}}
	results, err := Execute(context.Background(), g, 2, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantStatus(t, results, "P", StatusFailed)
	if results["P"].Err == nil {
		t.Error("expected P's Result.Err to name the panic, got nil")
	}
	wantStatus(t, results, "D", StatusSkipped)
	wantStatus(t, results, "S", StatusOK)
}

func TestExecute_AlreadyCancelledContextSkipsEverythingPromptly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var calls atomic.Int32
	g := Graph{Nodes: []Node{
		{ID: "A", Run: func(ctx context.Context, in map[NodeID]any) (any, error) {
			calls.Add(1)
			return nil, nil
		}},
		{ID: "B", DependsOn: []NodeID{"A"}, Run: func(ctx context.Context, in map[NodeID]any) (any, error) {
			calls.Add(1)
			return nil, nil
		}},
		{ID: "C", DependsOn: []NodeID{"B"}, Run: func(ctx context.Context, in map[NodeID]any) (any, error) {
			calls.Add(1)
			return nil, nil
		}},
	}}

	results, err := runWithTimeout(t, ctx, g, 1, 2*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, id := range []NodeID{"A", "B", "C"} {
		wantStatus(t, results, id, StatusSkipped)
		if !errors.Is(results[id].Err, context.Canceled) {
			t.Errorf("node %q: err = %v, want errors.Is(..., context.Canceled)", id, results[id].Err)
		}
	}
	if calls.Load() != 0 {
		t.Errorf("expected zero invocations against an already-canceled context, got %d", calls.Load())
	}
}

func TestExecute_CancellationMidRunPreservesFinishedResults(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	slowStarted := make(chan struct{})

	g := Graph{Nodes: []Node{
		{ID: "A_Fast", Run: func(ctx context.Context, in map[NodeID]any) (any, error) {
			return "fast-output", nil
		}},
		{ID: "B_Slow", Run: func(ctx context.Context, in map[NodeID]any) (any, error) {
			close(slowStarted)
			<-ctx.Done()
			return nil, ctx.Err()
		}},
		{ID: "C_Pending", Run: func(ctx context.Context, in map[NodeID]any) (any, error) {
			t.Error("C_Pending should never have started")
			return nil, nil
		}},
	}}

	type out struct {
		results map[NodeID]Result
		err     error
	}
	done := make(chan out, 1)
	go func() {
		r, err := Execute(ctx, g, 1, nil, nil)
		done <- out{r, err}
	}()

	<-slowStarted
	cancel()

	select {
	case o := <-done:
		if o.err != nil {
			t.Fatalf("unexpected error: %v", o.err)
		}
		wantStatus(t, o.results, "A_Fast", StatusOK)
		if o.results["A_Fast"].Output != "fast-output" {
			t.Errorf("A_Fast.Output = %v, want %q", o.results["A_Fast"].Output, "fast-output")
		}
		if o.results["B_Slow"].Status != StatusFailed || !errors.Is(o.results["B_Slow"].Err, context.Canceled) {
			t.Errorf("B_Slow result = %+v, want Failed wrapping context.Canceled", o.results["B_Slow"])
		}
		wantStatus(t, o.results, "C_Pending", StatusSkipped)
		if !errors.Is(o.results["C_Pending"].Err, context.Canceled) {
			t.Errorf("C_Pending.Err = %v, want errors.Is(..., context.Canceled)", o.results["C_Pending"].Err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Execute did not return in time — possible goroutine leak")
	}
}

// --- Execute: onStart/onResult observer contracts ---

// diamondWithFailingRoot builds a → b,c; b,c → d, with a failing, so b, c,
// and d are all skipped. Shared by the observer tests below.
func diamondWithFailingRoot(t *testing.T) Graph {
	t.Helper()
	errA := errors.New("a failed")
	return Graph{Nodes: []Node{
		{ID: "a", Run: func(ctx context.Context, in map[NodeID]any) (any, error) {
			return nil, errA
		}},
		{ID: "b", DependsOn: []NodeID{"a"}, Run: func(ctx context.Context, in map[NodeID]any) (any, error) {
			return nil, nil
		}},
		{ID: "c", DependsOn: []NodeID{"a"}, Run: func(ctx context.Context, in map[NodeID]any) (any, error) {
			return nil, nil
		}},
		{ID: "d", DependsOn: []NodeID{"b", "c"}, Run: func(ctx context.Context, in map[NodeID]any) (any, error) {
			return nil, nil
		}},
	}}
}

func TestExecute_OnResultCalledOnceForEveryNodeIncludingSkipped(t *testing.T) {
	g := diamondWithFailingRoot(t)
	var got []Result
	onResult := func(r Result) { got = append(got, r) }

	results, err := Execute(context.Background(), g, 2, nil, onResult)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("onResult called %d times, want 4", len(got))
	}
	byID := make(map[NodeID]Result, len(got))
	for _, r := range got {
		byID[r.NodeID] = r
	}
	for _, id := range []NodeID{"b", "c", "d"} {
		if byID[id].Status != StatusSkipped {
			t.Errorf("onResult's %q: status = %v, want StatusSkipped", id, byID[id].Status)
		}
	}
	if len(byID) != len(results) {
		t.Fatalf("onResult reported %d distinct nodes, want %d", len(byID), len(results))
	}
	for id, want := range results {
		if got := byID[id]; got != want {
			t.Errorf("onResult's %q = %+v, want %+v (the same value in the returned map)", id, got, want)
		}
	}
}

func TestExecute_OnStartCalledOnlyForDispatchedNodesNeverForSkipped(t *testing.T) {
	// A diamond where the root (a) succeeds and one branch (b) fails, so
	// only the join (d) is skipped — a, b, and c are all actually
	// dispatched (b's Run runs and returns an error; it isn't skipped).
	// Unlike diamondWithFailingRoot above (root fails, so every
	// descendant — b, c, and d — is skipped and never dispatched), this
	// is the shape needed to prove onStart fires for every dispatched
	// node and only for those.
	errB := errors.New("b failed")
	g := Graph{Nodes: []Node{
		{ID: "a", Run: func(ctx context.Context, in map[NodeID]any) (any, error) { return "a", nil }},
		{ID: "b", DependsOn: []NodeID{"a"}, Run: func(ctx context.Context, in map[NodeID]any) (any, error) { return nil, errB }},
		{ID: "c", DependsOn: []NodeID{"a"}, Run: func(ctx context.Context, in map[NodeID]any) (any, error) { return "c", nil }},
		{ID: "d", DependsOn: []NodeID{"b", "c"}, Run: func(ctx context.Context, in map[NodeID]any) (any, error) { return nil, nil }},
	}}
	var started []NodeID
	onStart := func(id NodeID) { started = append(started, id) }

	_, err := Execute(context.Background(), g, 2, onStart, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	set := make(map[NodeID]bool, len(started))
	for _, id := range started {
		set[id] = true
	}
	want := map[NodeID]bool{"a": true, "b": true, "c": true}
	if len(set) != len(want) {
		t.Fatalf("onStart ids = %v, want exactly %v", started, want)
	}
	for id := range want {
		if !set[id] {
			t.Errorf("onStart never called for dispatched node %q", id)
		}
	}
	if set["d"] {
		t.Error("onStart called for node d, which was skipped and never dispatched")
	}
}

func TestExecute_OnStartFiresBeforeOnResultForEveryNode(t *testing.T) {
	g := Graph{Nodes: []Node{
		{ID: "A", Run: func(ctx context.Context, in map[NodeID]any) (any, error) { return "a", nil }},
		{ID: "B", DependsOn: []NodeID{"A"}, Run: func(ctx context.Context, in map[NodeID]any) (any, error) { return "b", nil }},
		{ID: "C", Run: func(ctx context.Context, in map[NodeID]any) (any, error) { return "c", nil }},
	}}

	var seq int
	startIdx := make(map[NodeID]int)
	resultIdx := make(map[NodeID]int)
	onStart := func(id NodeID) {
		seq++
		startIdx[id] = seq
	}
	onResult := func(r Result) {
		seq++
		resultIdx[r.NodeID] = seq
	}

	_, err := Execute(context.Background(), g, 2, onStart, onResult)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, id := range []NodeID{"A", "B", "C"} {
		si, ok := startIdx[id]
		if !ok {
			t.Fatalf("no onStart call recorded for %q", id)
		}
		ri, ok := resultIdx[id]
		if !ok {
			t.Fatalf("no onResult call recorded for %q", id)
		}
		if si >= ri {
			t.Errorf("node %q: onStart index %d, onResult index %d — want onStart strictly before onResult", id, si, ri)
		}
	}
}

func TestExecute_ObserversNeverCalledForInvalidGraphOrBadLimit(t *testing.T) {
	var startCalls, resultCalls atomic.Int32
	onStart := func(NodeID) { startCalls.Add(1) }
	onResult := func(Result) { resultCalls.Add(1) }

	cyclic := Graph{Nodes: []Node{
		{ID: "A", DependsOn: []NodeID{"B"}, Run: func(ctx context.Context, in map[NodeID]any) (any, error) { return nil, nil }},
		{ID: "B", DependsOn: []NodeID{"A"}, Run: func(ctx context.Context, in map[NodeID]any) (any, error) { return nil, nil }},
	}}
	if _, err := Execute(context.Background(), cyclic, 2, onStart, onResult); err == nil {
		t.Fatal("expected error for cyclic graph, got nil")
	}
	if startCalls.Load() != 0 || resultCalls.Load() != 0 {
		t.Errorf("cyclic graph: onStart calls = %d, onResult calls = %d, want 0 and 0", startCalls.Load(), resultCalls.Load())
	}

	valid := Graph{Nodes: []Node{
		{ID: "A", Run: func(ctx context.Context, in map[NodeID]any) (any, error) { return nil, nil }},
	}}
	if _, err := Execute(context.Background(), valid, 0, onStart, onResult); err == nil {
		t.Fatal("expected error for limit=0, got nil")
	}
	if startCalls.Load() != 0 || resultCalls.Load() != 0 {
		t.Errorf("limit=0: onStart calls = %d, onResult calls = %d, want 0 and 0", startCalls.Load(), resultCalls.Load())
	}
}

func TestExecute_ObserverPanicDoesNotCrashExecuteOrLoseResults(t *testing.T) {
	g := Graph{Nodes: []Node{
		{ID: "A", Run: func(ctx context.Context, in map[NodeID]any) (any, error) { return "a", nil }},
		{ID: "B", DependsOn: []NodeID{"A"}, Run: func(ctx context.Context, in map[NodeID]any) (any, error) { return "b", nil }},
		{ID: "C", Run: func(ctx context.Context, in map[NodeID]any) (any, error) { return "c", nil }},
	}}

	panickyResult := func(Result) { panic("boom in onResult") }
	results, err := Execute(context.Background(), g, 2, nil, panickyResult)
	if err != nil {
		t.Fatalf("unexpected error with panicking onResult: %v", err)
	}
	for _, id := range []NodeID{"A", "B", "C"} {
		wantStatus(t, results, id, StatusOK)
	}

	panickyStart := func(NodeID) { panic("boom in onStart") }
	results, err = Execute(context.Background(), g, 2, panickyStart, nil)
	if err != nil {
		t.Fatalf("unexpected error with panicking onStart: %v", err)
	}
	for _, id := range []NodeID{"A", "B", "C"} {
		wantStatus(t, results, id, StatusOK)
	}
}

func TestExecute_NilObserversBehaveExactlyAsBefore(t *testing.T) {
	makeGraph := func() Graph {
		return Graph{Nodes: []Node{
			{ID: "A", Run: func(ctx context.Context, in map[NodeID]any) (any, error) { return "a", nil }},
			{ID: "B", DependsOn: []NodeID{"A"}, Run: func(ctx context.Context, in map[NodeID]any) (any, error) { return "b", nil }},
			{ID: "C", DependsOn: []NodeID{"A"}, Run: func(ctx context.Context, in map[NodeID]any) (any, error) { return "c", nil }},
		}}
	}

	withNil, err := Execute(context.Background(), makeGraph(), 2, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	withNoop, err := Execute(context.Background(), makeGraph(), 2, func(NodeID) {}, func(Result) {})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(withNil) != len(withNoop) {
		t.Fatalf("len(withNil) = %d, len(withNoop) = %d, want equal", len(withNil), len(withNoop))
	}
	for id, want := range withNil {
		if got := withNoop[id]; got != want {
			t.Errorf("node %q: with no-op observers = %+v, want %+v (nil-observer run's value)", id, got, want)
		}
	}
}

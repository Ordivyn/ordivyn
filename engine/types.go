package engine

import (
	"context"
	"errors"
)

// NodeID identifies a node within one Graph. A distinct string type, not a
// bare string, so a signature like map[NodeID]any can't be confused with an
// arbitrary map[string]any at a call site. Zero runtime cost.
type NodeID string

// NodeFunc is the one shape of work the engine schedules. It receives the
// resolved Output of every dependency it declared, keyed by NodeID, and a
// run-scoped context for caller-initiated cancellation. The engine never
// inspects what it returns — only stores and forwards it verbatim.
//
// A plain func type, not an interface: exactly one shape of work exists in
// brick 1. An interface gets introduced only when a second, genuinely
// different-shaped implementation exists to justify it.
type NodeFunc func(ctx context.Context, inputs map[NodeID]any) (any, error)

// Node is one vertex: identity, declared dependency edges, and the work.
type Node struct {
	ID        NodeID
	DependsOn []NodeID
	Run       NodeFunc
}

// Graph is the entire caller-declared input. An unordered bag of nodes —
// structure comes only from DependsOn. A three-step linear workflow and a
// wide fan-out diamond use this exact same shape; there is no separate
// "chain" representation to later reconcile with a graph one.
type Graph struct {
	Nodes []Node
}

// Status is a node's terminal state.
type Status int

const (
	StatusOK Status = iota
	StatusFailed
	StatusSkipped // never ran: a declared dependency failed, was skipped, or the run's context was already done
)

// Result is one node's outcome. Output is opaque — the engine stores and
// forwards it, never inspects or validates it.
type Result struct {
	NodeID NodeID
	Status Status
	Output any
	Err    error
}

// ErrSkipped marks a Result whose node never ran. Wrapped (via %w) around
// the dependency's own error, so errors.Is(result.Err, ErrSkipped) and
// errors.Is(result.Err, theOriginalRootCause) both hold, transitively,
// through however many layers of skipping separate a node from the actual
// failure.
var ErrSkipped = errors.New("engine: skipped, a declared dependency did not succeed")

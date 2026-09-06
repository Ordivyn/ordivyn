package engine

import (
	"errors"
	"fmt"
	"sort"
)

// buildGraph does every structural check in one O(V+E) pass — duplicate
// node IDs, duplicate entries within one node's own DependsOn, dangling
// DependsOn — aggregating every problem found via errors.Join rather than
// returning on the first. Returns adjacency bookkeeping shared by Validate
// (a throwaway Kahn's-algorithm peel) and Execute (the live scheduler).
func buildGraph(g Graph) (byID map[NodeID]Node, indegree map[NodeID]int, dependents map[NodeID][]NodeID, err error) {
	byID = make(map[NodeID]Node, len(g.Nodes))
	var errs []error
	for _, n := range g.Nodes {
		if _, dup := byID[n.ID]; dup {
			errs = append(errs, fmt.Errorf("duplicate node id %q", n.ID))
			continue
		}
		byID[n.ID] = n
	}

	indegree = make(map[NodeID]int, len(byID))
	dependents = make(map[NodeID][]NodeID, len(byID))
	for id := range byID {
		indegree[id] = 0
	}
	for id, n := range byID {
		seen := make(map[NodeID]bool, len(n.DependsOn))
		for _, dep := range n.DependsOn {
			if seen[dep] {
				errs = append(errs, fmt.Errorf("node %q declares dependency %q more than once", id, dep))
				continue
			}
			seen[dep] = true
			if _, ok := byID[dep]; !ok {
				errs = append(errs, fmt.Errorf("node %q depends on unknown node %q", id, dep))
				continue
			}
			indegree[id]++
			dependents[dep] = append(dependents[dep], id)
		}
	}
	if len(errs) > 0 {
		return nil, nil, nil, errors.Join(errs...)
	}
	return byID, indegree, dependents, nil
}

// Validate checks structural validity only — cycles, dangling deps,
// duplicate IDs — and runs no node. Exported deliberately: validation and
// scheduling are two phases of one engine, and Execute calling this
// internally doesn't make a standalone check speculative — a dry-run/lint
// use case is an earned second caller of an already-committed two-phase
// model, not a guess about the future.
func Validate(g Graph) error {
	byID, indegree, dependents, err := buildGraph(g)
	if err != nil {
		return err
	}
	// indegree is this call's own fresh copy from buildGraph, decremented
	// in place as the peel proceeds — Execute gets its own separate copy via
	// its own buildGraph call, so mutating this one destructively costs
	// nothing.
	var queue []NodeID
	for id, d := range indegree {
		if d == 0 {
			queue = append(queue, id)
		}
	}
	sort.Slice(queue, func(i, j int) bool { return queue[i] < queue[j] })

	peeled := 0
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		peeled++
		for _, c := range dependents[id] {
			indegree[c]--
			if indegree[c] == 0 {
				queue = append(queue, c)
			}
		}
	}
	if peeled != len(byID) {
		var stuck []string
		for id, d := range indegree {
			if d > 0 {
				stuck = append(stuck, string(id))
			}
		}
		sort.Strings(stuck)
		return fmt.Errorf("engine: cycle detected, involves node(s): %v", stuck)
	}
	return nil
}

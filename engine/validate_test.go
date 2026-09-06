package engine

import (
	"strings"
	"testing"
)

func TestValidate_RejectsCycle(t *testing.T) {
	// A depends on C, C depends on B, B depends on A: A -> B -> C -> A.
	g := Graph{Nodes: []Node{
		{ID: "A", DependsOn: []NodeID{"C"}},
		{ID: "B", DependsOn: []NodeID{"A"}},
		{ID: "C", DependsOn: []NodeID{"B"}},
	}}
	err := Validate(g)
	if err == nil {
		t.Fatal("expected error for cyclic graph, got nil")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("expected error to mention 'cycle', got: %v", err)
	}
	for _, id := range []string{"A", "B", "C"} {
		if !strings.Contains(err.Error(), id) {
			t.Errorf("expected error to name node %q, got: %v", id, err)
		}
	}
}

func TestValidate_RejectsSelfDependency(t *testing.T) {
	g := Graph{Nodes: []Node{
		{ID: "A", DependsOn: []NodeID{"A"}},
	}}
	err := Validate(g)
	if err == nil {
		t.Fatal("expected error for self-dependency, got nil")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("expected error to mention 'cycle', got: %v", err)
	}
}

func TestValidate_RejectsDanglingDependency(t *testing.T) {
	g := Graph{Nodes: []Node{
		{ID: "A", DependsOn: []NodeID{"Z"}},
	}}
	err := Validate(g)
	if err == nil {
		t.Fatal("expected error for dangling dependency, got nil")
	}
	if !strings.Contains(err.Error(), "Z") {
		t.Errorf("expected error to name node %q, got: %v", "Z", err)
	}
}

func TestValidate_RejectsDuplicateNodeID(t *testing.T) {
	g := Graph{Nodes: []Node{
		{ID: "A"},
		{ID: "A"},
	}}
	err := Validate(g)
	if err == nil {
		t.Fatal("expected error for duplicate node id, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate node id") {
		t.Errorf("expected error to mention duplicate node id, got: %v", err)
	}
}

func TestValidate_RejectsDuplicateDependencyWithinOneNode(t *testing.T) {
	g := Graph{Nodes: []Node{
		{ID: "B"},
		{ID: "A", DependsOn: []NodeID{"B", "B"}},
	}}
	err := Validate(g)
	if err == nil {
		t.Fatal("expected error for duplicate dependency entry, got nil")
	}
	if !strings.Contains(err.Error(), "more than once") {
		t.Errorf("expected error to mention duplicate dependency, got: %v", err)
	}
}

func TestValidate_AcceptsDiamond(t *testing.T) {
	// A -> B, A -> C, B -> D, C -> D.
	g := Graph{Nodes: []Node{
		{ID: "A"},
		{ID: "B", DependsOn: []NodeID{"A"}},
		{ID: "C", DependsOn: []NodeID{"A"}},
		{ID: "D", DependsOn: []NodeID{"B", "C"}},
	}}
	if err := Validate(g); err != nil {
		t.Fatalf("expected no error for a legitimate diamond, got: %v", err)
	}
}

func TestValidate_ReportsAllProblemsAtOnce(t *testing.T) {
	g := Graph{Nodes: []Node{
		{ID: "A"},
		{ID: "A"},                           // duplicate node id
		{ID: "C", DependsOn: []NodeID{"Z"}}, // dangling dependency
	}}
	err := Validate(g)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "duplicate node id") {
		t.Errorf("expected message to contain the duplicate-id problem, got: %v", msg)
	}
	if !strings.Contains(msg, "depends on unknown node") {
		t.Errorf("expected message to contain the dangling-dependency problem, got: %v", msg)
	}
}

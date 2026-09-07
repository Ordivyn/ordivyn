package schema

import (
	"context"
	"strings"
	"testing"

	"github.com/Ordivyn/ordivyn/internal/engine"
)

func TestLoad_ValidShellWorkflowProducesGraph(t *testing.T) {
	g, err := Load("testdata/valid_two_node.yaml")
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if len(g.Nodes) != 2 {
		t.Fatalf("len(g.Nodes) = %d, want 2", len(g.Nodes))
	}
	byID := make(map[engine.NodeID]engine.Node, len(g.Nodes))
	for _, n := range g.Nodes {
		byID[n.ID] = n
	}
	a, ok := byID["a"]
	if !ok {
		t.Fatalf("node %q not found", "a")
	}
	if len(a.DependsOn) != 0 {
		t.Errorf("node a DependsOn = %v, want empty", a.DependsOn)
	}
	b, ok := byID["b"]
	if !ok {
		t.Fatalf("node %q not found", "b")
	}
	if len(b.DependsOn) != 1 || b.DependsOn[0] != "a" {
		t.Errorf("node b DependsOn = %v, want [a]", b.DependsOn)
	}

	results, err := engine.Execute(context.Background(), g, 4, nil, nil)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	for _, id := range []engine.NodeID{"a", "b"} {
		r, ok := results[id]
		if !ok {
			t.Fatalf("no result for node %q", id)
		}
		if r.Status != engine.StatusOK {
			t.Errorf("node %q status = %v, want StatusOK (err: %v)", id, r.Status, r.Err)
		}
	}
}

func TestLoad_UnknownTopLevelKeyRejected(t *testing.T) {
	_, err := Load("testdata/unknown_top_level_key.yaml")
	if err == nil {
		t.Fatal("Load returned nil error, want a rejection of the unknown key")
	}
}

func TestLoad_UnknownNodeKeyRejected(t *testing.T) {
	_, err := Load("testdata/unknown_node_key.yaml")
	if err == nil {
		t.Fatal("Load returned nil error, want a rejection of the unknown node key")
	}
}

func TestLoad_MissingIDRejected(t *testing.T) {
	_, err := Load("testdata/missing_id.yaml")
	if err == nil {
		t.Fatal("Load returned nil error, want missing id rejected")
	}
	if !strings.Contains(err.Error(), `missing required field "id"`) {
		t.Errorf("error = %q, want it to mention missing id", err.Error())
	}
}

func TestLoad_MissingTypeRejected(t *testing.T) {
	_, err := Load("testdata/missing_type.yaml")
	if err == nil {
		t.Fatal("Load returned nil error, want missing type rejected")
	}
	if !strings.Contains(err.Error(), `missing required field "type"`) {
		t.Errorf("error = %q, want it to mention missing type", err.Error())
	}
}

func TestLoad_UnknownTypeErrorListsBothKnownTypes(t *testing.T) {
	_, err := Load("testdata/unknown_type.yaml")
	if err == nil {
		t.Fatal("Load returned nil error, want unknown type rejected")
	}
	if !strings.Contains(err.Error(), `unknown type "bogus"`) {
		t.Errorf("error = %q, want it to name the bad value", err.Error())
	}
	if !strings.Contains(err.Error(), "shell") || !strings.Contains(err.Error(), "agent") {
		t.Errorf("error = %q, want it to list both known types", err.Error())
	}
}

func TestLoad_ShellNodeMissingCommandRejected(t *testing.T) {
	_, err := Load("testdata/missing_command.yaml")
	if err == nil {
		t.Fatal("Load returned nil error, want missing command rejected")
	}
	if !strings.Contains(err.Error(), `requires field "command"`) {
		t.Errorf("error = %q, want it to mention missing command", err.Error())
	}
}

func TestLoad_AggregatesMultipleNodeErrors(t *testing.T) {
	_, err := Load("testdata/two_bad_nodes.yaml")
	if err == nil {
		t.Fatal("Load returned nil error, want both bad nodes reported")
	}
	joined, ok := err.(interface{ Unwrap() []error })
	if !ok {
		t.Fatalf("error is not an errors.Join aggregate: %T", err)
	}
	errs := joined.Unwrap()
	if len(errs) != 2 {
		t.Fatalf("len(errs) = %d, want 2 (got: %v)", len(errs), errs)
	}
	if !strings.Contains(err.Error(), `unknown type "bogus"`) {
		t.Errorf("error = %q, want it to mention the unknown type", err.Error())
	}
	if !strings.Contains(err.Error(), `missing required field "id"`) {
		t.Errorf("error = %q, want it to mention the missing id", err.Error())
	}
}

func TestLoad_EmptyNodesProducesEmptyGraphNoError(t *testing.T) {
	g, err := Load("testdata/empty_nodes.yaml")
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if len(g.Nodes) != 0 {
		t.Errorf("len(g.Nodes) = %d, want 0", len(g.Nodes))
	}
}

func TestLoad_MalformedYAMLSyntaxRejected(t *testing.T) {
	_, err := Load("testdata/malformed.yaml")
	if err == nil {
		t.Fatal("Load returned nil error, want malformed YAML rejected")
	}
}

func TestLoad_DoesNotValidateGraphStructure(t *testing.T) {
	t.Run("dangling depends_on", func(t *testing.T) {
		g, err := Load("testdata/dangling_dependency.yaml")
		if err != nil {
			t.Fatalf("Load returned error: %v, want no error (structural checks are engine.Validate's job)", err)
		}
		if err := engine.Validate(g); err == nil {
			t.Fatal("engine.Validate returned nil error, want it to report the dangling dependency")
		}
	})

	t.Run("duplicate id", func(t *testing.T) {
		g, err := Load("testdata/duplicate_id.yaml")
		if err != nil {
			t.Fatalf("Load returned error: %v, want no error (structural checks are engine.Validate's job)", err)
		}
		if err := engine.Validate(g); err == nil {
			t.Fatal("engine.Validate returned nil error, want it to report the duplicate id")
		}
	})
}

func TestLoad_ValidAgentWorkflowProducesGraph(t *testing.T) {
	// Execution is not exercised here — decodeNode only builds the
	// closure; running it is agent_test.go's job.
	g, err := Load("testdata/valid_agent.yaml")
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if len(g.Nodes) != 1 {
		t.Fatalf("len(g.Nodes) = %d, want 1", len(g.Nodes))
	}
	if g.Nodes[0].ID != "a" {
		t.Errorf("g.Nodes[0].ID = %q, want %q", g.Nodes[0].ID, "a")
	}
}

func TestLoad_AgentNodeMissingPromptRejected(t *testing.T) {
	_, err := Load("testdata/agent_missing_prompt.yaml")
	if err == nil {
		t.Fatal("Load returned nil error, want missing prompt rejected")
	}
	if !strings.Contains(err.Error(), `requires field "prompt"`) {
		t.Errorf("error = %q, want it to mention missing prompt", err.Error())
	}
}

func TestLoad_AgentNodeWithStrayCommandFieldRejected(t *testing.T) {
	_, err := Load("testdata/agent_stray_command.yaml")
	if err == nil {
		t.Fatal("Load returned nil error, want stray command field rejected")
	}
	if !strings.Contains(err.Error(), `field "command" is not valid for type "agent"`) {
		t.Errorf("error = %q, want it to name the stray field", err.Error())
	}
}

func TestLoad_ShellNodeWithStrayPromptFieldRejected(t *testing.T) {
	_, err := Load("testdata/shell_stray_prompt.yaml")
	if err == nil {
		t.Fatal("Load returned nil error, want stray prompt field rejected")
	}
	if !strings.Contains(err.Error(), `field "prompt" is not valid for type "shell"`) {
		t.Errorf("error = %q, want it to name the stray field", err.Error())
	}
}

func TestLoad_MixedShellAndAgentWorkflowProducesGraph(t *testing.T) {
	g, err := Load("testdata/mixed_shell_and_agent.yaml")
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if len(g.Nodes) != 2 {
		t.Fatalf("len(g.Nodes) = %d, want 2", len(g.Nodes))
	}
	byID := make(map[engine.NodeID]engine.Node, len(g.Nodes))
	for _, n := range g.Nodes {
		byID[n.ID] = n
	}
	if _, ok := byID["a"]; !ok {
		t.Fatalf("node %q not found", "a")
	}
	b, ok := byID["b"]
	if !ok {
		t.Fatalf("node %q not found", "b")
	}
	if len(b.DependsOn) != 1 || b.DependsOn[0] != "a" {
		t.Errorf("node b DependsOn = %v, want [a]", b.DependsOn)
	}
}

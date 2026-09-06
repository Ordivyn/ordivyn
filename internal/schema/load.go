package schema

import (
	"bytes"
	"errors"
	"fmt"
	"os"

	"github.com/Ordivyn/ordivyn/internal/engine"
	"gopkg.in/yaml.v3"
)

// Load reads path, decodes it as one workflow YAML document, and compiles
// it into an engine.Graph. Load enforces only that the YAML itself is
// well-formed: no unknown keys anywhere in the document, every field
// required by a node's declared type present, every type recognized. Load
// never checks graph-level structural properties — cycles, dangling
// depends_on, duplicate ids — because that is engine.Validate's job, and
// duplicating it here would let the two checks drift.
//
// The returned error, when non-nil, is an aggregate (errors.Join) naming
// every problem found across every node — not just the first — matching
// the aggregation style engine.Validate already established for "rejects
// the graph and says why."
func Load(path string) (engine.Graph, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return engine.Graph{}, fmt.Errorf("schema: read %s: %w", path, err)
	}

	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true) // one strict pass: unknown keys fail here, not later
	var f file
	if err := dec.Decode(&f); err != nil {
		return engine.Graph{}, fmt.Errorf("schema: parse %s: %w", path, err)
	}

	var errs []error
	nodes := make([]engine.Node, 0, len(f.Nodes))
	for i, rn := range f.Nodes {
		n, err := decodeNode(rn, i)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		nodes = append(nodes, n)
	}
	if len(errs) > 0 {
		return engine.Graph{}, errors.Join(errs...)
	}
	return engine.Graph{Nodes: nodes}, nil
}

// decodeNode turns one rawNode into an engine.Node by switching on Type.
// A plain string switch, not a registry: exactly two cases exist today
// ("shell", "agent"). A registry designed before a third kind exists would
// be guessing at its own shape.
func decodeNode(n rawNode, index int) (engine.Node, error) {
	label := nodeLabel(n.ID, index)
	if n.ID == "" {
		return engine.Node{}, fmt.Errorf("%s: missing required field %q", label, "id")
	}
	if n.Type == "" {
		return engine.Node{}, fmt.Errorf("%s: missing required field %q", label, "type")
	}
	deps := make([]engine.NodeID, len(n.DependsOn))
	for i, d := range n.DependsOn {
		deps[i] = engine.NodeID(d)
	}
	switch n.Type {
	case "shell":
		if n.Prompt != "" {
			return engine.Node{}, fmt.Errorf("%s: field %q is not valid for type %q", label, "prompt", "shell")
		}
		if n.Command == "" {
			return engine.Node{}, fmt.Errorf("%s: type %q requires field %q", label, "shell", "command")
		}
		return engine.Node{ID: engine.NodeID(n.ID), DependsOn: deps, Run: newShellFunc(n.Command)}, nil
	case "agent":
		if n.Command != "" {
			return engine.Node{}, fmt.Errorf("%s: field %q is not valid for type %q", label, "command", "agent")
		}
		if n.Prompt == "" {
			return engine.Node{}, fmt.Errorf("%s: type %q requires field %q", label, "agent", "prompt")
		}
		return engine.Node{ID: engine.NodeID(n.ID), DependsOn: deps, Run: newAgentFunc(agentBin, agentArgs, n.Prompt)}, nil
	default:
		return engine.Node{}, fmt.Errorf("%s: unknown type %q (known types: shell, agent)", label, n.Type)
	}
}

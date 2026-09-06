package schema

import "fmt"

// file is the root shape of one workflow YAML document.
type file struct {
	Nodes []rawNode `yaml:"nodes"`
}

// rawNode is the one flat decode shape for every node in the file. Two
// node types exist now; Command belongs to "shell" and Prompt belongs to
// "agent". decodeNode rejects either field appearing on the other type —
// KnownFields only catches keys that don't exist anywhere in this struct,
// not a key that exists but belongs to a different type.
type rawNode struct {
	ID        string   `yaml:"id"`
	Type      string   `yaml:"type"`
	DependsOn []string `yaml:"depends_on"`
	Command   string   `yaml:"command"`
	Prompt    string   `yaml:"prompt"`
}

// nodeLabel names a node in an error message even when the field that
// would normally identify it (ID) is itself missing or blank.
func nodeLabel(id string, index int) string {
	if id == "" {
		return fmt.Sprintf("node[%d]", index)
	}
	return fmt.Sprintf("node[%d] (id %q)", index, id)
}

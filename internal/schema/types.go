package schema

import "fmt"

// file is the root shape of one workflow YAML document.
type file struct {
	Nodes []rawNode `yaml:"nodes"`
}

// rawNode is the one flat decode shape for every node in the file. Today
// there is exactly one node type ("shell"), so its one variant field
// (Command) lives directly on this struct instead of behind a second type.
// A second kind will require reworking this: a stray field belonging to
// that kind would decode silently onto a "shell" node instead of being
// caught as an unknown key.
type rawNode struct {
	ID        string   `yaml:"id"`
	Type      string   `yaml:"type"`
	DependsOn []string `yaml:"depends_on"`
	Command   string   `yaml:"command"`
}

// nodeLabel names a node in an error message even when the field that
// would normally identify it (ID) is itself missing or blank.
func nodeLabel(id string, index int) string {
	if id == "" {
		return fmt.Sprintf("node[%d]", index)
	}
	return fmt.Sprintf("node[%d] (id %q)", index, id)
}

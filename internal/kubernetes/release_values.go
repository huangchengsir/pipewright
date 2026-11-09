package kubernetes

import (
	"bytes"
	"gopkg.in/yaml.v3"
	"io"
)

func RenderReleaseValues(data []byte, vars map[string]ReleaseVariableValue) ([]byte, error) {
	if len(data) > MaxReleaseManifestBytes {
		return nil, ErrManifest
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return []byte("{}\n"), nil
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var node yaml.Node
	if decoder.Decode(&node) != nil || decoder.Decode(new(yaml.Node)) != io.EOF || len(node.Content) != 1 || node.Content[0].Kind != yaml.MappingNode {
		return nil, ErrManifest
	}
	sensitive := false
	count := 0
	if e := renderReleaseNode(&node, vars, &sensitive, 0, &count); e != nil {
		return nil, e
	}
	out, e := yaml.Marshal(&node)
	if e != nil || len(out) > MaxReleaseManifestBytes {
		return nil, ErrManifest
	}
	return out, nil
}

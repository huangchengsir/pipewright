package kubernetes

import (
	"gopkg.in/yaml.v3"
	"strings"
)

func releaseIdentitySecret(node *yaml.Node, vars map[string]ReleaseVariableValue, parent string) bool {
	if node.Kind == yaml.MappingNode {
		for index := 0; index < len(node.Content); index += 2 {
			key, value := node.Content[index].Value, node.Content[index+1]
			if (key == "name" || key == "namespace") && (parent == "metadata" || parent == "containers" || parent == "initContainers" || parent == "ephemeralContainers") {
				for _, match := range placeholder.FindAllStringSubmatchIndex(value.Value, -1) {
					if match[0] > 0 && value.Value[match[0]-1] == '$' {
						continue
					}
					name := ""
					if match[2] >= 0 {
						name = value.Value[match[2]:match[3]]
					} else {
						name = strings.TrimSpace(value.Value[match[4]:match[5]])
					}
					if vars[name].Secret {
						return true
					}
				}
			}
			if key == "data" || key == "stringData" || key == "binaryData" {
				continue
			}
			if releaseIdentitySecret(value, vars, key) {
				return true
			}
		}
	} else {
		for _, child := range node.Content {
			if releaseIdentitySecret(child, vars, parent) {
				return true
			}
		}
	}
	return false
}

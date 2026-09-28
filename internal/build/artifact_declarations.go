package build

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"
)

type artifactDeclaration struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

func artifactDeclarations(cfg map[string]any) ([]artifactDeclaration, bool, error) {
	raw, ok := cfg["artifacts"]
	if !ok || raw == nil {
		return nil, false, nil
	}
	var encoded []byte
	switch value := raw.(type) {
	case string:
		if value == "" {
			return nil, false, nil
		}
		encoded = []byte(value)
	default:
		var err error
		encoded, err = json.Marshal(value)
		if err != nil {
			return nil, true, fmt.Errorf("invalid artifact declarations: %w", err)
		}
	}
	var declarations []artifactDeclaration
	if err := json.Unmarshal(encoded, &declarations); err != nil {
		return nil, true, fmt.Errorf("invalid artifact declarations: %w", err)
	}
	if len(declarations) == 0 {
		return nil, true, errors.New("artifact declarations must not be empty")
	}
	for i := range declarations {
		declarations[i].Path = strings.TrimSpace(declarations[i].Path)
		declarations[i].Name = strings.TrimSpace(declarations[i].Name)
		if declarations[i].Path == "" {
			return nil, true, fmt.Errorf("artifact declaration %d has no path", i+1)
		}
		if len([]rune(declarations[i].Name)) > 128 || strings.IndexFunc(declarations[i].Name, unicode.IsControl) >= 0 {
			return nil, true, fmt.Errorf("artifact declaration %d has an invalid name", i+1)
		}
	}
	return declarations, true, nil
}

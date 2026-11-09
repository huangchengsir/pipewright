package kubeprotocol

import (
	"errors"
	"strings"
)

// BindTypedList restores item TypeMeta omitted by Kubernetes typed list codecs.
// The caller must obtain version and kind from the exact authorized collection,
// never from an item or a guessed plural. Each page must pass before merging.
func BindTypedList(list map[string]any, version, kind string) error {
	invalid := errors.New("typed_list_identity_mismatch")
	if list == nil || version == "" || kind == "" {
		return invalid
	}
	bind := func(object map[string]any, expectedKind string, missing bool) bool {
		if object == nil {
			return false
		}
		for key := range object {
			if (strings.EqualFold(key, "apiVersion") && key != "apiVersion") || (strings.EqualFold(key, "kind") && key != "kind") {
				return false
			}
		}
		for key, expected := range map[string]string{"apiVersion": version, "kind": expectedKind} {
			value, exists := object[key]
			if !exists || value == "" {
				if !missing {
					return false
				}
				object[key] = expected
			} else if value != expected {
				return false
			}
		}
		return true
	}
	if !bind(list, kind+"List", false) {
		return invalid
	}
	raw, exists := list["items"]
	if !exists {
		return invalid
	}
	if raw == nil {
		list["items"] = []any{}
		return nil
	}
	items, ok := raw.([]any)
	if !ok || len(items) > 4096 {
		return invalid
	}
	for _, rawItem := range items {
		item, ok := rawItem.(map[string]any)
		if !ok || !bind(item, kind, true) {
			return invalid
		}
	}
	return nil
}

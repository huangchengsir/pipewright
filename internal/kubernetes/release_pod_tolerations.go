package kubernetes

import (
	"encoding/json"
	"strconv"
)

func releaseAdmissionInteger(value any) (int64, bool) {
	var text string
	switch v := value.(type) {
	case json.Number:
		text = string(v)
	case int:
		return int64(v), true
	case int64:
		return v, true
	case float64:
		text = strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return 0, false
	}
	n, e := strconv.ParseInt(text, 10, 64)
	return n, e == nil
}

type releaseToleration struct {
	key, operator, value, effect string
	seconds                      int64
	limited                      bool
}

func releaseReadToleration(raw any) (t releaseToleration, valid bool) {
	m, ok := raw.(map[string]any)
	if !ok {
		return t, false
	}
	for key, value := range m {
		switch key {
		case "key", "operator", "value", "effect":
			s, ok := value.(string)
			if !ok {
				return t, false
			}
			switch key {
			case "key":
				t.key = s
			case "operator":
				t.operator = s
			case "value":
				t.value = s
			case "effect":
				t.effect = s
			}
		case "tolerationSeconds":
			if value != nil {
				var ok bool
				t.seconds, ok = releaseAdmissionInteger(value)
				if !ok {
					return t, false
				}
				t.limited = true
			}
		default:
			return t, false
		}
	}
	if t.operator == "" {
		t.operator = "Equal"
	}
	return t, true
}

// DefaultTolerationSeconds applies only to Pods, not their controller template.
// Preserve each approved entry and accept only the two missing native additions.
// Cluster flags supply signed int64 seconds; no plugin manager marker is assumed.
func releasePodTolerationsMatch(want, actual any) bool {
	values := func(v any) ([]any, bool) {
		if v == nil {
			return nil, true
		}
		a, ok := v.([]any)
		return a, ok
	}
	w, ok := values(want)
	if !ok {
		return false
	}
	a, ok := values(actual)
	if !ok || len(a) < len(w) {
		return false
	}
	covered := map[string]bool{}
	for i, raw := range w {
		declared, ok := releaseReadToleration(raw)
		if !ok {
			return false
		}
		seen, ok := releaseReadToleration(a[i])
		if !ok || declared != seen {
			return false
		}
		for _, key := range []string{"node.kubernetes.io/not-ready", "node.kubernetes.io/unreachable"} {
			if (declared.key == key || declared.key == "") && (declared.effect == "NoExecute" || declared.effect == "") {
				covered[key] = true
			}
		}
	}
	for _, raw := range a[len(w):] {
		entry, ok := releaseReadToleration(raw)
		if !ok || entry.operator != "Exists" || entry.value != "" || entry.effect != "NoExecute" || !entry.limited || covered[entry.key] {
			return false
		}
		if entry.key != "node.kubernetes.io/not-ready" && entry.key != "node.kubernetes.io/unreachable" {
			return false
		}
		covered[entry.key] = true
	}
	return true
}

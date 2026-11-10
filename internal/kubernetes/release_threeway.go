package kubernetes

import "sort"

// releaseThreeWay preserves unowned map keys and named-list members. The old
// approved intent, not a live/defaulted object, defines what may be removed.
// Ambiguous atomic-list changes and changes to unowned existing values fail.
func releaseThreeWay(previous, current, desired map[string]any, partial bool) (map[string]any, map[string]any, map[string]any, error) {
	if partial {
		var err error
		desired, err = releaseOverlayIntent(previous, desired)
		if err != nil {
			return nil, nil, nil, err
		}
	}
	target, managed, _, err := releaseMergeIntent(previous, true, current, true, desired, true, "", releaseNativeListSchema(previous, current, desired))
	if err != nil {
		return nil, nil, nil, err
	}
	result, owned := releaseMap(target), releaseMap(managed)
	patch, _ := releaseMergeDifference(current, result).(map[string]any)
	if patch == nil {
		patch = map[string]any{}
	}
	return result, owned, patch, nil
}
func releaseSameValue(a, b any) bool { return ReleaseObjectMatches(a, b) && ReleaseObjectMatches(b, a) }

// Atomic values remain positional and symmetric. Only their exact native field
// context may equate officially omitted zeros inside array elements; unknown
// fields, extra members and real drift still fail in either direction.
func releaseSameValueAt(a, b any, path string, schema releaseListSchema) bool {
	return releaseObjectMatchesAt(a, b, path, schema) && releaseObjectMatchesAt(b, a, path, schema)
}

func releaseOverlayIntent(base, changes map[string]any) (map[string]any, error) {
	return releaseOverlayAt(base, changes, "", releaseNativeListSchema(base, changes))
}
func releaseOverlayAt(base, changes map[string]any, path string, schema releaseListSchema) (map[string]any, error) {
	out := releaseObjectCopy(base)
	if out == nil {
		out = map[string]any{}
	}
	for key, value := range changes {
		child := releaseFieldPath(path, key)
		if bm, ok := out[key].(map[string]any); ok {
			if cm, ok := value.(map[string]any); ok {
				value, err := releaseOverlayAt(bm, cm, child, schema)
				if err != nil {
					return nil, err
				}
				out[key] = value
				continue
			}
		}
		if old, ok := out[key].([]any); ok {
			if next, ok := value.([]any); ok {
				field := releaseNativeListField(child, schema)
				if field != "" && releaseNativeNamedLists(field, old, next) {
					merged := append([]any{}, old...)
					for _, item := range next {
						k, _ := releaseTemplateListKey(field, item)
						found := false
						for i, v := range merged {
							prior, _ := releaseTemplateListKey(field, v)
							if prior == k {
								value, err := releaseOverlayAt(releaseMap(v), releaseMap(item), child+"/*", schema)
								if err != nil {
									return nil, err
								}
								merged[i] = value
								found = true
								break
							}
						}
						if !found {
							merged = append(merged, item)
						}
					}
					if field == "env" || field == "initContainers" {
						selected := map[string]bool{}
						for _, v := range next {
							k, _ := releaseTemplateListKey(field, v)
							selected[k] = true
						}
						touched := []any{}
						for _, v := range old {
							k, _ := releaseTemplateListKey(field, v)
							if selected[k] {
								touched = append(touched, v)
							}
						}
						var err error
						merged, err = releaseOrderNativeMembers(field, touched, old, next, merged)
						if err != nil {
							return nil, err
						}
					}
					out[key] = merged
					continue
				}
			}
		}
		out[key] = value
	}
	return out, nil
}
func releaseNativeNamedLists(field string, lists ...[]any) bool {
	for _, list := range lists {
		seen := map[string]bool{}
		for _, v := range list {
			key, ok := releaseTemplateListKey(field, v)
			if !ok || seen[key] {
				return false
			}
			seen[key] = true
		}
	}
	return true
}
func releaseMergeIntent(previous any, hadPrevious bool, current any, hasCurrent bool, desired any, hasDesired bool, path string, schema releaseListSchema) (any, any, bool, error) {
	// A native serializer may omit an explicitly owned optional zero. Preserve
	// its ownership/declaration and compare that virtual old value, never adopt
	// external nonzero drift or rewrite the stored payload.
	if hadPrevious && !hasCurrent && releaseNativeZeroAt(schema, path, previous) {
		current = previous
		hasCurrent = true
	}
	if !hadPrevious && hasCurrent && releaseNativeZeroAt(schema, path, current) {
		current = nil
		hasCurrent = false
	}

	if !hasDesired || desired == nil {
		if !hadPrevious {
			return current, nil, hasCurrent, nil
		}
		if old, ok := previous.(map[string]any); ok {
			live, ok := current.(map[string]any)
			if !ok && hasCurrent {
				return nil, nil, false, ErrSuperseded
			}
			result, _, keep, e := releaseMergeIntent(old, true, live, true, map[string]any{}, true, path, schema)
			if e != nil {
				return nil, nil, false, e
			}
			if len(releaseMap(result)) == 0 {
				keep = false
			}
			return result, nil, keep, nil
		}
		if old, ok := previous.([]any); ok {
			live, ok := current.([]any)
			if !ok && hasCurrent {
				return nil, nil, false, ErrSuperseded
			}
			if field := releaseNativeListField(path, schema); field != "" {
				if !releaseNativeNamedLists(field, old, live) {
					return nil, nil, false, ErrSuperseded
				}
				result, _, _, e := releaseMergeNamed(old, live, nil, path, schema)
				if e != nil {
					return nil, nil, false, e
				}
				return result, nil, len(result) > 0, nil
			}
		}
		if hasCurrent && !releaseSameValueAt(previous, current, path, schema) {
			return nil, nil, false, ErrSuperseded
		}
		return nil, nil, false, nil
	}
	if next, ok := desired.(map[string]any); ok {
		old, oldMap := previous.(map[string]any)
		live, liveMap := current.(map[string]any)
		if hadPrevious && !oldMap || hasCurrent && !liveMap {
			return nil, nil, false, ErrSuperseded
		}
		result := map[string]any{}
		for k, v := range live {
			result[k] = v
		}
		owned := map[string]any{}
		keys := map[string]bool{}
		for k := range old {
			keys[k] = true
		}
		for k := range next {
			keys[k] = true
		}
		sorted := make([]string, 0, len(keys))
		for k := range keys {
			sorted = append(sorted, k)
		}
		sort.Strings(sorted)
		for _, key := range sorted {
			pv, po := old[key]
			cv, co := live[key]
			nv, no := next[key]
			value, managed, keep, e := releaseMergeIntent(pv, po, cv, co, nv, no, releaseFieldPath(path, key), schema)
			if e != nil {
				return nil, nil, false, e
			}
			if keep {
				result[key] = value
			} else {
				delete(result, key)
			}
			if managed != nil {
				owned[key] = managed
			}
		}
		return result, owned, true, nil
	}
	if next, ok := desired.([]any); ok {
		old, oldList := previous.([]any)
		live, liveList := current.([]any)
		if hadPrevious && !oldList || hasCurrent && !liveList {
			return nil, nil, false, ErrSuperseded
		}
		if field := releaseNativeListField(path, schema); field != "" {
			if !releaseNativeNamedLists(field, old, live, next) {
				return nil, nil, false, ErrSuperseded
			}
			result, owned, _, e := releaseMergeNamed(old, live, next, path, schema)
			return result, owned, true, e
		}
		if hadPrevious && !releaseSameValueAt(previous, current, path, schema) {
			if schema.pod && path == schema.podPath+"/tolerations" && releaseSameValueAt(previous, desired, path, schema) && releaseTemplateMatchesAt(previous, current, path, schema, true) {
				return current, previous, true, nil
			}
			return nil, nil, false, ErrSuperseded
		}
	}
	if hadPrevious {
		if !hasCurrent || !releaseSameValueAt(previous, current, path, schema) {
			return nil, nil, false, ErrSuperseded
		}
		return desired, desired, true, nil
	}
	if hasCurrent {
		if !releaseSameValueAt(current, desired, path, schema) {
			return nil, nil, false, ErrSuperseded
		}
		// The field existed outside our previous intent. Equal-value declarations
		// may observe it but do not acquire authority to remove it next time.
		return current, nil, true, nil
	}
	return desired, desired, true, nil
}
func releaseMergeNamed(old, live, next []any, path string, schema releaseListSchema) ([]any, []any, bool, error) {
	field := releaseNativeListField(path, schema)
	before, after := map[string]map[string]any{}, map[string]map[string]any{}
	liveIndex := map[string]int{}
	for _, v := range old {
		k, _ := releaseTemplateListKey(field, v)
		before[k] = releaseMap(v)
	}
	for _, v := range next {
		k, _ := releaseTemplateListKey(field, v)
		after[k] = releaseMap(v)
	}
	for i, v := range live {
		k, _ := releaseTemplateListKey(field, v)
		liveIndex[k] = i
	}
	prior := -1
	for _, v := range old {
		k, _ := releaseTemplateListKey(field, v)
		i, ok := liveIndex[k]
		if !ok || (field == "env" || field == "initContainers") && i <= prior {
			return nil, nil, false, ErrSuperseded
		}
		prior = i
	}
	result, owned := []any{}, []any{}
	seen := map[string]bool{}
	managed := map[string]any{}
	for _, v := range live {
		m := releaseMap(v)
		key, _ := releaseTemplateListKey(field, v)
		seen[key] = true
		p, po := before[key]
		n, no := after[key]
		if po && !no {
			if !releaseTemplateMatchesAt(p, m, path+"/*", schema, true) {
				return nil, nil, false, ErrSuperseded
			}
			continue
		}
		if !no {
			result = append(result, m)
			continue
		}
		if !po {
			if !releaseTemplateMatchesAt(n, m, path+"/*", schema, true) {
				return nil, nil, false, ErrSuperseded
			}
			result = append(result, m)
			continue
		}
		value, mm, _, e := releaseMergeIntent(p, true, m, true, n, true, path+"/*", schema)
		if e != nil {
			return nil, nil, false, e
		}
		result = append(result, value)
		if len(releaseMap(mm)) > 0 {
			managed[key] = mm
		}
	}
	for _, v := range next {
		k, _ := releaseTemplateListKey(field, v)
		if !seen[k] {
			result = append(result, v)
			managed[k] = v
		}
		if managed[k] != nil {
			owned = append(owned, managed[k])
		}
	}
	if field == "env" || field == "initContainers" {
		var e error
		result, e = releaseOrderNativeMembers(field, old, live, next, result)
		if e != nil {
			return nil, nil, false, e
		}
	}
	return result, owned, true, nil
}

// releaseMergeDifference emits an RFC 7386 patch. Arrays already contain the
// complete preserved current members, so JSON merge cannot erase injections.
func releaseMergeDifference(current, target any) any {
	if releaseSameValue(current, target) {
		return nil
	}
	next, ok := target.(map[string]any)
	old, oldMap := current.(map[string]any)
	if !ok || !oldMap {
		return target
	}
	patch := map[string]any{}
	for key := range old {
		if _, exists := next[key]; !exists {
			patch[key] = nil
		}
	}
	for key, value := range next {
		prior, exists := old[key]
		if !exists {
			patch[key] = value
			continue
		}
		if !releaseSameValue(prior, value) {
			patch[key] = releaseMergeDifference(prior, value)
		}
	}
	return patch
}

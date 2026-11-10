package kubernetes

// Null is a deletion only in a sealed RFC7386 map patch. It is not the same as
// a missing field in a full declaration, nor in a replacement array element.
func releaseApprovedActionFieldsMatch(in ReleaseActionIntent, want, actual map[string]any) bool {
	if in.MutationVersion != 1 || in.Verb != "patch" || in.ContentType != releaseMergeContentType {
		return releaseApprovedFieldsMatch(in.Resource, want, actual)
	}
	projection, ok := releaseMergeReceiptProjection(want, actual)
	return ok && releaseApprovedFieldsMatch(in.Resource, projection, actual)
}
func releaseMergeReceiptProjection(patch, actual map[string]any) (map[string]any, bool) {
	out := map[string]any{}
	for key, value := range patch {
		if value == nil {
			if _, present := actual[key]; present {
				return nil, false
			}
			continue
		}
		if m, ok := value.(map[string]any); ok {
			child, valid := releaseMergeReceiptProjection(m, releaseMap(actual[key]))
			if !valid {
				return nil, false
			}
			out[key] = child
		} else {
			out[key] = value
		}
	}
	return out, true
}

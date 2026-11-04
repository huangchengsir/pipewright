package kubernetes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

var ErrPatchDenied = errors.New("kubernetes_patch_not_authorized")
var ErrAccessReview = errors.New("kubernetes_permission_check_unverified")

const accessReviewPath = "/apis/authorization.k8s.io/v1/selfsubjectaccessreviews"

func patchAccessAttributes(t Target) map[string]string {
	return map[string]string{"namespace": t.Namespace, "verb": "patch", "group": "apps", "version": "v1", "resource": "deployments", "name": t.Deployment}
}

// CheckPatchAccess asks only about the fixed target mutation. This non-persistent
// review is advisory: authorization can change before PATCH, so errors after the
// dispatch fence must still be treated as unknown by the controller.
func (w *WriterSession) CheckPatchAccess(ctx context.Context) error {
	s := w.s
	s.mu.Lock()
	defer s.mu.Unlock()
	s.patchAccessVerified = false
	if s.closed || s.patched {
		return ErrOwner
	}
	op, err := s.runtime.store.GetOperation(ctx, s.lease.OperationID)
	if err != nil {
		return err
	}
	if op.OwnerEpoch != s.lease.OwnerEpoch || op.OwnerFenced || op.Retired || op.DispatchFenced || op.Business != NotDispatched {
		return ErrOwner
	}
	if err = s.runtime.store.ValidateAuthorization(ctx, s.auth); err != nil {
		return err
	}
	if err = s.checkIdentity(ctx, false); err != nil {
		return err
	}
	request, err := json.Marshal(map[string]any{
		"apiVersion": "authorization.k8s.io/v1", "kind": "SelfSubjectAccessReview",
		"spec": map[string]any{"resourceAttributes": patchAccessAttributes(s.auth.Target)},
	})
	if err != nil {
		return ErrAccessReview
	}
	defer clear(request)
	// --raw returns the server JSON directly and cannot be combined with --output.
	// Neither the URL nor the request kind/verb/resource accepts user input.
	raw, err := s.execPrivate(ctx, s.kubectl("create", "--raw="+accessReviewPath, "--filename=-"), request)
	if err != nil {
		return ErrAccessReview
	}
	defer clear(raw)
	if err = decodePatchAccessReview(raw, s.auth.Target); err != nil {
		return err
	}
	s.patchAccessVerified = true
	return nil
}

func decodePatchAccessReview(raw []byte, target Target) error {
	if len(raw) == 0 || len(raw) > MaxDiagnosticBytes {
		return ErrAccessReview
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	remaining := 1024
	value, err := accessReviewValue(decoder, 0, &remaining)
	if err != nil {
		return ErrAccessReview
	}
	if _, err = decoder.Token(); err != io.EOF {
		return ErrAccessReview
	}
	doc, ok := value.(map[string]any)
	if !ok || !reviewKeys(doc, "apiVersion", "kind", "metadata", "spec", "status") || doc["apiVersion"] != "authorization.k8s.io/v1" || doc["kind"] != "SelfSubjectAccessReview" {
		return ErrAccessReview
	}
	if meta, present := doc["metadata"]; present {
		if _, ok := meta.(map[string]any); !ok {
			return ErrAccessReview
		}
	}
	spec, ok := doc["spec"].(map[string]any)
	if !ok || len(spec) != 1 {
		return ErrAccessReview
	}
	attrs, ok := spec["resourceAttributes"].(map[string]any)
	expected := patchAccessAttributes(target)
	if !ok || len(attrs) != len(expected) {
		return ErrAccessReview
	}
	for key, want := range expected {
		got, ok := attrs[key].(string)
		if !ok || got != want {
			return ErrAccessReview
		}
	}
	status, ok := doc["status"].(map[string]any)
	if !ok || !reviewKeys(status, "allowed", "denied", "reason", "evaluationError") {
		return ErrAccessReview
	}
	allowed, ok := status["allowed"].(bool)
	if !ok {
		return ErrAccessReview
	}
	denied := false
	if value, present := status["denied"]; present {
		denied, ok = value.(bool)
		if !ok {
			return ErrAccessReview
		}
	}
	for _, key := range []string{"reason", "evaluationError"} {
		if value, present := status[key]; present {
			text, ok := value.(string)
			if !ok || key == "evaluationError" && text != "" {
				return ErrAccessReview
			}
		}
	}
	if allowed && denied {
		return ErrAccessReview
	}
	if !allowed {
		return ErrPatchDenied
	}
	return nil
}

func reviewKeys(object map[string]any, keys ...string) bool {
	for key := range object {
		found := false
		for _, permitted := range keys {
			found = found || key == permitted
		}
		if !found {
			return false
		}
	}
	return true
}

// Reject duplicate (including case-alias) fields instead of allowing JSON's
// last-value-wins behavior to turn contradictory evidence into permission.
func accessReviewValue(d *json.Decoder, depth int, remaining *int) (any, error) {
	*remaining--
	if depth > 16 || *remaining < 0 {
		return nil, ErrAccessReview
	}
	token, err := d.Token()
	if err != nil {
		return nil, err
	}
	delim, compound := token.(json.Delim)
	if !compound {
		return token, nil
	}
	switch delim {
	case '{':
		object := make(map[string]any)
		seen := make(map[string]bool)
		for d.More() {
			token, err := d.Token()
			if err != nil {
				return nil, err
			}
			key, ok := token.(string)
			if !ok || seen[strings.ToLower(key)] {
				return nil, ErrAccessReview
			}
			seen[strings.ToLower(key)] = true
			value, err := accessReviewValue(d, depth+1, remaining)
			if err != nil {
				return nil, err
			}
			object[key] = value
		}
		end, err := d.Token()
		if err != nil || end != json.Delim('}') {
			return nil, ErrAccessReview
		}
		return object, nil
	case '[':
		array := make([]any, 0)
		for d.More() {
			value, err := accessReviewValue(d, depth+1, remaining)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		end, err := d.Token()
		if err != nil || end != json.Delim(']') {
			return nil, ErrAccessReview
		}
		return array, nil
	default:
		return nil, ErrAccessReview
	}
}

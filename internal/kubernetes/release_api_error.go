package kubernetes

import "errors"

var errReleaseAdmissionMismatch = errors.New("kubernetes_admission_mismatch")

// Only the HTTP status is retained. API Status messages/reasons and response
// bodies can contain arbitrary admission output and must never become errors.
type releaseAPIStatusError struct{ status int }

func (e *releaseAPIStatusError) Error() string { return e.reason() }
func (e *releaseAPIStatusError) Unwrap() error {
	switch e.status {
	case 403:
		return ErrDenied
	case 404:
		return ErrNotFound
	}
	return nil
}
func (e *releaseAPIStatusError) reason() string {
	switch {
	case e.status == 400:
		return "kubernetes_request_invalid"
	case e.status == 401:
		return "kubernetes_credential_rejected"
	case e.status == 403:
		return "kubernetes_not_authorized"
	case e.status == 404:
		return "kubernetes_not_found"
	case e.status == 409:
		return "kubernetes_resource_conflict"
	case e.status == 422:
		return "kubernetes_resource_invalid"
	case e.status == 429:
		return "kubernetes_api_throttled"
	case e.status >= 500 && e.status <= 599:
		return "kubernetes_api_unavailable"
	}
	return "dry_run_failed"
}

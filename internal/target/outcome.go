package target

import "errors"

// Classification is additive: errors.Is still matches existing sanitized errors.
var ErrNotStarted = errors.New("target: command not started")
var ErrOutcomeUnknown = errors.New("target: command outcome unknown")

func NotStarted(cause error) error     { return errors.Join(ErrNotStarted, cause) }
func OutcomeUnknown(cause error) error { return errors.Join(ErrOutcomeUnknown, cause) }
func IsNotStarted(err error) bool {
	if errors.Is(err, ErrOutcomeUnknown) {
		return false
	}
	return errors.Is(err, ErrNotStarted) || errors.Is(err, ErrAuth) || errors.Is(err, ErrInvalidCredential) || errors.Is(err, ErrCredentialNotFound) || errors.Is(err, ErrVaultUnconfigured) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrTargetChanged)
}

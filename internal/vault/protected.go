package vault

import (
	"errors"
	"strings"
)

var ErrProtectedCredential = errors.New("vault: protected credential requires Kubernetes authorization")

// ValidateGenericReference prevents protected cluster identities entering generic consumers.
func ValidateGenericReference(id string) error {
	if strings.HasPrefix(strings.TrimSpace(id), "kcred_") {
		return ErrProtectedCredential
	}
	return nil
}

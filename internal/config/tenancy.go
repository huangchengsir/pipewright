package config

import (
	"errors"
	"os"
)

// MultiTenantMode preserves the distinction between an unset variable and an
// explicit false. An instance enabled previously must never silently downgrade.
type MultiTenantMode struct {
	Enabled  bool
	Explicit bool
}

func ParseMultiTenantMode(value string, present bool) (MultiTenantMode, error) {
	if !present {
		return MultiTenantMode{}, nil
	}
	switch value {
	case "true":
		return MultiTenantMode{Enabled: true, Explicit: true}, nil
	case "false":
		return MultiTenantMode{Explicit: true}, nil
	default:
		return MultiTenantMode{}, errors.New("config: PIPEWRIGHT_MULTI_TENANT must be true or false")
	}
}

func LoadMultiTenantMode() (MultiTenantMode, error) {
	value, present := os.LookupEnv("PIPEWRIGHT_MULTI_TENANT")
	return ParseMultiTenantMode(value, present)
}

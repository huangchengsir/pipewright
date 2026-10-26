package mask

import "context"

type registrarContextKey struct{}

// WithSecretRegistrar binds local secret registration to the current operation.
// It neither reads a credential nor performs network I/O. The owning run keeps
// its existing Masker, including older token generations, until the run exits.
func WithSecretRegistrar(ctx context.Context, register func(string)) context.Context {
	if register == nil {
		return ctx
	}
	return context.WithValue(ctx, registrarContextKey{}, register)
}

// RegisterContextSecret must be called on the exact value before it can reach
// a command, log, or callback. A concurrent later rotation cannot change which
// secret the operation has already received.
func RegisterContextSecret(ctx context.Context, value string) {
	if register, ok := ctx.Value(registrarContextKey{}).(func(string)); ok {
		register(value)
	}
}

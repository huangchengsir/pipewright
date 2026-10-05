package notify

import "context"

type deliveryObserverKey struct{}

// WithDeliveryObserver observes an enabled delivery attempt, not a skipped SendVia call.
// It does not change delivery behavior or expose channel configuration.
func WithDeliveryObserver(ctx context.Context, observe func()) context.Context {
	return context.WithValue(ctx, deliveryObserverKey{}, observe)
}

func observeDelivery(ctx context.Context) {
	if observe, ok := ctx.Value(deliveryObserverKey{}).(func()); ok && observe != nil {
		observe()
	}
}

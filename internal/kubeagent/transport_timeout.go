package kubeagent

import (
	"net/url"
	"time"
)

// Client-go discovery serializes its default deadline as timeout=32s. Accept
// only one bounded standard Duration.String value. This transport parameter
// never changes path/resource authority or extends the existing HTTP/lease
// deadlines; it remains in the original request and permit digest.
func validateTransportTimeout(query url.Values) error {
	values, present := query["timeout"]
	if !present {
		return nil
	}
	if len(values) != 1 || len(values[0]) == 0 || len(values[0]) > 32 {
		return errDenied
	}
	timeout, e := time.ParseDuration(values[0])
	if e != nil || timeout <= 0 || timeout > time.Minute || timeout.String() != values[0] {
		return errDenied
	}
	return nil
}

package store

import "testing"

func TestDMOptionalDriverLOBAdapter(t *testing.T) {
	if dmConnectorFactory == nil {
		t.Skip("official DM driver not supplied to this build")
	}
	if dmDriverSelfTest == nil {
		t.Fatal("missing DM adapter verification")
	}
	if err := dmDriverSelfTest(); err != nil {
		t.Fatal(err)
	}
}

package mask

import "testing"

func TestRegistryCompositeAndIndependentPasswordRedaction(t *testing.T) {
	for _, password := range []string{"password:with:colons", "abc", "x"} {
		m := NewMasker()
		m.RegisterRegistrySecret("alice:" + password)
		if got := m.Scrub("alice:" + password + " " + password); got != Placeholder+" "+Placeholder {
			t.Fatal(got)
		}
		stream := m.NewStream()
		text := ""
		for _, c := range password {
			text += stream.Write(string(c))
		}
		text += stream.Finish()
		if text != Placeholder {
			t.Fatal(text)
		}
	}
}

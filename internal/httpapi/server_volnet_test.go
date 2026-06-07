package httpapi

import "testing"

func TestParseVolumes(t *testing.T) {
	out := "{\"Name\":\"new-api_pg_data\",\"Driver\":\"local\"}\n{\"Name\":\"cache\",\"Driver\":\"local\"}\n"
	got := parseVolumes(out)
	if len(got) != 2 || got[0].Name != "new-api_pg_data" || got[0].Driver != "local" {
		t.Fatalf("parseVolumes wrong: %+v", got)
	}
	if len(parseVolumes("garbage\n")) != 0 {
		t.Fatalf("garbage should yield empty")
	}
}

func TestParseNetworks(t *testing.T) {
	out := "{\"ID\":\"abc\",\"Name\":\"bridge\",\"Driver\":\"bridge\",\"Scope\":\"local\"}\n{\"ID\":\"def\",\"Name\":\"host\",\"Driver\":\"host\",\"Scope\":\"local\"}\n"
	got := parseNetworks(out)
	if len(got) != 2 || got[0].Name != "bridge" || got[1].Driver != "host" {
		t.Fatalf("parseNetworks wrong: %+v", got)
	}
}

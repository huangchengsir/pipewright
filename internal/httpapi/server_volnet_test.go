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

func TestParseNetworkContainers(t *testing.T) {
	out := `{"abc123def456789":{"Name":"redis","IPv4Address":"172.18.0.2/16"},"ffff0001":{"Name":"web","IPv4Address":"172.18.0.3/16"}}`
	got := parseNetworkContainers(out)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	byName := map[string]netContainerDTO{}
	for _, c := range got {
		byName[c.Name] = c
	}
	if byName["redis"].IPv4 != "172.18.0.2/16" || byName["redis"].ID != "abc123def456" {
		t.Fatalf("redis wrong: %+v", byName["redis"])
	}
	// null / 空 → 空切片。
	if len(parseNetworkContainers("null")) != 0 || len(parseNetworkContainers("")) != 0 {
		t.Fatalf("null/empty should be empty")
	}
}

func TestParseNetworks(t *testing.T) {
	out := "{\"ID\":\"abc\",\"Name\":\"bridge\",\"Driver\":\"bridge\",\"Scope\":\"local\"}\n{\"ID\":\"def\",\"Name\":\"host\",\"Driver\":\"host\",\"Scope\":\"local\"}\n"
	got := parseNetworks(out)
	if len(got) != 2 || got[0].Name != "bridge" || got[1].Driver != "host" {
		t.Fatalf("parseNetworks wrong: %+v", got)
	}
}

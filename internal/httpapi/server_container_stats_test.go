package httpapi

import "testing"

func TestParseContainerStats(t *testing.T) {
	out := `{"BlockIO":"0B / 0B","CPUPerc":"0.15%","Container":"abc","MemPerc":"0.63%","MemUsage":"12.5MiB / 1.94GiB","Name":"web","NetIO":"1.2kB / 0B","PIDs":"5"}
{"BlockIO":"8.19kB / 0B","CPUPerc":"1.20%","Container":"def","MemPerc":"2.10%","MemUsage":"40MiB / 1.94GiB","Name":"redis","NetIO":"0B / 0B","PIDs":"4"}
`
	got := parseContainerStats(out)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Name != "web" || got[0].CpuPerc != "0.15%" || got[0].MemUsage != "12.5MiB / 1.94GiB" {
		t.Fatalf("stat0 wrong: %+v", got[0])
	}
	if got[0].MemPerc != "0.63%" || got[0].NetIO != "1.2kB / 0B" || got[0].BlockIO != "0B / 0B" {
		t.Fatalf("stat0 fields wrong: %+v", got[0])
	}
	if got[1].Name != "redis" || got[1].CpuPerc != "1.20%" {
		t.Fatalf("stat1 wrong: %+v", got[1])
	}
}

func TestParseContainerStats_SkipsGarbage(t *testing.T) {
	got := parseContainerStats("garbage line\n{\"Name\":\"x\",\"CPUPerc\":\"0.00%\"}\n\n   \n")
	if len(got) != 1 || got[0].Name != "x" {
		t.Fatalf("want 1 valid stat, got %+v", got)
	}
}

func TestParseContainerStats_Empty(t *testing.T) {
	if got := parseContainerStats(""); len(got) != 0 {
		t.Fatalf("want empty, got %+v", got)
	}
}

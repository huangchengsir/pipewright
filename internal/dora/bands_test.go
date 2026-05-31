package dora

import (
	"testing"
	"time"
)

func sec(d time.Duration) float64 { return d.Seconds() }

func TestDeployFrequencyBand(t *testing.T) {
	tests := []struct {
		name    string
		perDay  float64
		success int
		want    string
	}{
		{"no samples", 0, 0, BandNone},
		{"elite daily", 2, 60, BandElite},
		{"elite exactly one per day", 1, 30, BandElite},
		{"high weekly", 1.0 / 5, 6, BandHigh},
		{"medium monthly", 1.0 / 20, 1, BandMedium},
		{"low below monthly", 1.0 / 90, 1, BandLow},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := deployFrequencyBand(tt.perDay, tt.success); got != tt.want {
				t.Fatalf("deployFrequencyBand(%v,%d) = %q, want %q", tt.perDay, tt.success, got, tt.want)
			}
		})
	}
}

func TestLeadTimeBand(t *testing.T) {
	tests := []struct {
		name    string
		seconds float64
		samples int
		want    string
	}{
		{"no samples", 0, 0, BandNone},
		{"elite under a day", sec(2 * time.Hour), 3, BandElite},
		{"high under a week", sec(3 * 24 * time.Hour), 3, BandHigh},
		{"medium under a month", sec(15 * 24 * time.Hour), 3, BandMedium},
		{"low over a month", sec(60 * 24 * time.Hour), 3, BandLow},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := leadTimeBand(tt.seconds, tt.samples); got != tt.want {
				t.Fatalf("leadTimeBand(%v,%d) = %q, want %q", tt.seconds, tt.samples, got, tt.want)
			}
		})
	}
}

func TestChangeFailureRateBand(t *testing.T) {
	tests := []struct {
		name  string
		rate  float64
		total int
		want  string
	}{
		{"no deployments", 0, 0, BandNone},
		{"elite low failure", 0.10, 10, BandElite},
		{"elite boundary 15pct", 0.15, 10, BandElite},
		{"high", 0.25, 10, BandHigh},
		{"medium", 0.40, 10, BandMedium},
		{"low high failure", 0.80, 10, BandLow},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := changeFailureRateBand(tt.rate, tt.total); got != tt.want {
				t.Fatalf("changeFailureRateBand(%v,%d) = %q, want %q", tt.rate, tt.total, got, tt.want)
			}
		})
	}
}

func TestMTTRBand(t *testing.T) {
	tests := []struct {
		name    string
		seconds float64
		samples int
		want    string
	}{
		{"no pairs", 0, 0, BandNone},
		{"elite under an hour", sec(30 * time.Minute), 2, BandElite},
		{"high under a day", sec(6 * time.Hour), 2, BandHigh},
		{"medium under a week", sec(3 * 24 * time.Hour), 2, BandMedium},
		{"low over a week", sec(10 * 24 * time.Hour), 2, BandLow},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mttrBand(tt.seconds, tt.samples); got != tt.want {
				t.Fatalf("mttrBand(%v,%d) = %q, want %q", tt.seconds, tt.samples, got, tt.want)
			}
		})
	}
}

package dosebudget

import (
	"errors"
	"testing"
	"time"
)

func TestPeriodUsesHalfOpenBoundary(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	period, err := NewPeriod(start, end)
	if err != nil {
		t.Fatalf("NewPeriod returned error: %v", err)
	}
	tests := []struct {
		name string
		at   time.Time
		want bool
	}{
		{"start included", start, true},
		{"inside included", start.Add(time.Hour), true},
		{"last nanosecond included", end.Add(-time.Nanosecond), true},
		{"end excluded", end, false},
		{"before excluded", start.Add(-time.Nanosecond), false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := period.Contains(test.at); got != test.want {
				t.Fatalf("Contains(%s) = %v, want %v", test.at, got, test.want)
			}
		})
	}
}

func TestNewPeriodRejectsInvalidRanges(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, end := range []time.Time{start, start.Add(-time.Second), start.Add(371 * 24 * time.Hour)} {
		if _, err := NewPeriod(start, end); err == nil {
			t.Fatalf("NewPeriod(%s, %s) unexpectedly succeeded", start, end)
		}
	}
}

func TestValidateComparisonWindow(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	october := time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		asOf    time.Time
		now     time.Time
		wantErr bool
	}{
		{"valid", time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), october, false},
		{"equals start is invalid", start, october, true},
		{"before start is invalid", start.Add(-time.Second), october, true},
		{"later than now is invalid", october.Add(time.Second), october, true},
		{"equals now is valid", time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), false},
		{"370 days is valid", start.Add(370 * 24 * time.Hour), start.Add(400 * 24 * time.Hour), false},
		{"over 370 days is invalid", start.Add(370*24*time.Hour + time.Second), start.Add(400 * 24 * time.Hour), true},
		{"zero is invalid", time.Time{}, october, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateComparisonWindow(start, test.asOf, test.now)
			if test.wantErr && err == nil {
				t.Fatalf("ValidateComparisonWindow(%s) unexpectedly succeeded", test.asOf)
			}
			if !test.wantErr && err != nil {
				t.Fatalf("ValidateComparisonWindow(%s) returned error: %v", test.asOf, err)
			}
			if test.wantErr && !errors.Is(err, ErrInvalidPeriod) {
				t.Fatalf("error = %v, want ErrInvalidPeriod", err)
			}
		})
	}
}

func TestResolveCutOffs(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC)
	early := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	mid := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	late := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	batchDefault := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name         string
		planIDs      []uint
		explicit     map[uint]time.Time
		batchDefault *time.Time
		wantOrigins  []AsOfOrigin
		wantAsOf     []time.Time
		wantAnchor   time.Time
		wantMismatch bool
		wantErr      error
	}{
		{
			name:        "missing cut-off aligns to earliest explicit",
			planIDs:     []uint{1, 2, 3},
			explicit:    map[uint]time.Time{1: mid, 3: late},
			wantOrigins: []AsOfOrigin{AsOfOriginScenario, AsOfOriginBatchAnchor, AsOfOriginScenario},
			wantAsOf:    []time.Time{mid, mid, late},
			wantAnchor:  mid, wantMismatch: true,
		},
		{
			name:        "all explicit identical cut-offs match",
			planIDs:     []uint{1, 2},
			explicit:    map[uint]time.Time{1: mid, 2: mid},
			wantOrigins: []AsOfOrigin{AsOfOriginScenario, AsOfOriginScenario},
			wantAsOf:    []time.Time{mid, mid}, wantAnchor: mid,
		},
		{
			name:         "all missing use batch default",
			planIDs:      []uint{1, 2},
			batchDefault: &batchDefault,
			wantOrigins:  []AsOfOrigin{AsOfOriginBatchDefault, AsOfOriginBatchDefault},
			wantAsOf:     []time.Time{batchDefault, batchDefault}, wantAnchor: batchDefault,
		},
		{
			name:    "all missing without default is rejected",
			planIDs: []uint{1, 2},
			wantErr: ErrMissingAsOf,
		},
		{
			name:     "future explicit cut-off is rejected",
			planIDs:  []uint{1, 2},
			explicit: map[uint]time.Time{1: now.Add(time.Hour), 2: mid},
			wantErr:  ErrInvalidPeriod,
		},
		{
			name:        "anchor is earliest even when missing comes first",
			planIDs:     []uint{1, 2, 3},
			explicit:    map[uint]time.Time{2: late, 3: early},
			wantOrigins: []AsOfOrigin{AsOfOriginBatchAnchor, AsOfOriginScenario, AsOfOriginScenario},
			wantAsOf:    []time.Time{early, late, early}, wantAnchor: early, wantMismatch: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resolved, anchor, mismatch, err := ResolveCutOffs(test.planIDs, test.explicit, test.batchDefault, start, now)
			if test.wantErr != nil {
				if !errors.Is(err, test.wantErr) {
					t.Fatalf("error = %v, want %v", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveCutOffs returned error: %v", err)
			}
			if mismatch != test.wantMismatch {
				t.Errorf("mismatch = %v, want %v", mismatch, test.wantMismatch)
			}
			if !anchor.Equal(test.wantAnchor) {
				t.Errorf("anchor = %s, want %s", anchor, test.wantAnchor)
			}
			for i, wantOrigin := range test.wantOrigins {
				if resolved[i].Origin != wantOrigin {
					t.Errorf("scenario %d origin = %q, want %q", i, resolved[i].Origin, wantOrigin)
				}
			}
			for i, wantAsOf := range test.wantAsOf {
				if !resolved[i].AsOf.Equal(wantAsOf) {
					t.Errorf("scenario %d as_of = %s, want %s", i, resolved[i].AsOf, wantAsOf)
				}
			}
		})
	}
}

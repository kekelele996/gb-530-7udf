package dosebudget

import (
	"testing"
	"time"
)

func ptrTime(value time.Time) *time.Time { return &value }

func TestResolveAsOf(t *testing.T) {
	requestEnd := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	early := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	later := time.Date(2026, 6, 1, 8, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		explicit    []*time.Time
		requestEnd  time.Time
		wantCutOffs []time.Time
		wantSources []string
		wantErr     bool
	}{
		{
			name:        "no explicit as_of falls back to request period end",
			explicit:    []*time.Time{nil, nil},
			requestEnd:  requestEnd,
			wantCutOffs: []time.Time{requestEnd, requestEnd},
			wantSources: []string{AsOfSourceRequestPeriodEnd, AsOfSourceRequestPeriodEnd},
		},
		{
			name:        "missing as_of aligns to earliest explicit cut-off",
			explicit:    []*time.Time{ptrTime(later), nil, ptrTime(early)},
			requestEnd:  requestEnd,
			wantCutOffs: []time.Time{later, early, early},
			wantSources: []string{AsOfSourceExplicit, AsOfSourceAlignedEarliest, AsOfSourceExplicit},
		},
		{
			name:        "all explicit keep their own cut-offs",
			explicit:    []*time.Time{ptrTime(early), ptrTime(later)},
			requestEnd:  requestEnd,
			wantCutOffs: []time.Time{early, later},
			wantSources: []string{AsOfSourceExplicit, AsOfSourceExplicit},
		},
		{
			name:       "missing both explicit cut-offs and request end rejected",
			explicit:   []*time.Time{nil, nil},
			requestEnd: time.Time{},
			wantErr:    true,
		},
		{
			name:       "zero explicit timestamp rejected",
			explicit:   []*time.Time{ptrTime(time.Time{})},
			requestEnd: requestEnd,
			wantErr:    true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cutOffs, sources, err := ResolveAsOf(test.explicit, test.requestEnd)
			if (err != nil) != test.wantErr {
				t.Fatalf("ResolveAsOf error = %v, wantErr %v", err, test.wantErr)
			}
			if err != nil {
				return
			}
			if len(cutOffs) != len(test.wantCutOffs) {
				t.Fatalf("cutOffs length = %d, want %d", len(cutOffs), len(test.wantCutOffs))
			}
			for index := range cutOffs {
				if !cutOffs[index].Equal(test.wantCutOffs[index]) {
					t.Fatalf("cutOff[%d] = %s, want %s", index, cutOffs[index], test.wantCutOffs[index])
				}
				if sources[index] != test.wantSources[index] {
					t.Fatalf("source[%d] = %s, want %s", index, sources[index], test.wantSources[index])
				}
			}
		})
	}
}

func TestValidateAssessmentWindow(t *testing.T) {
	periodStart := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		start   time.Time
		asOf    time.Time
		now     time.Time
		wantErr bool
	}{
		{"valid window", periodStart, time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), now, false},
		{"cut-off within five minute skew accepted", periodStart, now.Add(2 * time.Minute), now, false},
		{"cut-off in the future rejected", periodStart, now.Add(time.Hour), now, true},
		{"window over 370 days rejected", periodStart, periodStart.Add(371 * 24 * time.Hour), now.AddDate(2, 0, 0), true},
		{"cut-off before period start rejected", periodStart, periodStart.Add(-time.Hour), now, true},
		{"zero cut-off rejected", periodStart, time.Time{}, now, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateAssessmentWindow(test.start, test.asOf, test.now)
			if (err != nil) != test.wantErr {
				t.Fatalf("ValidateAssessmentWindow error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}

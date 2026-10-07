package dosebudget

import (
	"errors"
	"fmt"
	"time"
)

var (
	ErrInvalidPeriod     = errors.New("invalid assessment period")
	ErrInvalidDoseInput  = errors.New("invalid dose input")
	ErrCorrectionChain   = errors.New("invalid correction chain")
	ErrDuplicateSource   = errors.New("duplicate source reference")
	ErrThresholdOrdering = errors.New("administrative limit must not exceed legal limit")
)

type Period struct {
	Start time.Time
	End   time.Time
}

func NewPeriod(start, end time.Time) (Period, error) {
	start = start.UTC()
	end = end.UTC()
	if start.IsZero() || end.IsZero() || !end.After(start) {
		return Period{}, fmt.Errorf("%w: end must be after start", ErrInvalidPeriod)
	}
	if end.Sub(start) > 370*24*time.Hour {
		return Period{}, fmt.Errorf("%w: range cannot exceed 370 days", ErrInvalidPeriod)
	}
	return Period{Start: start, End: end}, nil
}

func (period Period) Contains(value time.Time) bool {
	value = value.UTC()
	return !value.Before(period.Start) && value.Before(period.End)
}

func (period Period) ElapsedFraction(value time.Time) float64 {
	if !value.After(period.Start) {
		return 0
	}
	if !value.Before(period.End) {
		return 1
	}
	return value.Sub(period.Start).Seconds() / period.End.Sub(period.Start).Seconds()
}

// As-of cut-off provenance returned to planners.
const (
	AsOfSourceExplicit         = "explicit"
	AsOfSourceAlignedEarliest  = "aligned_earliest"
	AsOfSourceRequestPeriodEnd = "request_period_end"
)

// futureCutOffSkew absorbs clock skew between the planner's browser and the
// server while still rejecting evaluation windows that genuinely end in the future.
const futureCutOffSkew = 5 * time.Minute

// ResolveAsOf assigns one evidence cut-off per scenario in a comparison batch.
//
// Scenarios that carry their own as_of keep it. Scenarios without one are
// aligned to the earliest explicit as_of already present in the batch; only
// when no scenario carries an explicit cut-off do they fall back to
// requestPeriodEnd. Aligning to the earliest batch cut-off (instead of "now")
// prevents records verified later from leaking into the comparison and keeps
// every scenario reproducible over the same evidence set. requestPeriodEnd is
// ignored when at least one explicit as_of exists.
func ResolveAsOf(explicit []*time.Time, requestPeriodEnd time.Time) ([]time.Time, []string, error) {
	cutOffs := make([]time.Time, len(explicit))
	sources := make([]string, len(explicit))
	earliest := time.Time{}
	for _, asOf := range explicit {
		if asOf == nil {
			continue
		}
		utc := asOf.UTC()
		if utc.IsZero() {
			return nil, nil, fmt.Errorf("%w: as_of must carry a timestamp", ErrInvalidPeriod)
		}
		if earliest.IsZero() || utc.Before(earliest) {
			earliest = utc
		}
	}
	fallback := requestPeriodEnd.UTC()
	if !earliest.IsZero() {
		fallback = earliest
	}
	if fallback.IsZero() {
		return nil, nil, fmt.Errorf("%w: comparison requires period_end or at least one scenario as_of", ErrInvalidPeriod)
	}
	for index, asOf := range explicit {
		if asOf != nil {
			cutOffs[index] = asOf.UTC()
			sources[index] = AsOfSourceExplicit
			continue
		}
		cutOffs[index] = fallback
		if !earliest.IsZero() {
			sources[index] = AsOfSourceAlignedEarliest
		} else {
			sources[index] = AsOfSourceRequestPeriodEnd
		}
	}
	return cutOffs, sources, nil
}

// ValidateAssessmentWindow enforces the assessment window rules: the start is
// always the worker's configured period start, the cut-off must not end in the
// future, and the [start, end) window must not exceed 370 days.
func ValidateAssessmentWindow(periodStart, asOf, now time.Time) error {
	periodStart = periodStart.UTC()
	asOf = asOf.UTC()
	if asOf.IsZero() {
		return fmt.Errorf("%w: evaluation cut-off is required", ErrInvalidPeriod)
	}
	if asOf.After(now.UTC().Add(futureCutOffSkew)) {
		return fmt.Errorf("%w: evaluation cut-off cannot be in the future", ErrInvalidPeriod)
	}
	if _, err := NewPeriod(periodStart, asOf); err != nil {
		return err
	}
	return nil
}

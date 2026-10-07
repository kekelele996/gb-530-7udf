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
	// ErrMissingAsOf indicates a comparison request carried neither
	// per-scenario cut-offs nor a request-level default.
	ErrMissingAsOf = errors.New("each scenario must carry as_of, or the request must carry a default cut-off")
)

// AsOfOrigin identifies where a compared scenario's evaluation cut-off came
// from.
type AsOfOrigin string

const (
	// AsOfOriginScenario means the caller supplied the cut-off on the scenario.
	AsOfOriginScenario AsOfOrigin = "scenario"
	// AsOfOriginBatchDefault means the scenario inherited the request-level
	// default cut-off (default_as_of, or period_end for legacy callers).
	AsOfOriginBatchDefault AsOfOrigin = "batch_default"
	// AsOfOriginBatchAnchor means the scenario had no cut-off and was aligned
	// to the earliest explicit cut-off in the batch.
	AsOfOriginBatchAnchor AsOfOrigin = "batch_anchor"
)

const maxComparisonWindow = 370 * 24 * time.Hour

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

// ScenarioCutOff records the effective evaluation cut-off for one compared plan.
type ScenarioCutOff struct {
	PlanID uint
	AsOf   time.Time
	Origin AsOfOrigin
}

// ResolveCutOffs normalizes per-scenario evaluation cut-offs.
//
// Scenarios that carry their own as_of keep it. Scenarios without one are
// aligned to the earliest explicit as_of already present in the batch (when at
// least one scenario carries one); only when no scenario carries an as_of does
// the batch-level cut-off apply to every scenario. Aligning to the earliest
// in-batch cut-off, rather than evaluating missing scenarios at "now", keeps
// every column in one comparison on the same evidence basis: records verified
// after that earlier point are excluded for all scenarios, so a record
// verified later cannot leak into a side-by-side comparison.
func ResolveCutOffs(
	planIDs []uint,
	explicit map[uint]time.Time,
	batchDefault *time.Time,
	periodStart, now time.Time,
) ([]ScenarioCutOff, time.Time, bool, error) {
	periodStart = periodStart.UTC()
	now = now.UTC()
	earliestExplicit := time.Time{}
	hasExplicit := false
	for planID, value := range explicit {
		asOf := value.UTC()
		if err := ValidateComparisonWindow(periodStart, asOf, now); err != nil {
			return nil, time.Time{}, false, fmt.Errorf("plan %d: %w", planID, err)
		}
		if !hasExplicit || asOf.Before(earliestExplicit) {
			earliestExplicit = asOf
		}
		hasExplicit = true
	}
	resolved := make([]ScenarioCutOff, 0, len(planIDs))
	for _, planID := range planIDs {
		origin := AsOfOriginScenario
		asOf, isExplicit := explicit[planID]
		if !isExplicit {
			switch {
			case hasExplicit:
				asOf = earliestExplicit
				origin = AsOfOriginBatchAnchor
			case batchDefault != nil:
				asOf = batchDefault.UTC()
				origin = AsOfOriginBatchDefault
			default:
				return nil, time.Time{}, false, ErrMissingAsOf
			}
			if err := ValidateComparisonWindow(periodStart, asOf, now); err != nil {
				return nil, time.Time{}, false, fmt.Errorf("plan %d: %w", planID, err)
			}
		}
		resolved = append(resolved, ScenarioCutOff{PlanID: planID, AsOf: asOf.UTC(), Origin: origin})
	}
	anchor := resolved[0].AsOf
	mismatch := false
	for _, item := range resolved[1:] {
		if !item.AsOf.Equal(anchor) {
			mismatch = true
			if item.AsOf.Before(anchor) {
				anchor = item.AsOf
			}
		}
	}
	return resolved, anchor, mismatch, nil
}

// ValidateComparisonWindow enforces that an evaluation cut-off lies after the
// worker period start, is not later than now, and keeps the window within the
// 370-day maximum.
func ValidateComparisonWindow(periodStart, asOf, now time.Time) error {
	if asOf.IsZero() {
		return fmt.Errorf("%w: scenario as_of must not be empty", ErrInvalidPeriod)
	}
	if !asOf.After(periodStart) {
		return fmt.Errorf("%w: scenario as_of %s must be after the worker period start %s",
			ErrInvalidPeriod, asOf.Format(time.RFC3339), periodStart.Format(time.RFC3339))
	}
	if asOf.After(now) {
		return fmt.Errorf("%w: scenario as_of %s cannot be later than now %s",
			ErrInvalidPeriod, asOf.Format(time.RFC3339), now.Format(time.RFC3339))
	}
	if asOf.Sub(periodStart) > maxComparisonWindow {
		return fmt.Errorf("%w: scenario evaluation window cannot exceed 370 days from the worker period start",
			ErrInvalidPeriod)
	}
	return nil
}

// AsOfMismatchNotice explains mixed cut-offs to planning reviewers.
func AsOfMismatchNotice() string {
	return "Scenarios use different evaluation cut-offs; each column is recalculated over its own [period_start, as_of) window and missing cut-offs were aligned to the earliest cut-off in this batch. Compare the period column before acting."
}

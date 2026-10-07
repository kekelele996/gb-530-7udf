package dto

import "time"

type CreateDoseBudgetAssessmentRequest struct {
	PlanID    uint      `json:"plan_id" validate:"required,gt=0"`
	PeriodEnd time.Time `json:"period_end" validate:"required"`
	Version   uint      `json:"version" validate:"required,gt=0"`
}

// CompareScenarioInput carries one plan candidate together with its own optional
// assessment cut-off (as-of time). An omitted as_of is aligned by the service so
// every scenario in a batch is compared on one reproducible evidence cut-off.
type CompareScenarioInput struct {
	PlanID uint       `json:"plan_id" validate:"required,gt=0"`
	AsOf   *time.Time `json:"as_of"`
}

type CompareDoseBudgetRequest struct {
	// PlanIDs keeps the single shared period_end request shape working.
	PlanIDs []uint `json:"plan_ids" validate:"max=8,dive,gt=0"`
	// Scenarios allows each candidate plan to carry its own as_of cut-off.
	Scenarios []CompareScenarioInput `json:"scenarios" validate:"max=8,dive"`
	// PeriodEnd is the request-level cut-off used only when no scenario carries as_of.
	PeriodEnd time.Time `json:"period_end"`
}

// NormalizedScenarios returns per-plan as_of pointers, preferring the explicit
// scenarios list and falling back to the shared plan_ids/period_end shape.
func (request CompareDoseBudgetRequest) NormalizedScenarios() []CompareScenarioInput {
	if len(request.Scenarios) > 0 {
		return request.Scenarios
	}
	scenarios := make([]CompareScenarioInput, 0, len(request.PlanIDs))
	for _, planID := range request.PlanIDs {
		scenarios = append(scenarios, CompareScenarioInput{PlanID: planID})
	}
	return scenarios
}

type AssessmentReviewRequest struct {
	Decision string `json:"decision" validate:"required,oneof=accept reject"`
	Note     string `json:"note" validate:"required,min=3,max=1000"`
	Version  uint   `json:"version" validate:"required,gt=0"`
}

type DoseEvidence struct {
	PeriodStart          time.Time `json:"period_start"`
	PeriodEnd            time.Time `json:"period_end"`
	VerifiedEntryCount   int       `json:"verified_entry_count"`
	ExcludedEntryCount   int       `json:"excluded_entry_count"`
	CorrectedChainCount  int       `json:"corrected_chain_count"`
	Formula              string    `json:"formula"`
	ProjectionFormula    string    `json:"projection_formula"`
	AdministrativeLimit  float64   `json:"administrative_limit_msv"`
	AnnualLegalLimit     float64   `json:"annual_legal_limit_msv"`
	NearLegalRatio       float64   `json:"near_legal_ratio"`
	ThresholdVersion     string    `json:"threshold_version"`
	RequiresManualReview bool      `json:"requires_manual_review"`
	EscalationReason     string    `json:"escalation_reason"`
	BoundaryStatement    string    `json:"boundary_statement"`
}

type DoseBudgetAssessmentResponse struct {
	ID                uint                   `json:"id"`
	WorkerID          uint                   `json:"worker_id"`
	WorkerCode        string                 `json:"worker_code"`
	WorkerName        string                 `json:"worker_name"`
	PlanID            uint                   `json:"plan_id"`
	PlanCode          string                 `json:"plan_code"`
	AssessmentStatus  string                 `json:"assessment_status"`
	InputSnapshot     map[string]interface{} `json:"input_snapshot"`
	PeriodDoseMSV     float64                `json:"period_dose_msv"`
	ProjectedDoseMSV  float64                `json:"projected_dose_msv"`
	RemainingAdminMSV float64                `json:"remaining_admin_msv"`
	RemainingLegalMSV float64                `json:"remaining_legal_msv"`
	RiskBand          string                 `json:"risk_band"`
	Evidence          DoseEvidence           `json:"evidence"`
	ThresholdVersion  string                 `json:"threshold_version"`
	PlanVersion       uint                   `json:"plan_version"`
	WorkerVersion     uint                   `json:"worker_version"`
	CreatedAt         time.Time              `json:"created_at"`
	ReviewedBy        *uint                  `json:"reviewed_by,omitempty"`
	ReviewedAt        *time.Time             `json:"reviewed_at,omitempty"`
	ReviewNote        string                 `json:"review_note"`
}

// ScenarioResult is one row of a comparison: the assessment-shaped result plus
// the evidence window and as-of provenance so planners can see how far each
// candidate was calculated even when cut-offs differ within one batch.
type ScenarioResult struct {
	DoseBudgetAssessmentResponse
	PeriodStart time.Time `json:"period_start"`
	PeriodEnd   time.Time `json:"period_end"`
	AsOf        time.Time `json:"as_of"`
	// AsOfSource is "explicit" when the request supplied as_of and "aligned_earliest"
	// or "request_period_end" when the service filled the cut-off in.
	AsOfSource string `json:"as_of_source"`
}

type ScenarioComparisonResponse struct {
	WorkerID uint `json:"worker_id"`
	// PeriodDoseMSV mirrors the first scenario window and is retained for backwards
	// compatibility; per-scenario windows are authoritative when PeriodEndsAligned is false.
	PeriodDoseMSV     float64          `json:"period_dose_msv"`
	Scenarios         []ScenarioResult `json:"scenarios"`
	HighestRiskBand   string           `json:"highest_risk_band"`
	BoundaryStatement string           `json:"boundary_statement"`
	// PeriodEndsAligned reports whether every scenario used the same [start,end) window.
	PeriodEndsAligned bool `json:"period_ends_aligned"`
	// AsOfNotice is non-empty when the batch mixes explicit and aligned cut-offs.
	AsOfNotice string `json:"as_of_notice,omitempty"`
}

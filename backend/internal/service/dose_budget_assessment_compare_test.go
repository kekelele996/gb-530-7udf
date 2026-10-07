package service

import (
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"radiation-dose-budget-control/backend/internal/constants"
	"radiation-dose-budget-control/backend/internal/dosebudget"
	"radiation-dose-budget-control/backend/internal/dto"
	"radiation-dose-budget-control/backend/internal/model"
	"radiation-dose-budget-control/backend/internal/repository"
)

func TestCompareRecalculatesEachScenarioAtOwnAsOf(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:compare-asof-530?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	if err := db.AutoMigrate(&model.WorkerProfile{}, &model.WorkPermitPlan{}, &model.ExposureEntry{},
		&model.DoseBudgetAssessment{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	periodStart := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	worker := model.WorkerProfile{
		WorkerCode: "W-ASOF-530", DisplayName: "As Of Worker", AuthorizationLevel: "radiation-worker",
		AnnualLimitMSV: 20, AdministrativeLimitMSV: 12, ProfileStatus: constants.ProfileStatusActive,
		PeriodStart: periodStart, Version: 1, CreatedAt: periodStart, UpdatedAt: periodStart,
	}
	if err := db.Create(&worker).Error; err != nil {
		t.Fatalf("create worker: %v", err)
	}
	planOne := model.WorkPermitPlan{
		PlanCode: "P-ASOF-1", WorkerID: worker.ID, WorkArea: "bay A", TaskCategory: "survey",
		EstimatedRateMSVH: 0.6, PlannedMinutes: 60, ControlsJSON: `["distance"]`,
		PermitStatus: constants.PermitStatusDraft, Version: 1, CreatedAt: periodStart, UpdatedAt: periodStart,
	}
	planTwo := model.WorkPermitPlan{
		PlanCode: "P-ASOF-2", WorkerID: worker.ID, WorkArea: "bay B", TaskCategory: "inspection",
		EstimatedRateMSVH: 1.2, PlannedMinutes: 60, ControlsJSON: `["shielding"]`,
		PermitStatus: constants.PermitStatusDraft, Version: 1, CreatedAt: periodStart, UpdatedAt: periodStart,
	}
	if err := db.Create(&planOne).Error; err != nil {
		t.Fatalf("create plan one: %v", err)
	}
	if err := db.Create(&planTwo).Error; err != nil {
		t.Fatalf("create plan two: %v", err)
	}

	march := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	earlyVerifiedAt := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)
	lateVerifiedAt := time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)
	earlyEntry := model.ExposureEntry{
		WorkerID: worker.ID, SourceRef: "SRC-ASOF-EARLY", OccurredAt: march, DoseMSV: 1,
		EntryType: constants.EntryTypeConfirmed, QualityFlag: constants.QualityFlagVerified,
		VerifiedBy: ptrUInt(1), VerifiedAt: &earlyVerifiedAt, CreatedBy: 1, CreatedAt: earlyVerifiedAt,
	}
	lateEntry := model.ExposureEntry{
		WorkerID: worker.ID, SourceRef: "SRC-ASOF-LATE", OccurredAt: may(), DoseMSV: 3,
		EntryType: constants.EntryTypeConfirmed, QualityFlag: constants.QualityFlagVerified,
		VerifiedBy: ptrUInt(1), VerifiedAt: &lateVerifiedAt, CreatedBy: 1, CreatedAt: lateVerifiedAt,
	}
	if err := db.Create(&earlyEntry).Error; err != nil {
		t.Fatalf("create early entry: %v", err)
	}
	if err := db.Create(&lateEntry).Error; err != nil {
		t.Fatalf("create late entry: %v", err)
	}

	service := NewDoseBudgetAssessmentService(
		db,
		repository.NewDoseBudgetAssessmentRepository(db),
		repository.NewWorkPermitPlanRepository(db),
		repository.NewWorkerProfileRepository(db),
		repository.NewExposureEntryRepository(db),
		NewAuditService(repository.NewSystemRepository(db)),
		0.9, "ALARA-2026.1",
	)
	june := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	august := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	request := dto.CompareDoseBudgetRequest{
		Scenarios: []dto.CompareScenarioInput{
			{PlanID: planOne.ID, AsOf: &june},
			{PlanID: planTwo.ID, AsOf: nil},
		},
	}
	result, err := service.Compare(request)
	if err != nil {
		t.Fatalf("compare: %v", err)
	}
	if len(result.Scenarios) != 2 {
		t.Fatalf("expected 2 scenarios, got %d", len(result.Scenarios))
	}
	// Second scenario carries no as_of and must align to the earliest explicit
	// cut-off (June), so the July-verified record cannot enter the comparison.
	for index, scenario := range result.Scenarios {
		if scenario.PeriodDoseMSV != 1 {
			t.Fatalf("scenario %d period dose = %.3f, want 1 (July verification excluded at June cut-off)",
				index, scenario.PeriodDoseMSV)
		}
		if index == 0 && scenario.AsOfSource != string(dosebudget.AsOfOriginScenario) {
			t.Fatalf("scenario 0 source = %q, want %q", scenario.AsOfSource, dosebudget.AsOfOriginScenario)
		}
		if index == 1 && scenario.AsOfSource != string(dosebudget.AsOfOriginBatchAnchor) {
			t.Fatalf("scenario 1 source = %q, want %q", scenario.AsOfSource, dosebudget.AsOfOriginBatchAnchor)
		}
	}
	if result.AsOfMismatch {
		t.Fatalf("aligned cut-offs must not be flagged as mismatch")
	}
	if result.AsOfNotice != "" {
		t.Fatalf("expected no mismatch notice, got %q", result.AsOfNotice)
	}
	if result.PeriodDoseMSV != 1 {
		t.Fatalf("batch period dose = %.3f, want 1", result.PeriodDoseMSV)
	}

	// Distinct cut-offs: each scenario is recalculated over its own window, the
	// mismatch notice is raised, and later-verified evidence only enters the
	// scenario whose cut-off is after the verification time.
	mixedRequest := dto.CompareDoseBudgetRequest{
		Scenarios: []dto.CompareScenarioInput{
			{PlanID: planOne.ID, AsOf: &june},
			{PlanID: planTwo.ID, AsOf: &august},
		},
	}
	mixed, err := service.Compare(mixedRequest)
	if err != nil {
		t.Fatalf("compare mixed cut-offs: %v", err)
	}
	if !mixed.AsOfMismatch || mixed.AsOfNotice == "" {
		t.Fatalf("expected mismatch flag and notice for distinct cut-offs")
	}
	if !mixed.AsOfAnchor.Equal(june) {
		t.Fatalf("anchor = %s, want %s", mixed.AsOfAnchor, june)
	}
	first := mixed.Scenarios[0]
	second := mixed.Scenarios[1]
	if first.PeriodDoseMSV != 1 || first.ProjectedDoseMSV != 1.6 {
		t.Fatalf("june scenario = period %.3f projected %.3f, want 1 / 1.6", first.PeriodDoseMSV, first.ProjectedDoseMSV)
	}
	if second.PeriodDoseMSV != 4 || second.ProjectedDoseMSV != 5.2 {
		t.Fatalf("august scenario = period %.3f projected %.3f, want 4 / 5.2", second.PeriodDoseMSV, second.ProjectedDoseMSV)
	}
	if first.Evidence.PeriodEnd.Equal(second.Evidence.PeriodEnd) {
		t.Fatalf("scenario evidence must carry distinct period ends")
	}

	// With both scenarios cut off in August the later record is visible to both.
	lateRequest := dto.CompareDoseBudgetRequest{
		Scenarios: []dto.CompareScenarioInput{
			{PlanID: planOne.ID, AsOf: &august},
			{PlanID: planTwo.ID, AsOf: &august},
		},
	}
	lateResult, err := service.Compare(lateRequest)
	if err != nil {
		t.Fatalf("compare august: %v", err)
	}
	for index, scenario := range lateResult.Scenarios {
		if scenario.PeriodDoseMSV != 4 {
			t.Fatalf("august scenario %d period dose = %.3f, want 4", index, scenario.PeriodDoseMSV)
		}
	}
	if lateResult.AsOfMismatch {
		t.Fatalf("expected matching cut-offs, got mismatch")
	}
	if lateResult.AsOfNotice != "" {
		t.Fatalf("expected no mismatch notice, got %q", lateResult.AsOfNotice)
	}

	// A cut-off later than now must be rejected.
	tomorrow := time.Now().UTC().Add(24 * time.Hour)
	_, err = service.Compare(dto.CompareDoseBudgetRequest{
		Scenarios: []dto.CompareScenarioInput{
			{PlanID: planOne.ID, AsOf: &june},
			{PlanID: planTwo.ID, AsOf: &tomorrow},
		},
	})
	if err == nil {
		t.Fatalf("expected future cut-off to be rejected")
	}
}

func may() time.Time { return time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC) }

func ptrUInt(value uint) *uint { return &value }

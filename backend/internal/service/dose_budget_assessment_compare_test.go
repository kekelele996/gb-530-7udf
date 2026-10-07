package service

import (
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"radiation-dose-budget-control/backend/internal/constants"
	"radiation-dose-budget-control/backend/internal/dto"
	"radiation-dose-budget-control/backend/internal/model"
	"radiation-dose-budget-control/backend/internal/repository"
)

func newCompareTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:compare-asof-530?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	if err := db.AutoMigrate(
		&model.WorkerProfile{}, &model.WorkPermitPlan{}, &model.ExposureEntry{},
		&model.DoseBudgetAssessment{}, &model.AuditEvent{}, &model.User{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestCompareRecalculatesEachScenarioAtItsOwnAsOf(t *testing.T) {
	db := newCompareTestDB(t)
	periodStart := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	worker := model.WorkerProfile{
		WorkerCode: "W-ASOF", DisplayName: "As Of Worker", AuthorizationLevel: "L1",
		AnnualLimitMSV: 20, AdministrativeLimitMSV: 12, ProfileStatus: "active",
		PeriodStart: periodStart, Version: 1, CreatedAt: periodStart, UpdatedAt: periodStart,
	}
	if err := db.Create(&worker).Error; err != nil {
		t.Fatalf("create worker: %v", err)
	}
	createPlan := func(code string, rate float64, minutes int) model.WorkPermitPlan {
		plan := model.WorkPermitPlan{
			PlanCode: code, WorkerID: worker.ID, WorkArea: "Bay", TaskCategory: "Survey",
			EstimatedRateMSVH: rate, PlannedMinutes: minutes, ControlsJSON: "[]",
			PermitStatus: constants.PermitStatusDraft, Version: 1, CreatedAt: periodStart, UpdatedAt: periodStart,
		}
		if err := db.Create(&plan).Error; err != nil {
			t.Fatalf("create plan: %v", err)
		}
		return plan
	}
	planA := createPlan("PLAN-ASOF-A", 0, 60)
	planB := createPlan("PLAN-ASOF-B", 0, 60)
	occurred := periodStart.Add(30 * 24 * time.Hour)
	earlyVerify := periodStart.Add(60 * 24 * time.Hour)
	laterVerify := periodStart.Add(120 * 24 * time.Hour)
	earlyEntry := model.ExposureEntry{
		WorkerID: worker.ID, SourceRef: "EARLY", OccurredAt: occurred, DoseMSV: 1,
		EntryType: constants.EntryTypeConfirmed, QualityFlag: constants.QualityFlagVerified,
		VerifiedAt: &earlyVerify, CreatedBy: 1, CreatedAt: earlyVerify,
	}
	lateEntry := model.ExposureEntry{
		WorkerID: worker.ID, SourceRef: "LATE", OccurredAt: occurred.Add(time.Hour), DoseMSV: 2,
		EntryType: constants.EntryTypeConfirmed, QualityFlag: constants.QualityFlagVerified,
		VerifiedAt: &laterVerify, CreatedBy: 1, CreatedAt: laterVerify,
	}
	if err := db.Create(&earlyEntry).Error; err != nil {
		t.Fatalf("create early entry: %v", err)
	}
	if err := db.Create(&lateEntry).Error; err != nil {
		t.Fatalf("create late entry: %v", err)
	}
	assessmentSvc := NewDoseBudgetAssessmentService(
		db,
		repository.NewDoseBudgetAssessmentRepository(db),
		repository.NewWorkPermitPlanRepository(db),
		repository.NewWorkerProfileRepository(db),
		repository.NewExposureEntryRepository(db),
		NewAuditService(repository.NewSystemRepository(db)),
		0.9, "ALARA-2026.1",
	)
	earlyCutOff := periodStart.Add(90 * 24 * time.Hour)
	lateCutOff := periodStart.Add(150 * 24 * time.Hour)
	response, err := assessmentSvc.Compare(dto.CompareDoseBudgetRequest{
		Scenarios: []dto.CompareScenarioInput{
			{PlanID: planA.ID, AsOf: &lateCutOff},
			{PlanID: planB.ID, AsOf: &earlyCutOff},
		},
	})
	if err != nil {
		t.Fatalf("compare: %v", err)
	}
	if response.PeriodEndsAligned {
		t.Fatal("period_ends_aligned = true, want false for different as_of windows")
	}
	if response.AsOfNotice == "" {
		t.Fatal("as_of_notice should flag mixed evaluation windows")
	}
	if response.Scenarios[0].PeriodDoseMSV != 3 {
		t.Fatalf("late cut-off period dose = %v, want 3 (both entries)", response.Scenarios[0].PeriodDoseMSV)
	}
	if response.Scenarios[1].PeriodDoseMSV != 1 {
		t.Fatalf("early cut-off period dose = %v, want 1 (later verification excluded)", response.Scenarios[1].PeriodDoseMSV)
	}
	if !response.Scenarios[0].AsOf.Equal(lateCutOff) || !response.Scenarios[1].AsOf.Equal(earlyCutOff) {
		t.Fatalf("scenario as_of = %s / %s", response.Scenarios[0].AsOf, response.Scenarios[1].AsOf)
	}
	if !response.Scenarios[0].PeriodStart.Equal(periodStart) {
		t.Fatal("scenario window must still start at the worker period start")
	}
}

func TestCompareMissingAsOfAlignsToEarliestBatchCutOff(t *testing.T) {
	db := newCompareTestDB(t)
	periodStart := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	worker := model.WorkerProfile{
		WorkerCode: "W-ALIGN", DisplayName: "Align Worker", AuthorizationLevel: "L1",
		AnnualLimitMSV: 20, AdministrativeLimitMSV: 12, ProfileStatus: "active",
		PeriodStart: periodStart, Version: 1, CreatedAt: periodStart, UpdatedAt: periodStart,
	}
	if err := db.Create(&worker).Error; err != nil {
		t.Fatalf("create worker: %v", err)
	}
	plan := model.WorkPermitPlan{
		PlanCode: "PLAN-ALIGN", WorkerID: worker.ID, WorkArea: "Bay", TaskCategory: "Survey",
		EstimatedRateMSVH: 0, PlannedMinutes: 60, ControlsJSON: "[]",
		PermitStatus: constants.PermitStatusDraft, Version: 1, CreatedAt: periodStart, UpdatedAt: periodStart,
	}
	if err := db.Create(&plan).Error; err != nil {
		t.Fatalf("create plan: %v", err)
	}
	second := model.WorkPermitPlan{
		PlanCode: "PLAN-ALIGN-2", WorkerID: worker.ID, WorkArea: "Bay", TaskCategory: "Survey",
		EstimatedRateMSVH: 0, PlannedMinutes: 60, ControlsJSON: "[]",
		PermitStatus: constants.PermitStatusDraft, Version: 1, CreatedAt: periodStart, UpdatedAt: periodStart,
	}
	if err := db.Create(&second).Error; err != nil {
		t.Fatalf("create second plan: %v", err)
	}
	assessmentSvc := NewDoseBudgetAssessmentService(
		db,
		repository.NewDoseBudgetAssessmentRepository(db),
		repository.NewWorkPermitPlanRepository(db),
		repository.NewWorkerProfileRepository(db),
		repository.NewExposureEntryRepository(db),
		NewAuditService(repository.NewSystemRepository(db)),
		0.9, "ALARA-2026.1",
	)
	explicitCutOff := periodStart.Add(60 * 24 * time.Hour)
	requestEnd := periodStart.Add(200 * 24 * time.Hour)
	response, err := assessmentSvc.Compare(dto.CompareDoseBudgetRequest{
		PeriodEnd: requestEnd,
		Scenarios: []dto.CompareScenarioInput{
			{PlanID: plan.ID, AsOf: &explicitCutOff},
			{PlanID: second.ID},
		},
	})
	if err != nil {
		t.Fatalf("compare: %v", err)
	}
	if response.Scenarios[1].AsOfSource != "aligned_earliest" {
		t.Fatalf("missing as_of source = %q, want aligned_earliest", response.Scenarios[1].AsOfSource)
	}
	if !response.Scenarios[1].AsOf.Equal(explicitCutOff) {
		t.Fatalf("aligned cut-off = %s, want %s", response.Scenarios[1].AsOf, explicitCutOff)
	}
	if response.AsOfNotice == "" {
		t.Fatal("alignment within the batch should be explained in as_of_notice")
	}
}

func TestCompareRejectsFutureAndOversizedWindows(t *testing.T) {
	db := newCompareTestDB(t)
	periodStart := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	worker := model.WorkerProfile{
		WorkerCode: "W-WINDOW", DisplayName: "Window Worker", AuthorizationLevel: "L1",
		AnnualLimitMSV: 20, AdministrativeLimitMSV: 12, ProfileStatus: "active",
		PeriodStart: periodStart, Version: 1, CreatedAt: periodStart, UpdatedAt: periodStart,
	}
	if err := db.Create(&worker).Error; err != nil {
		t.Fatalf("create worker: %v", err)
	}
	plans := make([]model.WorkPermitPlan, 2)
	for index := range plans {
		plans[index] = model.WorkPermitPlan{
			PlanCode: "PLAN-WINDOW-" + string(rune('A'+index)), WorkerID: worker.ID, WorkArea: "Bay",
			TaskCategory: "Survey", EstimatedRateMSVH: 0, PlannedMinutes: 60, ControlsJSON: "[]",
			PermitStatus: constants.PermitStatusDraft, Version: 1, CreatedAt: periodStart, UpdatedAt: periodStart,
		}
		if err := db.Create(&plans[index]).Error; err != nil {
			t.Fatalf("create plan: %v", err)
		}
	}
	assessmentSvc := NewDoseBudgetAssessmentService(
		db,
		repository.NewDoseBudgetAssessmentRepository(db),
		repository.NewWorkPermitPlanRepository(db),
		repository.NewWorkerProfileRepository(db),
		repository.NewExposureEntryRepository(db),
		NewAuditService(repository.NewSystemRepository(db)),
		0.9, "ALARA-2026.1",
	)
	future := time.Now().UTC().Add(48 * time.Hour)
	if _, err := assessmentSvc.Compare(dto.CompareDoseBudgetRequest{
		Scenarios: []dto.CompareScenarioInput{
			{PlanID: plans[0].ID, AsOf: &future}, {PlanID: plans[1].ID, AsOf: &future},
		},
	}); err == nil {
		t.Fatal("future as_of must be rejected")
	}
	oversized := periodStart.Add(371 * 24 * time.Hour)
	if oversized.Before(time.Now().UTC()) {
		t.Skip("fixed period fixtures are in the past relative to the host clock")
	}
	if _, err := assessmentSvc.Compare(dto.CompareDoseBudgetRequest{
		Scenarios: []dto.CompareScenarioInput{
			{PlanID: plans[0].ID, AsOf: &oversized}, {PlanID: plans[1].ID, AsOf: &oversized},
		},
	}); err == nil {
		t.Fatal("window longer than 370 days must be rejected")
	}
}

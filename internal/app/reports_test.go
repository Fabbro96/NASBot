package app

import (
	"strings"
	"testing"

	"nasbot/pkg/model"
)

func TestGenerateReport_EmptyState(t *testing.T) {
	ctx := model.InitApp(nil)
	ctx.Config = &model.Config{}
	report := generateReport(ctx, true, func(string) {})
	if report == "" {
		t.Errorf("expected non-empty report")
	}
	if !strings.Contains(report, "Containers:") {
		t.Errorf("expected Containers: in report")
	}
}

func TestGenerateReport_WithStats(t *testing.T) {
	ctx := model.InitApp(nil)
	ctx.Config = &model.Config{}
	ctx.State.AddEvent("cpu", "High CPU usage!")
	ctx.Stats.Set(model.Stats{
		CPU: 95.5,
		RAM: 80.2,
	})

	report := generateReport(ctx, true, func(string) {})
	if !strings.Contains(report, "High CPU usage!") {
		t.Errorf("expected alert in report, got: %s", report)
	}
}

func TestGenerateReport_MultipleAlerts(t *testing.T) {
	ctx := model.InitApp(nil)
	ctx.Config = &model.Config{}
	ctx.State.AddEvent("alert", "Alert 1")
	report := generateReport(ctx, true, func(string) {})
	if !strings.Contains(report, "Alert 1") {
		t.Errorf("expected alerts in report")
	}
}

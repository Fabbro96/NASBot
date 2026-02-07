package main

import (
	"math"
	"testing"
	"time"
)

func TestPredictDiskFull_DecreasingFree(t *testing.T) {
	giB := uint64(1024 * 1024 * 1024)
	start := time.Now().Add(-10 * 24 * time.Hour)
	history := []DiskUsagePoint{
		{Time: start, SSDFree: 100 * giB},
		{Time: start.Add(10 * 24 * time.Hour), SSDFree: 90 * giB},
	}

	pred := predictDiskFull(history, true)
	if pred.DaysUntilFull <= 0 {
		t.Fatalf("expected positive days until full, got %.2f", pred.DaysUntilFull)
	}
	if math.Abs(pred.DaysUntilFull-90) > 0.5 {
		t.Fatalf("expected ~90 days, got %.2f", pred.DaysUntilFull)
	}
	if math.Abs(pred.GBPerDay-1) > 0.05 {
		t.Fatalf("expected ~1 GB/day, got %.2f", pred.GBPerDay)
	}
}

func TestPredictDiskFull_StableFree(t *testing.T) {
	giB := uint64(1024 * 1024 * 1024)
	start := time.Now().Add(-2 * 24 * time.Hour)
	history := []DiskUsagePoint{
		{Time: start, SSDFree: 100 * giB},
		{Time: start.Add(2 * 24 * time.Hour), SSDFree: 100 * giB},
	}

	pred := predictDiskFull(history, true)
	if pred.DaysUntilFull != -1 {
		t.Fatalf("expected DaysUntilFull -1, got %.2f", pred.DaysUntilFull)
	}
}

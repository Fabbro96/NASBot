package commands

import (
	"fmt"
	"strings"
	"time"

	"nasbot/internal/format"
)

// getDiskPredictionText estimates when disks will be full
func getDiskPredictionText(ctx *AppContext) string {
	ctx.State.Mu.Lock()
	history := make([]DiskUsagePoint, len(ctx.State.DiskHistory))
	copy(history, ctx.State.DiskHistory)
	ctx.State.Mu.Unlock()

	var b strings.Builder
	b.WriteString("📊 *Disk Space Prediction*\n\n")

	if len(history) < 12 { // Need at least 1 hour of data
		b.WriteString("_Collecting data... need at least 1 hour of history._\n\n")
		b.WriteString(fmt.Sprintf("_Data points: %d/12_", len(history)))
		return b.String()
	}

	// Calculate trend for SSD
	ssdPred := predictDiskFull(history, true)
	hddPred := predictDiskFull(history, false)

	s, _ := ctx.Stats.Get()

	// SSD
	writeDiskPred := func(icon, name string, pred DiskPrediction, usedPct float64) {
		b.WriteString(fmt.Sprintf("%s *%s* — %.1f%% used\n", icon, name, usedPct))
		switch {
		case pred.DaysUntilFull < 0:
			b.WriteString("   📈 _Usage decreasing or stable_\n")
		case pred.DaysUntilFull > 365:
			b.WriteString("   ✅ _More than a year until full_\n")
		case pred.DaysUntilFull > 30:
			b.WriteString(fmt.Sprintf("   ✅ ~%d days until full\n", int(pred.DaysUntilFull)))
		case pred.DaysUntilFull > 7:
			b.WriteString(fmt.Sprintf("   ⚠️ ~%d days until full\n", int(pred.DaysUntilFull)))
		default:
			b.WriteString(fmt.Sprintf("   🚨 ~%d days until full!\n", int(pred.DaysUntilFull)))
		}
		b.WriteString(fmt.Sprintf("   _Rate: %.2f GB/day_\n\n", pred.GBPerDay))
	}

	writeDiskPred("💿", "SSD", ssdPred, s.VolSSD.Used)
	writeDiskPred("🗄", "HDD", hddPred, s.VolHDD.Used)

	b.WriteString(fmt.Sprintf("\n_Based on %d data points (%s of data)_",
		len(history),
		format.FormatDuration(time.Since(history[0].Time))))

	return b.String()
}

func GetDiskPredictionText(ctx *AppContext) string { return getDiskPredictionText(ctx) }

// predictDiskFull calculates days until disk is full using linear regression
func predictDiskFull(history []DiskUsagePoint, isSSD bool) DiskPrediction {
	if len(history) < 2 {
		return DiskPrediction{DaysUntilFull: -1}
	}

	first := history[0]
	last := history[len(history)-1]

	var firstFree, lastFree uint64
	if isSSD {
		firstFree = first.SSDFree
		lastFree = last.SSDFree
	} else {
		firstFree = first.HDDFree
		lastFree = last.HDDFree
	}

	timeDiff := last.Time.Sub(first.Time).Hours() / 24 // Days
	if timeDiff < 0.01 {
		return DiskPrediction{DaysUntilFull: -1}
	}

	// GB change (negative means filling up)
	gbChange := float64(int64(lastFree)-int64(firstFree)) / 1024 / 1024 / 1024
	gbPerDay := gbChange / timeDiff

	if gbPerDay >= 0 {
		// Disk is freeing up or stable
		return DiskPrediction{DaysUntilFull: -1, GBPerDay: -gbPerDay}
	}

	// Days until free space = 0
	currentFreeGB := float64(lastFree) / 1024 / 1024 / 1024
	daysUntilFull := currentFreeGB / (-gbPerDay)

	return DiskPrediction{
		DaysUntilFull: daysUntilFull,
		GBPerDay:      -gbPerDay,
	}
}

// recordDiskUsage adds current disk usage to history
func recordDiskUsage(ctx *AppContext) {
	s, ready := ctx.Stats.Get()

	if !ready {
		return
	}

	ctx.State.Mu.Lock()
	defer ctx.State.Mu.Unlock()

	point := DiskUsagePoint{
		Time:    time.Now(),
		SSDUsed: s.VolSSD.Used,
		HDDUsed: s.VolHDD.Used,
		SSDFree: s.VolSSD.Free,
		HDDFree: s.VolHDD.Free,
	}

	ctx.State.DiskHistory = append(ctx.State.DiskHistory, point)

	// Keep max 7 days of data (at 5-min intervals = 2016 points)
	if len(ctx.State.DiskHistory) > 2016 {
		ctx.State.DiskHistory = ctx.State.DiskHistory[1:]
	}
}

func RecordDiskUsage(ctx *AppContext) {
	recordDiskUsage(ctx)
}

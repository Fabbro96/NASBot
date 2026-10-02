package commands

import (
	"fmt"
	"strings"
	"time"

	"nasbot/internal/format"
)

// minDiskPoints is the number of samples a regression needs before a disk
// prediction is shown.
const minDiskPoints = 2

// minDiskSpanDays is the minimum time span between the first and the last
// sample used by the regression.
const minDiskSpanDays = 0.01

// minFillGBPerDay is the noise floor of the regression (10 MB/day). Below it a
// disk is stable: the least-squares slope of a perfectly constant series is not
// exactly zero in floating point, and a slope of -1e-13 GB/day would be reported
// as "more than a year until full" instead of "not filling up".
const minFillGBPerDay = 0.01

// predState tells apart the outcomes a consumer must not confuse: a disk that
// is not filling up is a different thing from a disk with no data at all
// (mounted less than the length of the history window).
type predState int

const (
	predNoData predState = iota
	predNotFilling
	predFilling
)

func (s predState) String() string {
	switch s {
	case predNotFilling:
		return "not_filling"
	case predFilling:
		return "filling"
	default:
		return "no_data"
	}
}

// getDiskPredictionText estimates when disks will be full
func getDiskPredictionText(ctx *AppContext) string {
	tr := ctx.Tr
	ctx.State.Mu.Lock()
	history := make([]DiskUsagePoint, len(ctx.State.DiskHistory))
	copy(history, ctx.State.DiskHistory)
	ctx.State.Mu.Unlock()

	var b strings.Builder
	b.WriteString(tr("diskpred_title"))

	if len(history) < 12 { // Need at least 1 hour of data
		b.WriteString(tr("diskpred_collecting"))
		b.WriteString(trf(tr, "diskpred_datapoints", len(history)))
		return b.String()
	}

	// Calculate trend for SSD
	ssdPred, ssdState := predictDiskFullState(history, "SSD")

	s, _ := ctx.Stats.Get()

	// SSD
	writeDiskPred := func(icon, name string, pred DiskPrediction, state predState, usedPct float64) {
		b.WriteString(fmt.Sprintf("%s *%s* — %.1f%% used\n", icon, name, usedPct))
		switch state {
		case predNoData:
			b.WriteString(tr("diskpred_no_data"))
		case predNotFilling:
			b.WriteString(tr("diskpred_decreasing"))
			b.WriteString(trf(tr, "diskpred_rate", pred.GBPerDay))
			return
		default:
			switch {
			case pred.DaysUntilFull > 365:
				b.WriteString(tr("diskpred_year_plus"))
			case pred.DaysUntilFull > 30:
				b.WriteString(trf(tr, "diskpred_days_ok", int(pred.DaysUntilFull)))
			case pred.DaysUntilFull > 7:
				b.WriteString(trf(tr, "diskpred_days_warn", int(pred.DaysUntilFull)))
			default:
				b.WriteString(trf(tr, "diskpred_days_crit", int(pred.DaysUntilFull)))
			}
		}
		b.WriteString(trf(tr, "diskpred_rate", pred.GBPerDay))
	}

	writeDiskPred("💿", "SSD", ssdPred, ssdState, s.VolSSD.Used)
	for _, mount := range volumeMounts(s.SecondaryVols) {
		pred, state := predictDiskFullState(history, mount)
		writeDiskPred("🗄", diskDisplayName(mount), pred, state, s.SecondaryVols[mount].Used)
	}

	b.WriteString(trf(tr, "diskpred_footer",
		len(history),
		format.FormatDuration(time.Since(history[0].Time))))

	return b.String()
}

func GetDiskPredictionText(ctx *AppContext) string { return getDiskPredictionText(ctx) }

// diskSample is one usable (time, free space) observation of a single disk.
type diskSample struct {
	days   float64 // days elapsed since the first sample of the regression
	freeGB float64
}

// collectDiskSamples extracts the free-space series of diskName from history.
// Secondary disks are looked up with the map presence check: a disk mounted
// less than the length of the history window has no entry in the oldest points,
// and treating the missing zero as a real measurement produced a phantom fill
// rate.
func collectDiskSamples(history []DiskUsagePoint, diskName string) ([]diskSample, error) {
	if len(history) < minDiskPoints {
		return nil, fmt.Errorf("history too short: %d points", len(history))
	}

	t0 := history[0].Time
	samples := make([]diskSample, 0, len(history))
	for _, p := range history {
		var free uint64
		var ok bool
		if diskName == "SSD" {
			free, ok = p.SSDFree, true
		} else {
			free, ok = p.SecondaryFree[diskName]
		}
		if !ok {
			continue
		}
		days := p.Time.Sub(t0).Hours() / 24
		if days < 0 {
			continue // history out of order: ignore rather than invert the slope
		}
		samples = append(samples, diskSample{
			days:   days,
			freeGB: float64(free) / 1024 / 1024 / 1024,
		})
	}

	if len(samples) < minDiskPoints {
		return nil, fmt.Errorf("only %d samples for %q", len(samples), diskName)
	}
	return samples, nil
}

// predictDiskFull estimates the days until diskName is full.
//
// GBPerDay is always the positive daily consumption (GB/day) and the direction
// of the trend comes from DaysUntilFull alone:
//
//	-1 with GBPerDay == 0  -> not filling up (stable or growing)
//	>0                    -> filling up, filled in that many days
//
// predictDiskFullState adds the distinction the numeric result cannot carry:
// "no data yet" (a disk that was not mounted for the whole history window) is
// a separate state, not a prediction.
func predictDiskFull(history []DiskUsagePoint, diskName string) DiskPrediction {
	pred, _ := predictDiskFullState(history, diskName)
	return pred
}

func predictDiskFullState(history []DiskUsagePoint, diskName string) (DiskPrediction, predState) {
	samples, err := collectDiskSamples(history, diskName)
	if err != nil {
		return DiskPrediction{DaysUntilFull: -1}, predNoData
	}

	span := samples[len(samples)-1].days - samples[0].days
	if span < minDiskSpanDays {
		return DiskPrediction{DaysUntilFull: -1}, predNoData
	}

	// Least squares regression of freeGB over days: every sample counts, so a
	// single big backup or download in the last point can no longer flip the
	// prediction from "over a year" to "3 days".
	var sumX, sumY, sumXY, sumXX float64
	for _, s := range samples {
		sumX += s.days
		sumY += s.freeGB
		sumXY += s.days * s.freeGB
		sumXX += s.days * s.days
	}
	n := float64(len(samples))
	denom := n*sumXX - sumX*sumX
	if denom == 0 {
		return DiskPrediction{DaysUntilFull: -1}, predNoData
	}
	slopeGBPerDay := (n*sumXY - sumX*sumY) / denom // negative == filling up

	if slopeGBPerDay >= -minFillGBPerDay {
		// Free space stable or growing: no consumption to report.
		return DiskPrediction{DaysUntilFull: -1}, predNotFilling
	}

	gbPerDay := -slopeGBPerDay
	currentFreeGB := samples[len(samples)-1].freeGB
	daysUntilFull := currentFreeGB / gbPerDay

	return DiskPrediction{
		DaysUntilFull: daysUntilFull,
		GBPerDay:      gbPerDay,
	}, predFilling
}

// recordDiskUsage adds current disk usage to history
func recordDiskUsage(ctx *AppContext) {
	s, ready := ctx.Stats.Get()

	if !ready {
		return
	}

	ctx.State.Mu.Lock()
	defer ctx.State.Mu.Unlock()

	secUsed := make(map[string]float64)
	secFree := make(map[string]uint64)
	for mount, vol := range s.SecondaryVols {
		secUsed[mount] = vol.Used
		secFree[mount] = vol.Free
	}

	point := DiskUsagePoint{
		Time:          time.Now(),
		SSDUsed:       s.VolSSD.Used,
		SSDFree:       s.VolSSD.Free,
		SecondaryUsed: secUsed,
		SecondaryFree: secFree,
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

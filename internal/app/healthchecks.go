package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"nasbot/internal/format"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// MaxDowntimeEvents is the max number of downtime events to keep in history
const MaxDowntimeEvents = 50

const (
	// healthchecksForceRebootDefaultMins è il timeout usato quando
	// healthchecks.force_reboot_after_minutes non è valorizzato ma il riavvio
	// forzato è abilitato. Deve combaciare con defaultForceRebootAfterMins in
	// config_defaults.go (15): quel file non è di questa corsia, quindi se i
	// due divergono vince il default scritto lì e questo è solo il fallback
	// per un contesto costruito a mano.
	//
	// Perché 15 e non i 6 di prima: il pinger non distingue "il NAS è
	// bloccato" da "la rete è caduta", e i due casi si sovrappongono in un
	// flap. 6 minuti sono meno di un ciclo di retry tipico del modem (30-60s
	// per some, 2-5 min per una WAN che renegozia) più il tempo di boot del
	// NAS: un riavvio scattava mentre la rete era ancora in recupero. 15
	// minuti assorbono quel flap restando abbastanza rapidi da recuperare un
	// blocco vero, che è il caso d'uso per cui l'utente ha voluto questa
	// funzione: se il NAS è bianco, l'unica cura è il riavvio.
	healthchecksForceRebootDefaultMins = 15

	// healthchecksMinRebootInterval è il pavimento tra due riavvii forzati
	// consecutivi. Con force_reboot_after_minutes a 15 è il doppio: un NAS
	// che riavvia, riparte e viene giudicato di nuovo "morto" per colpa della
	// rete ancora down aspetta altri 15 minuti invece di ripartire subito.
	//
	// Serve a coprire il caso che NetForceRebootTriggered non copre: quel
	// flag è volatile e si azzera a ogni ping riuscito, quindi un NAS che
	// risponde per un istante e poi tace lo ri-armerebbe. Con il pavimento,
	// due riavvii non possono mai stare a meno di mezz'ora.
	healthchecksMinRebootInterval = 30 * time.Minute

	// downtimeResumeWindow: un evento di downtime ancora aperto (EndTime zero)
	// più vecchio di questo viene considerato abbandonato e non ripreso.
	// Serve a non trascinare per mesi uno stato rimasto su un file che il
	// backup ha riportato indietro.
	downtimeResumeWindow = 6 * time.Hour
)

// forcedRebootReasonPrefix marca il Reason dell'evento di downtime che un
// riavvio forzato ha aperto. Serve a due cose:
//
//	un riavvio forzato chiude l'evento di downtime che lo ha causato e ne apre
//	uno nuovo, il cui StartTime è quindi l'istante esatto del riavvio e il cui
//	Reason porta questo prefisso.
//
// Il prefisso è leggibile perché finisce in /health (getHealthchecksStats) e
// nel riassunto per Gemini: "⚡ reboot forced: network error" dice all'utente
// che il NAS è stato riavviato, dove un tag tipo "[internal-3]" non direbbe
// nulla.
//
// Il tracciato non dipende più soltanto da questo testo: da quando
// HealthchecksState.LastForcedReboot esiste, l'istante del riavvio sta in un
// campo time.Time vero e proprio, e sinceForcedRebootLocked legge quello. Il
// Reason resta la parte leggibile, non più la fonte unica di verità.
const forcedRebootReasonPrefix = "⚡ reboot forced: "

// rebootOpenedEvent apre l'evento di downtime che segue un riavvio forzato e
// chiude quello che lo ha causato.
//
// Chiudere il vecchio evento non è un dettaglio: è così che l'evento nuovo ha
// StartTime = istante del riavvio.
//
// LastForcedReboot iscrives lo stesso istante in un campo dedicato: è la prova
// esplicita che questo bot ha già riavviato, e resta valida anche se il log
// viene svuotato da /health o trimmed per MaxDowntimeEvents.
//
// L'evento nuovo resta "aperto" perché il NAS potrebbe non essere ancora
// tornato: recordHealthcheckSuccess lo chiuderà e manderà la notifica di
// recupero, che è esattamente ciò che l'utente vuole vedere.
//
// Must be called with ctx.Monitor.Mu held.
func rebootOpenedEventLocked(hc *HealthchecksState, reason string) {
	now := time.Now()
	if n := len(hc.DowntimeEvents); n > 0 {
		prev := &hc.DowntimeEvents[n-1]
		prev.EndTime = now
		prev.Duration = format.FormatDuration(prev.EndTime.Sub(prev.StartTime))
	}
	hc.LastForcedReboot = now
	hc.DowntimeEvents = append(hc.DowntimeEvents, DowntimeLog{
		StartTime: now,
		Reason:    forcedRebootReasonPrefix + reason,
	})
	if len(hc.DowntimeEvents) > MaxDowntimeEvents {
		hc.DowntimeEvents = hc.DowntimeEvents[len(hc.DowntimeEvents)-MaxDowntimeEvents:]
	}
}

// healthchecksForceRebootAfter traduce la config in un timeout per il riavvio
// forzato. 0 = non riavviare mai. Mirror di networkForceRebootAfter.
//
// Il default è ATTIVO: healthchecks.io fa ping al NAS, quindi un pinger che
// tace significa "il mio NAS non risponde", e su un NAS bloccato l'unica
// cura è il riavvio. Disattivarlo di default toglieva all'utente l'unica
// capacità di recupero che ha. Il pericolo reale di questa funzione non è il
// singolo riavvio, è il ciclo: se a cadere è la rete e non il NAS, il ping non
// torna comunque e il bot riavvierebbe per sempre. È compito di
// healthchecksMinRebootInterval e di NetForceRebootTriggered, non del default.
func healthchecksForceRebootAfter(cfg *Config) time.Duration {
	if cfg == nil || !cfg.Healthchecks.ForceRebootOnDown {
		return 0
	}
	mins := cfg.Healthchecks.ForceRebootAfterMins
	if mins <= 0 {
		mins = healthchecksForceRebootDefaultMins
	}
	return time.Duration(mins) * time.Minute
}

// downtimeOpenLocked dice se l'ultimo evento di downtime persistito è ancora
// aperto. Serve perché Monitor.HealthInDowntime è volatile: senza questo, un
// riavvio del solo processo riapriva un evento nuovo e il countdown del
// riavvio forzato ripartiva da zero, quindi il reboot si ri-armava a ogni boot
// durante un outage prolungato.
//
// DowntimeEvents fa già parte di BotState (state.go:42, salvata da saveState e
// riletta da loadState), quindi questa derivazione regge finché il file di
// stato sopravvive. Se un giorno si vuole la persistenza esplicita, il campo
// mancante è:
//
//	HealthInDowntime bool `json:"health_in_downtime"`
//
// su BotState (state.go:41-42), con tre righe: copy in loadState accanto a
// `ctx.Monitor.Healthchecks = state.Healthchecks` (state.go:81-83), copy in
// saveState accanto a `healthchecks := ctx.Monitor.Healthchecks`
// (state.go:153-160), e il campo inizializzato in BotState{...} (state.go:174).
// Finché quel campo non esiste, downtimeOpenLocked ne fa le veci e le due copie
// vanno tenute allineate.
//
// Must be called with ctx.Monitor.Mu held.
func downtimeOpenLocked(hc *HealthchecksState) bool {
	if len(hc.DowntimeEvents) == 0 {
		return false
	}
	last := hc.DowntimeEvents[len(hc.DowntimeEvents)-1]
	if !last.EndTime.IsZero() {
		return false
	}
	return time.Since(last.StartTime) < downtimeResumeWindow
}

// sinceForcedRebootLocked restituisce da quanto tempo è scattato l'ultimo
// riavvio forzato. Ok=false se non ce n'è stato uno.
//
// Legge LastForcedReboot, il campo dedicato, e solo in sua assenza cade sul log
// di downtime (l'evento con Reason che comincia per forcedRebootReasonPrefix è
// quello aperto da rebootOpenedEventLocked). Il fallback serve per uno stato
// scritto da una versione precedente, che non aveva il campo: senza di esso un
// aggiornamento riporterebbe il NAS al comportamento di prima, cioè con il
// pavimento tra due riavvii dipendente dal testo.
//
// Must be called with ctx.Monitor.Mu held.
func sinceForcedRebootLocked(hc *HealthchecksState) (time.Duration, bool) {
	if !hc.LastForcedReboot.IsZero() {
		return time.Since(hc.LastForcedReboot), true
	}
	for i := len(hc.DowntimeEvents) - 1; i >= 0; i-- {
		ev := hc.DowntimeEvents[i]
		if !strings.HasPrefix(ev.Reason, forcedRebootReasonPrefix) {
			continue
		}
		return time.Since(ev.StartTime), true
	}
	return 0, false
}

// forcedRebootAllowedLocked decide se è il momento di riavviare.
//
// Tre condizioni, tutte necessarie:
//
//  1. downFor >= forceAfter: la soglia configurata (15 minuti al default).
//  2. !episodeAlreadyRebootedLocked: UN riavvio per episodio di downtime.
//  3. sinceReboot >= healthchecksMinRebootInterval: il pavimento assoluto, che
//     sopravvive anche a un azzeramento del flag.
//
// La 2 da sola non basta: se il NAS risponde per un istante e poi tace,
// l'1 passa (l'evento di downtime è nuovo) e il flag è di nuovo falso, e
// senza la 3 il bot riavvierebbe due volte in pochi minuti. La 3 da sola non
// basta: non distingue "outage iniziato adesso" da "outage in corso da ore,
// già riavviato una volta".
//
// Must be called with ctx.Monitor.Mu held.
func forcedRebootAllowedLocked(hc *HealthchecksState, downFor, forceAfter time.Duration) (bool, string) {
	if forceAfter <= 0 {
		return false, "disabled"
	}
	if downFor < forceAfter {
		return false, "below_threshold"
	}
	if episodeAlreadyRebootedLocked(hc) {
		return false, "already_rebooted_this_episode"
	}
	if since, ok := sinceForcedRebootLocked(hc); ok && since < healthchecksMinRebootInterval {
		return false, "min_interval"
	}
	return true, "ok"
}

// episodeAlreadyRebootedLocked dice se l'episodio di downtime in corso ha già
// avuto il suo riavvio forzato.
//
// Due fonti, perché ognuna copre il difetto dell'altra:
//
//   - NetForceRebootTriggered: impostato al momento del riavvio e azzerato da
//     recordHealthcheckSuccess, quindi si ri-arma a outage finito. È un flag: se
//     un ripristino dello stato lo perde, il gate si riapre.
//   - LastForcedReboot + l'evento di downtime aperto: l'evento che
//     rebootOpenedEventLocked apre ha StartTime == LastForcedReboot, quindi
//     "l'ultimo riavvio è dentro questo episodio" è una domanda con risposta
//     esatta, che non dipende da un flag né dal testo del Reason.
//
// Must be called with ctx.Monitor.Mu held.
func episodeAlreadyRebootedLocked(hc *HealthchecksState) bool {
	if hc.NetForceRebootTriggered {
		return true
	}
	if hc.LastForcedReboot.IsZero() || len(hc.DowntimeEvents) == 0 {
		return false
	}
	open := hc.DowntimeEvents[len(hc.DowntimeEvents)-1]
	if !open.EndTime.IsZero() {
		// L'episodio è chiuso: il prossimo fallimento ne apre uno nuovo, che
		// riparte da zero come deve.
		return false
	}
	return !hc.LastForcedReboot.Before(open.StartTime)
}

// ═══════════════════════════════════════════════════════════════════
//  HEALTHCHECKS.IO INTEGRATION
// ═══════════════════════════════════════════════════════════════════

// startHealthchecksPinger starts the background goroutine that pings healthchecks.io
func startHealthchecksPinger(ctx *AppContext, bot BotAPI, runCtx context.Context) {
	hc := ctx.Cfg().Healthchecks
	if !hc.Enabled || hc.PingURL == "" {
		slog.Info("Healthchecks.io disabled or no URL configured")
		return
	}

	period := hc.PeriodSeconds
	if period <= 0 {
		period = 60 // default 1 minute
	}

	slog.Info("Healthchecks.io pinger started", "period_sec", period)

	ticker := time.NewTicker(time.Duration(period) * time.Second)
	defer ticker.Stop()

	// Initial ping on startup
	pingHealthchecks(ctx, bot)

	for {
		select {
		case <-runCtx.Done():
			return
		case <-ticker.C:
			pingHealthchecks(ctx, bot)
		}
	}
}

// redactPingURLIn masks the ping UUID inside an error message.
//
// The *url.Error that http.Client returns embeds the request URL verbatim, so
// the reason string of a failed ping would otherwise carry the healthchecks.io
// UUID to the chat and to the persisted downtime log. It is the same secret
// redactPingURL hides from /configjson, reached from the other direction.
func redactPingURLIn(text, pingURL string) string {
	if text == "" || pingURL == "" {
		return text
	}
	return strings.ReplaceAll(text, pingURL, redactPingURL(pingURL))
}

// pingHealthchecks sends a ping to healthchecks.io
func pingHealthchecks(appCtx *AppContext, bot BotAPI) {
	pingURL := appCtx.Cfg().Healthchecks.PingURL
	if pingURL == "" {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// The reason reaches the user: it lands in the hc_down_alert message and in
	// the persisted downtime log, so both errors below go through sanitizeErr
	// like any other error bound for Telegram. The ping URL needs its own
	// treatment, because the *url.Error embeds the request URL and the UUID in
	// it is the only thing that lets anyone publish to this check.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pingURL, nil)
	if err != nil {
		recordHealthcheckFailure(appCtx, bot, fmt.Sprintf("request error: %v", redactPingURLIn(sanitizeErr(err).Error(), pingURL)))
		return
	}

	if appCtx.HTTP == nil {
		appCtx.HTTP = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := appCtx.HTTP.Do(req)
	if err != nil {
		recordHealthcheckFailure(appCtx, bot, fmt.Sprintf("network error: %v", redactPingURLIn(sanitizeErr(err).Error(), pingURL)))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		recordHealthcheckSuccess(appCtx, bot)
	} else {
		recordHealthcheckFailure(appCtx, bot, fmt.Sprintf("HTTP %d", resp.StatusCode))
	}
}

// recordHealthcheckSuccess records a successful ping
func recordHealthcheckSuccess(ctx *AppContext, bot BotAPI) {
	var (
		shouldNotify bool
		downtimeStr  string
		totalPings   int
	)

	ctx.Monitor.Mu.Lock()
	ctx.Monitor.Healthchecks.TotalPings++
	ctx.Monitor.Healthchecks.SuccessfulPings++
	ctx.Monitor.Healthchecks.LastPingTime = time.Now()
	ctx.Monitor.Healthchecks.LastPingSuccess = true
	ctx.Monitor.Healthchecks.NetForceRebootTriggered = false // Reset watchdog flag if applicable

	// If we were in downtime, close the event and notify recovery. The
	// volatile flag is not persisted, so an open event in the downtime log
	// counts too: otherwise a process restart leaves the event open forever
	// and the next failure appends a second overlapping one.
	if (ctx.Monitor.HealthInDowntime || downtimeOpenLocked(&ctx.Monitor.Healthchecks)) && len(ctx.Monitor.Healthchecks.DowntimeEvents) > 0 {
		lastIdx := len(ctx.Monitor.Healthchecks.DowntimeEvents) - 1
		event := &ctx.Monitor.Healthchecks.DowntimeEvents[lastIdx]
		event.EndTime = time.Now()
		downtimeDuration := event.EndTime.Sub(event.StartTime)
		event.Duration = format.FormatDuration(downtimeDuration)
		ctx.Monitor.HealthInDowntime = false
		downtimeStr = event.Duration

		slog.Warn("Healthchecks: downtime ended", "duration", event.Duration)
		ctx.State.AddEvent("info", fmt.Sprintf("🟢 Healthchecks recovered (down for %s)", downtimeStr))

		shouldNotify = true
	}

	totalPings = ctx.Monitor.Healthchecks.TotalPings
	ctx.Monitor.Mu.Unlock()

	// Send recovery notification
	if shouldNotify {
		if bot != nil && !ctx.IsQuietHours() {
			cfg := ctx.Cfg()
			period := cfg.Healthchecks.PeriodSeconds
			if period <= 0 {
				period = 60
			}
			// Localized: the period goes inside a translated message, so the
			// English-only formatter put "1 minute" in an Italian sentence.
			periodStr := format.FormatPeriodL(period, ctx.Tr)

			msg := fmt.Sprintf(ctx.Tr("hc_up_alert"),
				downtimeStr,
				totalPings,
				periodStr)
			m := tgbotapi.NewMessage(cfg.AllowedUserID, msg)
			m.ParseMode = "Markdown"
			safeSend(bot, m)
		}
	}

	// Save state periodically (every 10 pings)
	if (totalPings%10 == 0) || shouldNotify {
		goSafe("save-state-healthcheck", func() { saveState(ctx) })
	}
}

// recordHealthcheckFailure records a failed ping
func recordHealthcheckFailure(ctx *AppContext, bot BotAPI, reason string) {
	var shouldForceReboot bool
	var rebootGate string
	var downtimeStr string
	forceAfter := healthchecksForceRebootAfter(ctx.Cfg())

	ctx.Monitor.Mu.Lock()

	// "Last success" va letto PRIMA che LastPingTime venga sovrascritto:
	// rileggendolo dopo, l'avviso di downtime riportava sempre ~0s.
	prevLastPing := ctx.Monitor.Healthchecks.LastPingTime
	prevPingOK := ctx.Monitor.Healthchecks.LastPingSuccess

	ctx.Monitor.Healthchecks.TotalPings++
	ctx.Monitor.Healthchecks.FailedPings++
	ctx.Monitor.Healthchecks.LastPingTime = time.Now()
	ctx.Monitor.Healthchecks.LastPingSuccess = false
	ctx.Monitor.Healthchecks.LastFailure = time.Now()

	slog.Error("Healthchecks ping failed", "reason", reason)

	// Start a new downtime event if we weren't already in one. The persisted
	// downtime log counts as evidence, because HealthInDowntime is volatile.
	inDowntime := ctx.Monitor.HealthInDowntime || downtimeOpenLocked(&ctx.Monitor.Healthchecks)

	if !inDowntime {
		ctx.Monitor.HealthInDowntime = true
		event := DowntimeLog{
			StartTime: time.Now(),
			Reason:    reason,
		}
		ctx.Monitor.Healthchecks.DowntimeEvents = append(ctx.Monitor.Healthchecks.DowntimeEvents, event)

		// Keep only the last N events
		if len(ctx.Monitor.Healthchecks.DowntimeEvents) > MaxDowntimeEvents {
			ctx.Monitor.Healthchecks.DowntimeEvents = ctx.Monitor.Healthchecks.DowntimeEvents[len(ctx.Monitor.Healthchecks.DowntimeEvents)-MaxDowntimeEvents:]
		}

		totalPings := ctx.Monitor.Healthchecks.TotalPings

		ctx.Monitor.Mu.Unlock() // Unlock before sending message to avoid deadlock if network is slow

		// Persist the event in both the log and the state file.
		ctx.State.AddEvent("warning", fmt.Sprintf("🔴 Healthchecks down: %s", reason))

		// Notify user (respecting quiet hours)
		if bot != nil && !ctx.IsQuietHours() {
			cfg := ctx.Cfg()
			period := cfg.Healthchecks.PeriodSeconds
			if period <= 0 {
				period = 60
			}
			periodStr := format.FormatPeriodL(period, ctx.Tr)

			// Calculate time since the last successful ping
			lastPingAgo := "N/A"
			if prevPingOK && !prevLastPing.IsZero() {
				lastPingAgo = format.FormatDuration(time.Since(prevLastPing))
			}

			msg := fmt.Sprintf(ctx.Tr("hc_down_alert"),
				reason,
				periodStr,
				totalPings,
				lastPingAgo)
			m := tgbotapi.NewMessage(cfg.AllowedUserID, msg)
			m.ParseMode = "Markdown"
			safeSend(bot, m)
		}
	} else {
		// ALREADY IN DOWNTIME - check the forced reboot gates.
		ctx.Monitor.HealthInDowntime = true
		if len(ctx.Monitor.Healthchecks.DowntimeEvents) > 0 {
			lastIdx := len(ctx.Monitor.Healthchecks.DowntimeEvents) - 1
			downFor := time.Since(ctx.Monitor.Healthchecks.DowntimeEvents[lastIdx].StartTime)
			downtimeStr = format.FormatDuration(downFor)

			if allowed, why := forcedRebootAllowedLocked(&ctx.Monitor.Healthchecks, downFor, forceAfter); allowed {
				// The event opened here is the one sinceForcedRebootLocked
				// looks for, and it is persisted with the rest of
				// DowntimeEvents: the "already rebooted" evidence therefore
				// survives the very reboot it is about to cause.
				rebootOpenedEventLocked(&ctx.Monitor.Healthchecks, reason)
				ctx.Monitor.Healthchecks.NetForceRebootTriggered = true
				shouldForceReboot = true
				rebootGate = why
			} else if forceAfter > 0 && downFor >= forceAfter {
				// Logged only when a reboot was actually withheld. This branch
				// is what stops a reboot loop, so if it ever fires
				// unexpectedly the log is the only place it shows up.
				slog.Warn("[Healthchecks] forced reboot withheld",
					"why", why, "down_for", downtimeStr)
			}
		}
		ctx.Monitor.Mu.Unlock()
	}

	goSafe("save-state-healthcheck-fail", func() { saveState(ctx) })

	if shouldForceReboot {
		chatID := ctx.Cfg().AllowedUserID

		// Announced BEFORE the reboot, and NOT behind the quiet hours: this is
		// a terminal action on the user's machine, so it has to be announced
		// even at 3am. A reboot the user never heard about is
		// indistinguishable from a crash, which is the opposite of what a
		// notification is for. The ordinary healthcheck messages keep
		// respecting quiet hours.
		msg := fmt.Sprintf(ctx.Tr("hc_force_reboot_alert"), downtimeStr)
		m := tgbotapi.NewMessage(chatID, msg)
		m.ParseMode = "Markdown"
		safeSend(bot, m)

		// Logged after the notification, so the log records the order too: if
		// safeSend stalls on a dead network the machine still reboots, and
		// the log must show the reboot was decided and announced.
		slog.Error("Healthchecks silent past the configured timeout, triggering forced reboot",
			"down_for", downtimeStr, "threshold", forceAfter, "gate", rebootGate)

		ctx.State.AddEvent("critical", fmt.Sprintf("Forced reboot triggered: Healthchecks down for %s", downtimeStr))
		saveState(ctx)

		time.Sleep(1 * time.Second) // gives telegram time to send
		executeForcedReboot(ctx, bot, chatID, 0, "healthchecks-down-timeout")
	}
}

// getHealthchecksPeriodRate calculates the success rate for the current report period
func getHealthchecksPeriodRate(hc HealthchecksState) (rate float64, periodTotal int) {
	periodTotal = hc.TotalPings - hc.ReportBaseTotal
	periodSuccess := hc.SuccessfulPings - hc.ReportBaseSuccessful

	// If no pings occurred in this period, assume 100% healthy based on last status
	if periodTotal <= 0 {
		if hc.LastPingSuccess {
			return 100.0, 0
		}
		return 0.0, 0
	}

	return float64(periodSuccess) / float64(periodTotal) * 100, periodTotal
}

// getHealthchecksStats returns formatted stats for the /health command
func getHealthchecksStats(ctx *AppContext) string {
	hc := ctx.Cfg().Healthchecks

	ctx.Monitor.Mu.Lock()
	defer ctx.Monitor.Mu.Unlock()

	if !hc.Enabled {
		return ctx.Tr("health_disabled") + buildWatchdogStatusLocked(ctx)
	}

	if hc.PingURL == "" {
		return ctx.Tr("health_no_url") + buildWatchdogStatusLocked(ctx)
	}

	var sb strings.Builder
	sb.WriteString(ctx.Tr("health_title"))

	// Current status
	if ctx.Monitor.Healthchecks.LastPingSuccess {
		sb.WriteString("✅ " + ctx.Tr("health_status_ok") + "\n\n")
	} else {
		sb.WriteString("❌ " + ctx.Tr("health_status_fail") + "\n\n")
	}

	// Stats
	total := ctx.Monitor.Healthchecks.TotalPings
	success := ctx.Monitor.Healthchecks.SuccessfulPings
	failed := ctx.Monitor.Healthchecks.FailedPings

	if total > 0 {
		successRate := float64(success) / float64(total) * 100
		sb.WriteString(fmt.Sprintf(ctx.Tr("health_stats_fmt"), total, success, failed, successRate))
	} else {
		sb.WriteString(ctx.Tr("health_no_data"))
	}

	// Last ping
	if !ctx.Monitor.Healthchecks.LastPingTime.IsZero() {
		ago := time.Since(ctx.Monitor.Healthchecks.LastPingTime)
		sb.WriteString(fmt.Sprintf(ctx.Tr("health_last_ping"), format.FormatDuration(ago)))
	}

	// Configuration
	period := hc.PeriodSeconds
	if period <= 0 {
		period = 60
	}
	grace := hc.GraceSeconds
	if grace <= 0 {
		grace = 60
	}
	sb.WriteString(fmt.Sprintf(ctx.Tr("health_config_fmt"), period, grace))
	sb.WriteString(buildWatchdogStatusLocked(ctx))

	// Recent downtime events
	if len(ctx.Monitor.Healthchecks.DowntimeEvents) > 0 {
		sb.WriteString("\n" + ctx.Tr("health_downtime_title"))
		// Show last 5 events
		start := len(ctx.Monitor.Healthchecks.DowntimeEvents) - 5
		if start < 0 {
			start = 0
		}
		for i := len(ctx.Monitor.Healthchecks.DowntimeEvents) - 1; i >= start; i-- {
			event := ctx.Monitor.Healthchecks.DowntimeEvents[i]
			startStr := event.StartTime.In(ctx.State.TimeLocation).Format("02/01 15:04")
			if event.EndTime.IsZero() {
				sb.WriteString(fmt.Sprintf("• `%s` — _%s_ (ongoing)\n", startStr, event.Reason))
			} else {
				sb.WriteString(fmt.Sprintf("• `%s` — %s (%s)\n", startStr, event.Duration, event.Reason))
			}
		}
	}

	return sb.String()
}

func buildWatchdogStatusLocked(ctx *AppContext) string {
	netStatus := ctx.Tr("health_watchdogs_ok")
	if ctx.Monitor.NetConsecutiveDegraded > 0 || ctx.Monitor.NetFailCount > 0 {
		netStatus = ctx.Tr("health_watchdogs_warn")
	}

	kwStatus := ctx.Tr("health_watchdogs_ok")
	if ctx.Monitor.KwConsecutiveCheckErrors > 0 {
		kwStatus = ctx.Tr("health_watchdogs_err")
	}

	netAgo := ctx.Tr("health_watchdogs_never")
	if !ctx.Monitor.NetLastCheckTime.IsZero() {
		netAgo = format.FormatDuration(time.Since(ctx.Monitor.NetLastCheckTime))
	}

	kwAgo := ctx.Tr("health_watchdogs_never")
	if !ctx.Monitor.KwLastCheckTime.IsZero() {
		kwAgo = format.FormatDuration(time.Since(ctx.Monitor.KwLastCheckTime))
	}

	var b strings.Builder
	b.WriteString("\n" + ctx.Tr("health_watchdogs_title"))
	b.WriteString(fmt.Sprintf(ctx.Tr("health_watchdogs_network"), netStatus, netAgo, ctx.Monitor.NetConsecutiveDegraded, ctx.Monitor.NetFailCount))
	b.WriteString(fmt.Sprintf(ctx.Tr("health_watchdogs_kernel"), kwStatus, kwAgo, ctx.Monitor.KwConsecutiveCheckErrors))

	if ctx.Monitor.KwLastCheckError != "" {
		errText := strings.ReplaceAll(ctx.Monitor.KwLastCheckError, "`", "'")
		if len(errText) > 140 {
			errText = errText[:140] + "..."
		}
		b.WriteString(fmt.Sprintf(ctx.Tr("health_watchdogs_last_error"), errText))
	}

	return b.String()
}

// getHealthchecksAISummary generates an AI summary of downtime patterns
func getHealthchecksAISummary(ctx *AppContext) string {
	ctx.Monitor.Mu.Lock()
	defer ctx.Monitor.Mu.Unlock()

	if len(ctx.Monitor.Healthchecks.DowntimeEvents) == 0 {
		return ctx.Tr("health_ai_no_data")
	}

	// Build context for AI
	var sb strings.Builder
	sb.WriteString("Healthchecks.io monitoring data:\n")
	sb.WriteString(fmt.Sprintf("- Total pings: %d\n", ctx.Monitor.Healthchecks.TotalPings))
	sb.WriteString(fmt.Sprintf("- Successful: %d\n", ctx.Monitor.Healthchecks.SuccessfulPings))
	sb.WriteString(fmt.Sprintf("- Failed: %d\n", ctx.Monitor.Healthchecks.FailedPings))
	sb.WriteString(fmt.Sprintf("- Success rate: %.1f%%\n", float64(ctx.Monitor.Healthchecks.SuccessfulPings)/float64(max(ctx.Monitor.Healthchecks.TotalPings, 1))*100))
	sb.WriteString("\nDowntime events:\n")

	for _, event := range ctx.Monitor.Healthchecks.DowntimeEvents {
		startStr := event.StartTime.In(ctx.State.TimeLocation).Format("2006-01-02 15:04")
		if event.EndTime.IsZero() {
			sb.WriteString(fmt.Sprintf("- %s: %s (ongoing)\n", startStr, event.Reason))
		} else {
			sb.WriteString(fmt.Sprintf("- %s: %s, duration %s\n", startStr, event.Reason, event.Duration))
		}
	}

	return sb.String()
}

// handleHealthCommand handles the /health command
func handleHealthCommand(ctx *AppContext, bot BotAPI, chatID int64) {
	cfg := ctx.Cfg()
	stats := getHealthchecksStats(ctx)

	// Create inline keyboard
	var keyboard tgbotapi.InlineKeyboardMarkup

	ctx.Monitor.Mu.Lock()
	hasEvents := len(ctx.Monitor.Healthchecks.DowntimeEvents) > 0
	ctx.Monitor.Mu.Unlock()

	if cfg.Healthchecks.Enabled && cfg.GeminiAPIKey != "" && hasEvents {
		keyboard = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🤖 "+ctx.Tr("health_ai_analyze"), "health_ai"),
				tgbotapi.NewInlineKeyboardButtonData("🔄 "+ctx.Tr("health_refresh"), "health_refresh"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🧹 "+ctx.Tr("health_clear_history"), "health_clear"),
			),
		)
	} else if cfg.Healthchecks.Enabled {
		keyboard = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🔄 "+ctx.Tr("health_refresh"), "health_refresh"),
			),
		)
	}

	msg := tgbotapi.NewMessage(chatID, stats)
	msg.ParseMode = "Markdown"
	if cfg.Healthchecks.Enabled {
		msg.ReplyMarkup = keyboard
	}
	safeSend(bot, msg)
}

// handleHealthCallback handles callback queries for health buttons
func handleHealthCallback(ctx *AppContext, bot BotAPI, query *tgbotapi.CallbackQuery, action string) {
	cfg := ctx.Cfg()
	chatID := query.Message.Chat.ID
	msgID := query.Message.MessageID

	switch action {
	case "health_refresh":
		stats := getHealthchecksStats(ctx)
		edit := tgbotapi.NewEditMessageText(chatID, msgID, stats)
		edit.ParseMode = "Markdown"

		ctx.Monitor.Mu.Lock()
		hasEvents := len(ctx.Monitor.Healthchecks.DowntimeEvents) > 0
		ctx.Monitor.Mu.Unlock()

		var keyboard tgbotapi.InlineKeyboardMarkup
		if cfg.GeminiAPIKey != "" && hasEvents {
			keyboard = tgbotapi.NewInlineKeyboardMarkup(
				tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButtonData("🤖 "+ctx.Tr("health_ai_analyze"), "health_ai"),
					tgbotapi.NewInlineKeyboardButtonData("🔄 "+ctx.Tr("health_refresh"), "health_refresh"),
				),
				tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButtonData("🧹 "+ctx.Tr("health_clear_history"), "health_clear"),
				),
			)
		} else {
			keyboard = tgbotapi.NewInlineKeyboardMarkup(
				tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButtonData("🔄 "+ctx.Tr("health_refresh"), "health_refresh"),
				),
			)
		}
		edit.ReplyMarkup = &keyboard
		safeSend(bot, edit)

	case "health_ai":
		if cfg.GeminiAPIKey == "" {
			cb := tgbotapi.NewCallback(query.ID, ctx.Tr("health_no_gemini"))
			safeSend(bot, cb)
			return
		}

		// Show loading
		modelName := "gemini-3.1-flash-lite"
		loadingText := fmt.Sprintf("⏳ %s\n_(%s)_", ctx.Tr("health_analyzing"), modelName)
		loadingMsg := tgbotapi.NewEditMessageText(chatID, msgID, loadingText)
		loadingMsg.ParseMode = "Markdown"
		safeSend(bot, loadingMsg)

		// Get AI analysis
		aiContext := getHealthchecksAISummary(ctx)
		prompt := fmt.Sprintf(ctx.Tr("health_ai_prompt"), aiContext)

		analysis, err := callGeminiWithFallback(ctx, prompt, func(model string) {
			// Update the loading message with the current model
			newText := fmt.Sprintf("⏳ %s\n_(%s)_", ctx.Tr("health_analyzing"), model)
			edit := tgbotapi.NewEditMessageText(chatID, msgID, newText)
			edit.ParseMode = "Markdown"
			safeSend(bot, edit)
		})
		if err != nil {
			slog.Error("Healthchecks AI error", "err", err)
			errText := fmt.Sprintf("❌ %s\n\n_Error: %v_", ctx.Tr("health_ai_error"), sanitizeErr(err))
			edit := tgbotapi.NewEditMessageText(chatID, msgID, errText)
			edit.ParseMode = "Markdown"
			keyboard := tgbotapi.NewInlineKeyboardMarkup(
				tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButtonData("🔄 "+ctx.Tr("health_ai_analyze"), "health_ai"),
					tgbotapi.NewInlineKeyboardButtonData("⬅️ "+ctx.Tr("back"), "health_refresh"),
				),
			)
			edit.ReplyMarkup = &keyboard
			if _, sendErr := bot.Send(edit); sendErr != nil {
				edit.ParseMode = ""
				safeSend(bot, edit)
			}
			return
		}

		// Show analysis
		result := fmt.Sprintf("🤖 *%s*\n\n%s", ctx.Tr("health_ai_title"), analysis)
		edit := tgbotapi.NewEditMessageText(chatID, msgID, result)
		edit.ParseMode = "Markdown"
		keyboard := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("⬅️ "+ctx.Tr("back"), "health_refresh"),
			),
		)
		edit.ReplyMarkup = &keyboard
		if _, sendErr := bot.Send(edit); sendErr != nil {
			slog.Error("Error sending AI analysis (Markdown)", "err", sanitizeErr(sendErr))
			edit.ParseMode = ""
			safeSend(bot, edit)
		}

	case "health_clear":
		ctx.Monitor.Mu.Lock()
		ctx.Monitor.Healthchecks.DowntimeEvents = []DowntimeLog{}
		ctx.Monitor.Healthchecks.TotalPings = 0
		ctx.Monitor.Healthchecks.SuccessfulPings = 0
		ctx.Monitor.Healthchecks.FailedPings = 0
		// The in-flight downtime is over as far as the user is concerned: the
		// cleared log is the evidence downtimeOpenLocked looks at, so without
		// this the next failed ping would resume the old event instead of
		// opening a new one.
		ctx.Monitor.HealthInDowntime = false
		ctx.Monitor.Mu.Unlock()
		saveState(ctx)

		stats := getHealthchecksStats(ctx)
		edit := tgbotapi.NewEditMessageText(chatID, msgID, stats+"\n\n✅ "+ctx.Tr("health_history_cleared"))
		edit.ParseMode = "Markdown"
		keyboard := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🔄 "+ctx.Tr("health_refresh"), "health_refresh"),
			),
		)
		edit.ReplyMarkup = &keyboard
		safeSend(bot, edit)
	}

	// Answer callback to remove loading indicator
	safeSend(bot, tgbotapi.NewCallback(query.ID, ""))
}

// pingHealthchecksStart sends a /start signal to healthchecks.io
func pingHealthchecksStart(ctx *AppContext) {
	pingURL := ctx.Cfg().Healthchecks.PingURL
	if !ctx.Cfg().Healthchecks.Enabled || pingURL == "" {
		return
	}

	url := pingURL + "/start"
	timeoutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(timeoutCtx, http.MethodGet, url, nil)
	if err != nil {
		return
	}

	if ctx.HTTP == nil {
		ctx.HTTP = &http.Client{Timeout: 10 * time.Second}
	}

	resp, err := ctx.HTTP.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	slog.Info("Healthchecks.io /start signal sent")
}

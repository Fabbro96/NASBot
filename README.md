# 🖥️ NASBot

> Un bot Telegram leggero e reattivo per tenere sotto controllo il tuo NAS — ovunque tu sia.

![Go](https://img.shields.io/badge/Go-1.18+-00ADD8?logo=go&logoColor=white)
![Platform](https://img.shields.io/badge/Platform-Linux%20ARM64-orange)
![License](https://img.shields.io/badge/License-MIT-green)

---

## ✨ Perché NASBot?

Hai un NAS casalingo o un mini-server ARM e vuoi sapere come sta **senza aprire SSH ogni volta**?  
NASBot ti manda una dashboard interattiva su Telegram: CPU, RAM, dischi, container Docker, temperature — tutto a portata di tap.

**Caratteristiche principali:**

| | |
|---|---|
| 📊 **Dashboard live** | Pulsanti inline per aggiornare al volo |
| 🌅 **Report mattina** | Ogni giorno alle 07:30 con "Buongiorno!" |
| 🌆 **Report sera** | Ogni giorno alle 18:30 con "Buonasera!" |
| 🛡️ **Protezione autonoma** | Riavvia container se RAM critica |
| 🐳 **Gestione Docker** | Start/Stop/Restart container da Telegram |
| 🔔 **Allarmi intelligenti** | Notifica solo stress I/O prolungato (2+ min) |
| 🔄 **Auto-recovery** | Riavvio automatico dopo crash/reboot |
| 🔒 **Accesso singolo** | Solo il tuo user ID può comandare |
| 🪶 **Leggero** | Binario statico ~6 MB, zero dipendenze runtime |

---

## 📋 Requisiti

| Requisito | Note |
|-----------|------|
| **Go ≥ 1.18** | Solo se compili da sorgente |
| **Linux** | Testato su Debian/Ubuntu ARM64, TerraMaster |
| `docker` *(opzionale)* | Per gestione container |
| `smartmontools` *(opzionale)* | Per temperature SMART (`/temp`) |

### ⚠️ Permessi

- `/reboot` e `/shutdown` eseguono direttamente `reboot`/`poweroff` → il processo deve girare come **root** o avere i permessi necessari.
- `smartctl` di solito richiede **root** o appartenenza al gruppo `disk`.
- Per la gestione Docker, l'utente deve poter eseguire comandi `docker`.

---

## ⚙️ Configurazione

Il bot legge due variabili d'ambiente **obbligatorie**:

| Variabile | Descrizione |
|-----------|-------------|
| `BOT_TOKEN` | Token rilasciato da [@BotFather](https://t.me/BotFather) |
| `BOT_USER_ID` | Il tuo chat ID numerico (puoi ottenerlo da [@userinfobot](https://t.me/userinfobot)) |

```bash
export BOT_TOKEN="123456:ABC-xyz..."
export BOT_USER_ID="123456789"
```

> 💡 **Tip:** non committare mai il token nel repo! Usa un file `.env` ignorato da git oppure variabili di sistema.

---

## 🚀 Avvio

### Opzione A — Script di gestione (consigliato)

```bash
./start_bot.sh start     # Avvia
./start_bot.sh stop      # Ferma
./start_bot.sh restart   # Riavvia
./start_bot.sh status    # Stato dettagliato
./start_bot.sh logs 100  # Ultimi 100 log
```

### Opzione B — Avvio automatico (auto-recovery)

Per TerraMaster e sistemi senza systemd:
```bash
sudo ./setup_autostart.sh
```

Per sistemi con systemd:
```bash
sudo cp nasbot.service /etc/systemd/system/
# Modifica BOT_TOKEN e BOT_USER_ID nel file
sudo systemctl daemon-reload
sudo systemctl enable nasbot
sudo systemctl start nasbot
```

### Opzione C — Compila e lancia manualmente

```bash
go build -o nasbot .
./nasbot
```

---

## 🤖 Comandi Telegram

| Comando | Descrizione |
| --- | --- |
| `/status` | 📊 Dashboard risorse interattiva |
| `/report` | 📋 Report completo stato NAS |
| `/temp` | 🌡 Temperature CPU e Dischi (SMART) |
| `/docker` | 🐳 Menu gestione Container |
| `/dstats` | 📈 Consumo risorse Container |
| `/container <nome>` | 🔍 Info container specifico |
| `/net` | 🌐 Info IP Locale e Pubblico |
| `/speedtest` | 🚀 Test velocità connessione |
| `/logs` | 📜 Ultimi log di sistema (dmesg) |
| `/reboot` | 🔄 Riavvia il NAS |
| `/shutdown` | 🛑 Spegni il NAS |
| `/help` | ❓ Guida comandi |

> `/start` è un alias di `/status`.

---

## 🛠️ Script di avvio (`start_box.sh`)

Uno script pronto per avviare (o fermare) il bot, con controllo anti-duplicato e un po' di colore:

```bash
#!/bin/bash
# ============================================================
#  NASBot Launcher — start | stop | status
# ============================================================

# --- CONFIGURAZIONE (sostituisci con i tuoi valori) ---------
export BOT_TOKEN="IL_TUO_TOKEN"
export BOT_USER_ID="IL_TUO_USER_ID"
BOT_DIR="/Volume1/public"
# ------------------------------------------------------------

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'

cd "$BOT_DIR" || { echo -e "${RED}✗ Directory $BOT_DIR non trovata${NC}"; exit 1; }

case "${1:-start}" in
  start)
    if pgrep -x "nasbot" > /dev/null; then
      echo -e "${YELLOW}⚡ NASBot già in esecuzione (PID $(pgrep -x nasbot))${NC}"
    else
      [[ -z "$BOT_TOKEN" || -z "$BOT_USER_ID" ]] && { echo -e "${RED}✗ BOT_TOKEN o BOT_USER_ID mancanti${NC}"; exit 1; }
      nohup ./nasbot >> nasbot.log 2>&1 &
      sleep 1
      if pgrep -x "nasbot" > /dev/null; then
        echo -e "${GREEN}✔ NASBot avviato (PID $(pgrep -x nasbot))${NC}"
      else
        echo -e "${RED}✗ Avvio fallito — controlla nasbot.log${NC}"
      fi
    fi
    ;;
  stop)
    if pgrep -x "nasbot" > /dev/null; then
      pkill -x "nasbot"
      echo -e "${GREEN}✔ NASBot fermato${NC}"
    else
      echo -e "${YELLOW}⚠ NASBot non era in esecuzione${NC}"
    fi
    ;;
  status)
    if pgrep -x "nasbot" > /dev/null; then
      echo -e "${GREEN}● NASBot attivo (PID $(pgrep -x nasbot))${NC}"
    else
      echo -e "${RED}○ NASBot non attivo${NC}"
    fi
    ;;
  *)
    echo "Uso: $0 {start|stop|status}"
    exit 1
    ;;
esac
```

```bash
chmod +x start_box.sh
./start_box.sh start   # avvia
./start_box.sh status  # controlla
./start_box.sh stop    # ferma
```

> 💡 **Tip:** aggiungi `@reboot /percorso/start_box.sh start` al crontab per l'avvio automatico al boot.

---

## 🔧 Personalizzazione

Nel codice (`main.go`) trovi alcune costanti che puoi modificare:

```go
const (
    SogliaCPU      = 90.0       // % CPU per allarme
    SogliaRAM      = 90.0       // % RAM per allarme
    PathSSD        = "/Volume1" // mount point SSD
    PathHDD        = "/Volume2" // mount point HDD
    CooldownMinuti = 20         // minuti tra un allarme e l'altro
)
```

Dopo le modifiche: `go build -o nasbot .`

---

## 🐛 Troubleshooting

| Problema | Soluzione |
|----------|-----------|
| *"BOT_TOKEN mancante"* | Controlla che le variabili siano esportate nella shell che lancia il bot |
| *Temperature disco "??"* | Installa `smartmontools` e verifica i permessi (`sudo smartctl ...`) |
| *Comandi Docker falliscono* | Assicurati che l'utente che esegue il bot sia nel gruppo `docker` |
| *Il bot non risponde* | Verifica che `BOT_USER_ID` corrisponda al tuo chat ID |

---

## 📜 Licenza

MIT — usalo, modificalo, divertiti. 🎉

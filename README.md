# NASBot (Telegram)

Bot Telegram per monitorare un NAS (CPU/RAM/SWAP, spazio dischi, I/O, processi top) e comandare alcune azioni di sistema.

Il codice è in `main.go` (Go). Nel repository è presente anche un binario precompilato `nasbot` (ARM64).

## Funzionalità

- Dashboard con pulsanti inline (refresh status, temp, docker, docker stats)
- Comandi Telegram per status e diagnostica (rete, speedtest, logs)
- Monitoraggio in background con allarme (CPU alta o I/O “bloccato”) con cooldown
- Accesso limitato a un singolo utente (chat id)

## Requisiti

- Go >= 1.18 (se compili da sorgente)
- Linux (il codice legge anche `/sys/class/thermal/...` e usa comandi di sistema)
- Facoltativi ma consigliati:
  - `docker` (per `/docker` e `/dstats`)
  - `smartctl` (pacchetto `smartmontools`) per `/temp` (lettura SMART dischi)

Nota permessi:
- `/reboot` e `/shutdown` eseguono `reboot`/`poweroff` direttamente: il processo deve avere i privilegi necessari.
- `smartctl` spesso richiede root o capability (`sudo`, group `disk`, oppure policy ad-hoc).

## Configurazione

Il bot usa variabili d’ambiente (obbligatorie):

- `BOT_TOKEN`: token del bot Telegram
- `BOT_USER_ID`: chat id numerico autorizzato (gli altri messaggi vengono ignorati)

Esempio:

```bash
export BOT_TOKEN="123456:ABC..."
export BOT_USER_ID="123456789"
```

## Avvio

### 1) Esegui da sorgente

```bash
go run .
```

### 2) Compila e avvia

```bash
go build -o nasbot .
./nasbot
```

### 3) Usa il binario incluso (ARM64)

Nel repo c’è un eseguibile `nasbot` per `linux/arm64`:

```bash
./nasbot
```

Se la tua macchina NON è ARM64, compila da sorgente oppure ricompila per la tua architettura.

## Comandi Telegram

| Comando | Descrizione |
| --- | --- |
| `/status` | 📊 Dashboard risorse interattiva |
| `/temp` | 🌡 Temperature CPU e Dischi (SMART) |
| `/docker` | 🐳 Stato dei Container |
| `/dstats` | 📈 Consumo risorse Container |
| `/net` | 🌐 Info IP Locale e Pubblico |
| `/speedtest` | 🚀 Test velocità connessione |
| `/logs` | 📜 Ultimi log di sistema (dmesg) |
| `/reboot` | 🔄 Riavvia il NAS |
| `/shutdown` | 🛑 Spegni il NAS |
| `/help` | ❓ Guida comandi |

Nota: `/start` è gestito come alias di `/status`.

## Avvio automatico (esempio `start_box.sh`)

Esempio di script per esportare le variabili d’ambiente ed evitare doppi avvii:

```bash
#!/bin/bash

# --- CONFIGURAZIONE ---
export BOT_TOKEN="TOKEN"
export BOT_USER_ID="USER"
# ----------------------

cd /Volume1/public/

# Evita doppi avvii
if pgrep -x "nasbot" > /dev/null
then
  echo "Bot già attivo."
else
  # Avvio silenzioso in background
  nohup ./nasbot > /dev/null 2>&1 &
  echo "Bot avviato."
fi
```

Ricordati di rendere lo script eseguibile (`chmod +x start_box.sh`) e di sostituire `TOKEN`/`USER` con valori reali.

## Note di configurazione dischi

Nel codice ci sono costanti per i mountpoint:

- `PathSSD = "/Volume1"`
- `PathHDD = "/Volume2"`

Se nel tuo NAS i path sono diversi, aggiorna `main.go` e ricompila.

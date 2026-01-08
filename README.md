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

- `/start` o `/status`: stato sistema + top processi CPU/RAM
- `/temp`: temperature (CPU + SMART dischi `sda`, `sdb`)
- `/docker`: elenco container
- `/dstats`: risorse container (`docker stats`)
- `/net`: IP locale + IP pubblico
- `/speedtest`: download test (100MB) e stima Mbps
- `/logs`: ultimi 10 log kernel (`dmesg | tail -n 10`)
- `/reboot`: reboot host (richiede permessi)
- `/shutdown`: spegnimento host (richiede permessi)
- `/help`: help sintetico

## Note di configurazione dischi

Nel codice ci sono costanti per i mountpoint:

- `PathSSD = "/Volume1"`
- `PathHDD = "/Volume2"`

Se nel tuo NAS i path sono diversi, aggiorna `main.go` e ricompila.

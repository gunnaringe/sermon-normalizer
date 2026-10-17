# talenorm

CLI som behandler tale-opptak (tale + musikk) til publiseringsklare filer.
Se `../PLAN.md` for bakgrunn og språkvalg.

## Krav

- `ffmpeg` i PATH (Linux: `sudo apt install ffmpeg`, Windows: `winget install Gyan.FFmpeg`)
- Go 1.22+ for å bygge (ingen eksterne Go-avhengigheter)

## Bygg

```bash
go build -o talenorm .                                   # for denne maskinen
GOOS=linux GOARCH=amd64 go build -o dist/talenorm-linux-amd64 .   # for Linux x86-64
GOOS=linux GOARCH=arm64 go build -o dist/talenorm-linux-arm64 .   # for Linux ARM
go test ./...
```

PowerShell: sett `$env:GOOS="linux"; $env:GOARCH="amd64"` før `go build`.

## Docker (ffmpeg følger med)

ffmpeg er et eget C-program og kan ikke bygges inn i Go-binæren, så containeren pakker begge sammen.

```bash
docker build -t talenorm .
docker run --rm -u "$(id -u):$(id -g)" -v /sti/til/Taler:/data talenorm run /data/2026/2026-10-04
```

`-u` gjør at filene som lages får din bruker som eier, ikke root.
Bygget kjører `go vet` og `go test`, og sjekker at ffmpeg har libopus og libmp3lame.

## Bruk

Legg opptaket av hele gudstjenesten i datomappen, og oppgi hvor talen starter og slutter:

```
2026-10-04/
  2026-10-04_raw.wav          (eller _raw.flac)    hele opptaket
```

```bash
talenorm run /sti/til/Taler/2026/2026-10-04 --start 41:20 --end 1:12:05
```

Tidene kan skrives som `t:mm:ss`, `mm:ss` eller sekunder, med desimaler om nødvendig (`41:20.5`).
Uten `--start` klippes det fra begynnelsen, uten `--end` til slutten.
Verktøyet klipper `arkiv/<dato>_edit.flac` fra raw og fortsetter med master, MP3 og Opus.
For å klippe på nytt med andre tider, kjør igjen med `--force`.

**Mer enn ett stykke** (for eksempel en sang midt i talen som skal bort): klipp selv i et
lydprogram og legg resultatet i mappen. Da brukes det i stedet, og `--start`/`--end` utelates
(begge samtidig gir feil). WAV og FLAC godtas:

```
2026-10-04/
  2026-10-04_raw.wav          (eller _raw.flac)    hele opptaket
  2026-10-04_edit.flac        (eller _edit.wav)    klippet av deg
```

```bash
talenorm run /sti/til/Taler/2026/2026-10-04
```

Resultat:

```
2026-10-04/
  2026-10-04.flac      master: mono, 48 kHz / 24-bit, -19 LUFS
  2026-10-04.mp3       mono, 96 kbps
  2026-10-04.opus      mono, 48 kbps
  arkiv/
    2026-10-04_raw.flac
    2026-10-04_edit.flac
```

- WAV konverteres til FLAC i `arkiv/`, lyden verifiseres med MD5, og **WAV slettes bare hvis lyden er identisk** (med mindre `--keep-wav`).
- MP3 og Opus kodes, måles og kodes på nytt med justert gain hvis true peak > -1 dBTP eller loudness avviker.
- Eksisterende resultatfiler hoppes over uten `--force`.
- Gamle navn `_1-raw.*` og `_2-edit.*` godtas.

Flagg for `run` (kan stå før eller etter mappen):

| Flagg | Standard | |
|---|---|---|
| `--start` | | start på talen i raw (klipper edit automatisk) |
| `--end` | | slutt på talen i raw |
| `--target-lufs` | -19 | -19 for mono, -16 for stereo |
| `--true-peak` | -1.5 | maks true peak for master |
| `--mp3-bitrate` | 96k | |
| `--opus-bitrate` | 48k | |
| `--noise-reduction` | 12 | dB, 0 = av |
| `--keep-wav` | av | ikke slett WAV |
| `--force` | av | lag resultatfilene på nytt |
| `--ffmpeg` | ffmpeg | sti til ffmpeg |

Måle filer:

```bash
talenorm measure fil1.mp3 fil2.flac
```

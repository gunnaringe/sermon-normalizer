# Plan: sermon-normalizer, behandling av tale-opptak

## Mål

Ett kommandolinjeverktøy som tar et opptak av en tale (tale + musikk, ulikt volum)
og lager ferdige filer for publisering på SoundCloud, Spotify og andre podcast-tjenester.
Skal kjøre på Linux (server), men må kunne testes på Windows.

## Dagens flyt (manuell + `behandle-tale.ps1`)

1. Opptaker gir `.wav`. `sox` konverterer til `.flac`.
2. Manuell klipping i lydprogram (kun talen beholdes), normalisert midlertidig for enklere klipping.
3. `behandle-tale.ps1` lager master (mono, -19 LUFS), MP3 og Opus.

## Mappestruktur per opptak

```
<dato>\                      f.eks. 2026-10-04
  <dato>.flac                master, mono, 48 kHz / 24-bit, -19 LUFS
  <dato>.mp3                 mono, 96 kbps  (publisering)
  <dato>.opus                mono, 48 kbps
  arkiv\
    <dato>_raw.flac          hele opptaket, uredigert
    <dato>_edit.flac         klippet versjon (input til master)
```

Input som legges i mappen: `<dato>_raw.wav` (eller `_raw.flac`) og `<dato>_edit.flac`.
Gamle navn `_1-raw.*` og `_2-edit.flac` godtas.

## Språkvalg

**Anbefaling: Go.** Selve lydarbeidet gjøres av `ffmpeg` uansett språk. Koden er orkestrering:
finne filer, kalle ffmpeg, tolke måleresultat, flytte/slette filer, rapportere.

| Språk | For | Mot |
|---|---|---|
| **Go** | Én statisk binærfil, `GOOS=linux go build` fra Windows, ingen runtime eller venv på serveren, god `os/exec`, standardbibliotek dekker flagg/regex/tester | Litt mer kode enn Python |
| Python | Raskest å skrive | Krever Python + venv/pakker på serveren, vanskeligere å levere som én fil |
| Bash | Minst kode for rene ffmpeg-kall | Skjør for parsing, feilhåndtering og tester; ikke portabelt til Windows |
| Rust | Samme leveranse som Go | Mer arbeid uten gevinst her |

Go passer også siden du allerede har valgt det, og det er enkelt å utvide senere
(for eksempel overvåke en mappe eller et web-API).

## Arkitektur

Modul `github.com/gunnaringe/sermon-normalizer`, kun standardbibliotek (ingen eksterne avhengigheter).

- `main.go`: CLI (underkommandoer, flagg, Ctrl+C avbryter pent)
- `ffmpeg.go`: tynn wrapper rundt `ffmpeg` (`-nostdin`, fanger stdout og stderr)
- `measure.go`: måling med `ebur128` og parsing av sammendraget (LUFS, true peak, LRA)
- `pipeline.go`: stegene (raw, edit, master, lossy, rapport)
- `*_test.go`: tester for parsing, filnavn og gain-beregning

## CLI

```
sermon-normalizer run <mappe> [flagg]     kjør hele flyten for en datomappe
sermon-normalizer measure <fil>...        mål LUFS / true peak / LRA
sermon-normalizer version

run-flagg:
  --start / --end          klipp edit fra raw (ett stykke); ellers leveres _edit selv
  --target-lufs  -19       -19 for mono, -16 for stereo
  --true-peak    -1.5
  --mp3-bitrate  96k
  --opus-bitrate 48k
  --noise-reduction 12     0 = av
  --keep-wav               ikke slett WAV etter verifisering
  --force                  lag resultatfilene på nytt
```

## Viktige regler (fra erfaring i denne økten)

- WAV slettes **bare** etter at lyden i `raw.flac` er verifisert bit-identisk (MD5 over lydstrømmen).
- MP3 og Opus kodes, **måles og kodes på nytt med justert gain** hvis peak > -1 dBTP
  eller loudness avviker. Gain begrenses av peak-taket.
- Måling med `ebur128` (rask), ikke `loudnorm` (oppsampler til 192 kHz og er treg).
- Kall ffmpeg med argumentliste, aldri via shell (unngår quoting-feil som `-c:a` i PowerShell).
- Eksisterende resultatfiler hoppes over uten `--force`.

## Faser

1. **Port av ps1 til Go** (denne leveransen): `run` og `measure`, tester, bygg for Windows og Linux.
2. **Test på ekte opptak:** 2026-09-20 og 2026-10-04 gir samme resultat som ps1.
3. **Drift på Linux:** installer ffmpeg (`apt install ffmpeg`), kopier binæren, kjør på en mappe.
4. **Senere (valgfritt):**
   - `run --all <årsmappe>` for å behandle alle datomapper uten master
   - valgfri sox/ffmpeg-konvertering fra wav direkte i verktøyet (finnes allerede)
   - mono/stereo-automatikk med riktig mål-LUFS
   - metadata (ID3-tagger: tittel, dato, kirke) på MP3
   - overvåking av mappe (Resilio Sync) og automatisk behandling

## Risiko og åpne spørsmål

- **Opus true peak** overskyter lett; håndteres av målesteget, men vurder å sette master-peak til -2 dBTP.
- **Navngiving av tittel** i filnavn er ikke bestemt (nå kun dato).
- **Støyfjerning** (`afftdn`) er ikke lyttet til; juster `--noise-reduction` etter ørene.
- Resilio Sync kan lage `.rsls`-plassholdere; verktøyet ignorerer dem.

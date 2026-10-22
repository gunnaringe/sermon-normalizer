# What a run makes

```
2026-10-04/
  2026-10-04_raw.flac  the whole recording, untouched by runs
  2026-10-04.flac      master: mono, 48 kHz / 24-bit, -19 LUFS
  2026-10-04.mp3       mono, 96 kbps
  2026-10-04.opus      mono, 48 kbps
```

| Option | Default | |
|---|---|---|
| Outputs | FLAC, MP3, Opus | which files to make |
| Stereo | off (mono) | stereo moves the defaults to -16 LUFS, MP3 128k, Opus 96k |
| Fade in / out | 0 | seconds of fade at the start and end of the sermon, 0 = off |
| Pause | 0 | seconds of silence put in where a section ends or a cut is |
| Noise reduction | 6 dB | 0 = off |
| Room sound kept | 0 % | raises the floor the noise reduction works down to (0 to 100) |
| Leveling | normal | off, gentle, normal, strong (`dynaudnorm`) |
| Compression | off | off, light, normal, strong (`acompressor`, after the gain) |
| Target loudness | -19 LUFS | |
| Max true peak | -1.5 dBTP | for the master |
| MP3 / Opus bitrate | 96k / 48k | |

- **The master is made in steps.** First the cut, the channels, the noise reduction and the leveling go into a temporary
  file (the slow part). Its loudness is measured (EBU R128), and the gain that brings it to the target is worked out; with
  compression on, the gain is applied, the compressor runs, and the loudness is measured again. Then the gain, a peak limiter
  (half a dB under the true-peak ceiling) and the fades are applied to make the master, which is measured once more: if the
  limiter took some of the gain back, the gain is corrected, up to two times. So the master lands within 0.3 LU of the target
  unless the peaks of the recording keep it from getting louder (the run then warns: try compression).
- The fades are applied last, after the loudness processing, so they stay intact in every output.
- MP3 and Opus are encoded, measured and re-encoded with adjusted gain if true peak > -1 dBTP or
  the loudness is off. They are always made from the master; without FLAC it is a temporary
  hidden file in the date folder, removed when the run ends.
- **Intro and outro.** A recording can have a clip before and after the sermon (the church's, or its own). The
  clips are made elsewhere, often with music, so they get no noise reduction or leveling: each is brought to the target
  loudness (gain and the same limiter) and to the sermon's rate and channels, and joined to the finished sermon with a
  short pause at each join (0.5 s unless the church chooses otherwise); the sermon keeps its own fades and tags. A run
  with clips makes two versions of each output: with them (the main files, `<date>.flac/.mp3/.opus`) and the sermon
  only (`<date>_sermon.flac/.mp3/.opus`). The one with them is made from the sermon's master.
- Up-to-date outputs are kept; Save redoes those that are out of date (and cuts again). Output is written to a `.partial` file and
  renamed when done, so an interrupted run never leaves a half-written file behind.
- Mono is the usual choice for speech, and mono and stereo recordings both work either way.
  With stereo, the run warns if the source is mono or its two channels are identical or nearly
  so (difference 30 dB or more below the signal, common when a mixer sends one signal to both
  sides): stereo output would sound the same as mono in bigger files. It also warns if one
  channel is silent or 20 dB or more quieter than the other: stereo output would have the
  sermon on one side only, while mono mixes both channels. It only warns.

## The original recording

A run moves the whole recording into `original/` in the date folder, as FLAC, so the date folder
ends up with the finished files, the saved cuts and that one folder. No cut file is made: the
outputs are made straight from the original, using the sections and cuts (`recording.json`),
so there is nothing else to keep in step.

```
2026-10-04/
  2026-10-04.flac
  2026-10-04.mp3
  2026-10-04.opus
  recording.json                what the recording is, the saved and adjusted sections, cuts
                                and fades, and how each file was made
  original/
    2026-10-04_raw.flac         the whole recording
```

- The recording is moved into `original/` as it is, under the name it arrived with (`R_20261018-104506.wav` is kept as
  `R_20261018-104506.flac`; a video's sound as `.mka`; an MP3 stays `.mp3`). The name is kept in `recording.json` as `sourceName`;
  a recording that has none (from before) is `<date>_raw.<ext>`. Either is found in `original/` (`_raw.flac`, `_raw.mp3`, `_raw.mka`...); a
  WAV or video not converted yet is converted there first, and **deleted only if the audio is
  identical**. Across disks a file is copied, verified and only then deleted.
- A recording already in `original/` is never replaced by a run that only adds files; one that redoes files replaces it.

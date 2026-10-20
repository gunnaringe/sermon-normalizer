// The editor for one date folder: cut the sermon on the waveform, set the
// options, run, and follow the job.
import WaveSurfer from './vendor/wavesurfer/wavesurfer.esm.js';
import RegionsPlugin from './vendor/wavesurfer/plugins/regions.esm.js';
import TimelinePlugin from './vendor/wavesurfer/plugins/timeline.esm.js';
import MinimapPlugin from './vendor/wavesurfer/plugins/minimap.esm.js';
import HoverPlugin from './vendor/wavesurfer/plugins/hover.esm.js';
import { confirmDialog } from './dialog.js';
import { api, q, h, parseTime, formatTime, formatSize, typing, debounce, crumbs, forTenant, suggestNames, dateInput, formatDateTime, wheelZoomNeedsKey } from './util.js';

const STORE = 'sermon-studio-options';
const MAX_ZOOM = 200; // px per second: 5 ms per pixel, enough to cut between words

const css = (name) => getComputedStyle(document.documentElement).getPropertyValue(name).trim();

// describeCodec names a recording's format the way people know it.
function describeCodec(fmt) {
  if (!fmt) return '';
  const c = fmt.codec || '';
  if (c.startsWith('pcm_')) return 'WAV';
  return { flac: 'FLAC', mp3: 'MP3', opus: 'Opus', vorbis: 'Ogg Vorbis', aac: 'AAC', alac: 'ALAC' }[c] || (c || fmt.container || '').toUpperCase();
}

// describeFormat is a recording's format on one line: "FLAC · 48 kHz · 24-bit · 2 channels · 1,412 kb/s".
function describeFormat(fmt) {
  return [
    describeCodec(fmt),
    fmt.sampleRate ? `${fmt.sampleRate / 1000} kHz` : '',
    fmt.bits ? `${fmt.bits}-bit` : '',
    fmt.channels === 1 ? '1 channel' : `${fmt.channels} channels`,
    fmt.bitrate ? `${fmt.bitrate.toLocaleString('en')} kb/s` : '',
  ].filter(Boolean).join(' · ');
}

// FORMAT_KEYS are chosen per recording, so they are never carried over.
const FORMAT_KEYS = ['flac', 'mp3', 'opus'];

// savedOptions are the choices of the last run, except per-recording ones.
function savedOptions(config) {
  const opts = { ...config.defaults.mono };
  try {
    const stored = JSON.parse(localStorage.getItem(STORE) || '{}');
    for (const k of FORMAT_KEYS) delete stored[k];
    Object.assign(opts, stored);
  } catch {}
  return opts;
}

function saveOptions(opts) {
  const { sections, cuts, title, artist, album, flac, mp3, opus, ...keep } = opts;
  localStorage.setItem(STORE, JSON.stringify(keep));
}

export async function editorView(view, path, config) {
  const info = await api('GET', 'api/folder?path=' + q(path));
  let alive = true;
  const cleanups = [];
  const teardown = () => {
    alive = false;
    for (const fn of cleanups) fn();
  };

  const opts = savedOptions(config);
  // The formats are this recording's own: those it has, or the defaults when it
  // has none. Choosing here changes nothing elsewhere.
  if (info.outputs.length) for (const f of FORMAT_KEYS) opts[f] = info.outputs.some((o) => o.format === f);
  // Tags are per sermon: the church's defaults for this folder, editable for this run.
  const tags = { title: '', artist: '', album: '', ...(info.tags || {}) };
  let job = info.job;
  let regions = null; // the waveform's regions plugin: sections, cuts, silences
  const sections = []; // the parts of the recording that are the sermon: { region, row, ... }, in time order
  const cuts = []; // parts cut out of the sections: { region, row, ... }, in time order
  let sectionSeq = 0, cutSeq = 0;
  let ws = null;
  let duration = 0;
  let silenceEdges = [];
  let stopAt = null; // playback pauses here (hear start/end, play selection)
  let addCutButton = null; // disabled until there is a section
  let skipNow = false; // this playback jumps over the cuts even with "play without the cuts" off
  let lastCheck = { error: 'Checking…' };
  let events = null; // the job's event stream
  cleanups.push(() => events && events.close());

  // ---- layout ----
  const waveSection = h('section', {});
  const lengthOut = h('p', { class: 'length' });
  const sectionList = h('div', { class: 'sections' });
  const sectionHint = h('p', { class: 'muted' });
  const cutList = h('div', { class: 'cuts' });
  const cutHint = h('p', { class: 'muted' });
  const skipBox = h('input', { type: 'checkbox', checked: true });
  const inputPanel = h('div', { class: 'panel input-file' });
  const channelWarning = h('p', { class: 'notice', role: 'status' });
  const pills = h('div', { class: 'pills', role: 'group', 'aria-label': 'Formats' });
  const outputNote = h('p', { class: 'muted' });
  const checkOut = h('div', {});
  const regenerateButton = h('button', { class: 'primary', disabled: true, onclick: regenerate }, 'Regenerate');
  // The fades are part of the edit; they sit with the sections and cuts.
  const fadeInput = (key, label) => h('label', { class: 'fade' }, h('span', { class: 'name' }, label), h('input', {
    type: 'number', name: key, value: opts[key], step: 0.5, min: 0,
    oninput: (e) => { opts[key] = e.target.value === '' ? NaN : Number(e.target.value); changed(); },
  }));
  const fades = h('div', { class: 'toolbar' }, fadeInput('fadeIn', 'Fade in (s)'), fadeInput('fadeOut', 'Fade out (s)'));
  const saveNote = h('span', { class: 'muted' });
  const versionOut = h('div', {});
  const saveButton = h('button', { disabled: true, title: 'Make the current settings the saved version', onclick: saveVersion }, 'Save…');
  const revertButton = h('button', { disabled: true, title: 'Throw away the adjustments and go back to the saved version', onclick: revertVersion }, 'Revert…');
  const jobPanel = h('section', {});

  // Another date or name for this sermon: renames the folder and its files.
  const renamePanel = h('div', { class: 'panel', hidden: true });
  const renameButton = h('button', { class: 'link', title: 'Change the date or name of this sermon', onclick: () => {
    const [, d = '', n = ''] = info.date.match(/^(\d{4}-\d{2}-\d{2})(?: (.*))?$/) || [];
    const date = dateInput({ name: 'date', value: d, required: true, 'aria-label': 'Date' });
    const name = h('input', { type: 'text', value: n, maxlength: 60, placeholder: 'optional, e.g. Evening', 'aria-label': 'Name' });
    const msg = h('span', { class: 'error' });
    const save = h('button', { class: 'primary', onclick: async () => {
      try {
        const res = await api('POST', 'api/rename?path=' + q(path), { date: date.value, name: name.value });
        location.hash = '#/folder/' + res.path;
      } catch (err) { msg.textContent = err.message; }
    } }, 'Rename');
    renamePanel.replaceChildren(
      h('label', {}, 'Date ', date), ' ', h('label', {}, 'Name ', name), ' ', save, ' ',
      h('button', { onclick: () => { renamePanel.hidden = true; } }, 'Cancel'), ' ', msg,
      h('p', { class: 'muted' }, 'The folder and the files named after it are renamed. Files already made keep their tags.'));
    renamePanel.hidden = false;
    suggestNames(name);
  } }, 'Rename…');

  view.replaceChildren(
    crumbs(path.split('/').slice(0, -1).join('/'), info.date),
    h('h1', {}, info.date, ' ', renameButton),
    renamePanel,
    h('section', { class: 'step', 'aria-labelledby': 'step-input' }, h('h2', { id: 'step-input' }, 'Input file'), inputPanel),
    h('section', { class: 'step', 'aria-labelledby': 'step-edit' }, h('h2', { id: 'step-edit' }, 'Edit'), waveSection),
    h('section', { class: 'step', 'aria-labelledby': 'step-options' }, h('h2', { id: 'step-options' }, 'Options'), optionsForm()),
    h('section', { class: 'step', 'aria-labelledby': 'step-output' }, h('h2', { id: 'step-output' }, 'Output'),
      h('div', { class: 'panel' },
        pills, outputNote, checkOut,
        h('p', {}, regenerateButton)),
      jobPanel),
  );

  // ---- input file: what the recording is ----
  const fileURL = (f, download) => forTenant('api/file?' + (download ? 'download=1&' : '') + 'path=' + q(f.path));

  function showInput() {
    const raw = info.raw, rec = info.recording, from = info.convertedFrom;
    if (!raw) {
      inputPanel.replaceChildren(h('p', {}, 'No recording in this folder yet.'));
      return;
    }
    const fmt = rec && rec.format;
    const rows = [
      ['File', [raw.name, h('span', { class: 'muted' }, ' · ' + formatSize(raw.size))]],
      ['Format', fmt ? describeFormat(fmt) : h('span', { class: 'muted' }, 'Reading…')],
      ['Length', fmt ? formatTime(fmt.duration, 0) : h('span', { class: 'muted' }, '…')],
      ['Source', from
        ? `${describeCodec(fmt) || 'FLAC'}, converted from the original ${describeCodec(from.format) || 'WAV'} ` +
          `(${from.name}, ${formatSize(from.size)}); verified identical`
        : (fmt ? `${describeCodec(fmt)} as it was received` : '…')],
      ['Channels', channelsText()],
    ];
    const md = fmt && fmt.metadata ? Object.entries(fmt.metadata) : [];
    inputPanel.replaceChildren(
      h('dl', { class: 'facts' }, rows.flatMap(([k, v]) => [h('dt', {}, k), h('dd', {}, v)])),
      md.length ? h('details', { class: 'metadata' }, h('summary', {}, `Metadata in the file (${md.length})`),
        h('dl', { class: 'facts' }, md.flatMap(([k, v]) => [h('dt', {}, k), h('dd', {}, v)]))) : '',
      h('p', {}, h('a', { class: 'button', href: fileURL(raw, true) }, 'Download original')));
    showChannelWarning();
  }

  // verdict is what the recording's channels are, for people.
  function verdict(v) {
    return {
      mono: 'Mono (one channel)',
      stereo: 'Stereo',
      identical: 'Mono (the stereo channels are identical)',
      more: 'More than two channels',
      'one-sided': `Mono (the ${v.quiet} side is ${v.quietBy ? v.quietBy + ' dB quieter' : 'silent'})`,
    }[v.kind] || v.kind;
  }

  function channelsText() {
    const rec = info.recording;
    if (!rec) return h('span', { class: 'muted' }, 'Checking…');
    return rec.channels ? verdict(rec.channels) : h('span', { class: 'muted' }, rec.error || 'Not known');
  }

  // A recording that is mono in effect gains nothing from stereo, or worse.
  function showChannelWarning() {
    const v = info.recording && info.recording.channels;
    const mono = v && ['mono', 'identical', 'one-sided'].includes(v.kind);
    channelWarning.textContent = opts.stereo && mono
      ? `⚠ The recording is ${verdict(v).toLowerCase()}: stereo gives ` +
        (v.kind === 'one-sided' ? 'the sermon on one side only' : 'the same sound in bigger files') + '. Choose mono.'
      : '';
    channelWarning.hidden = !channelWarning.textContent;
  }

  // refreshInfo reads the folder again: the recording and the outputs change
  // with conversion, analysis and runs.
  async function refreshInfo() {
    const inf = await api('GET', 'api/folder?path=' + q(path));
    if (!alive) return;
    Object.assign(info, { raw: inf.raw, recording: inf.recording, convertedFrom: inf.convertedFrom, outputs: inf.outputs });
    showInput();
    renderOutputs();
  }
  showInput();
  // The facts come with the waveform's preparation: look again until they are there.
  const waitForFacts = async () => {
    while (alive && info.raw && !info.recording) {
      await new Promise((r) => setTimeout(r, 2000));
      if (alive) await refreshInfo().catch(() => {});
    }
  };
  waitForFacts();

  // ---- waveform ----
  async function prepareWaveform() {
    const bar = h('progress', { max: 1, value: 0 });
    const note = h('p', { class: 'muted' }, 'Preparing the waveform…');
    waveSection.replaceChildren(note, bar);
    let st = await api('POST', 'api/analysis?path=' + q(path));
    while (alive && st.state === 'running') {
      bar.value = st.progress;
      note.textContent = st.stage === 'convert' ? 'Converting the recording to FLAC…' : 'Preparing the waveform…';
      await new Promise((r) => setTimeout(r, 500));
      st = await api('GET', 'api/analysis?path=' + q(path));
    }
    if (!alive) return;
    if (st.state !== 'done') {
      waveSection.replaceChildren(h('p', { class: 'error' }, 'Could not read the recording: ' + (st.error || st.state)));
      return;
    }
    if (info.raw && /\.wav$/i.test(info.raw.name)) { // it became FLAC meanwhile
      await refreshInfo();
      check();
    }
    const peaks = await api('GET', 'api/peaks?path=' + q(path));
    if (!alive) return;
    buildWaveform(peaks, forTenant('api/preview?path=' + q(path) + '&v=' + st.key));
  }

  function buildWaveform(data, previewURL) {
    duration = data.duration;
    silenceEdges = data.silences.flatMap((s) => [s.start, s.end]);
    const minimapEl = h('div', { id: 'minimap' });
    const waveEl = h('div', { id: 'waveform' });
    const zoom = h('input', { type: 'range', min: 0, max: 100, step: 'any', value: 0, 'aria-label': 'Zoom', oninput: () => applyZoom() });
    const clock = h('span', { class: 'clock' });
    const playButton = h('button', { onclick: () => ws.playPause(), title: 'Play / pause (space)' }, '▶ Play');
    const button = (label, title, fn) => h('button', { title, onclick: fn }, label);
    addCutButton = h('button', { title: 'Cut out a part of the sermon, starting at the playhead (c)', onclick: addCutHere }, 'Cut out here');
    waveSection.replaceChildren(
      minimapEl, waveEl,
      h('div', { class: 'controls' },
        h('div', { class: 'group wide' },
          h('strong', {}, 'Navigate'),
          h('div', { class: 'toolbar' },
            playButton,
            button('◀ silence', 'Previous silence edge ([)', () => jump(-1)),
            button('silence ▶', 'Next silence edge (])', () => jump(1)),
            h('span', { class: 'sep' }),
            h('span', { class: 'zoom' },
              button('−', 'Zoom out (-)', () => { zoom.value = Math.max(0, +zoom.value - 10); applyZoom(); }),
              zoom,
              button('+', 'Zoom in (+)', () => { zoom.value = Math.min(100, +zoom.value + 10); applyZoom(); })),
            clock)),
        h('div', { class: 'group' },
          h('strong', {}, 'Sermon'),
          h('div', { class: 'toolbar' },
            button('Set start', 'The section at the playhead (or the next one) starts there (s)', markStart),
            button('Set end', 'The section at the playhead (or the one before) ends there (e)', markEnd),
            h('span', { class: 'sep' }),
            button('Hear start', 'Play 5 s from the start of the sermon', hearStart),
            button('Hear end', 'Play the last 5 s of the sermon', hearEnd),
            button('Play sermon', 'Play all the sections (p)', playSelection)),
          sectionHint,
          sectionList,
          lengthOut),
        h('div', { class: 'group' },
          h('strong', {}, 'Cut out'),
          h('div', { class: 'toolbar' },
            addCutButton,
            h('label', { class: 'check', title: 'Playback jumps over the parts that are cut out and the gaps between sections' }, skipBox, ' Play without the cuts')),
          cutHint,
          cutList),
        h('div', { class: 'group' },
          h('strong', {}, 'Fades'),
          fades),
        h('div', { class: 'group wide' },
          h('strong', {}, 'Saved version'),
          versionOut,
          h('div', { class: 'toolbar' }, saveButton, revertButton, saveNote),
          h('p', { class: 'muted' }, 'The sections, cuts and fades are kept in the folder as you change them. Save makes them the saved version; Revert goes back to it. Regenerate saves them and makes the files.'))),
      h('p', { class: 'touch-hint' },
        'Drag a finger along the waveform to select the sermon, and on an empty part to add another section. ' +
        'Drag the edges to adjust. To cut a part out, put the playhead there and use Cut out here. Grey marks silences.'),
      h('p', { class: 'keys' },
        'Drag on the waveform to select the sermon; drag on an empty part to add another section, or hold ', h('kbd', {}, 'shift'), ' and drag to cut out a part. Drag the edges to adjust. Grey marks silences. Keys: ',
        h('kbd', {}, 'space'), ' play · ', h('kbd', {}, 's'), '/', h('kbd', {}, 'e'), ' start/end at the playhead · ', h('kbd', {}, 'c'), ' cut out here · ',
        h('kbd', {}, '['), '/', h('kbd', {}, ']'), ' silences · ', h('kbd', {}, '←'), '/', h('kbd', {}, '→'), ' 1 s (shift 0.1 s) · ',
        h('kbd', {}, '+'), '/', h('kbd', {}, '-'), ' zoom · ', h('kbd', {}, 'p'), ' play sermon. ',
        wheelZoomNeedsKey() ? [h('kbd', {}, 'ctrl'), '+scroll zooms'] : 'Scroll zooms',
        ' at the mouse, ', h('kbd', {}, 'shift'), '+scroll moves along (Settings › Waveform).'),
    );
    renderCuts();

    regions = RegionsPlugin.create();
    ws = WaveSurfer.create({
      container: waveEl,
      height: 180,
      waveColor: css('--wave'),
      progressColor: css('--wave-progress'),
      cursorColor: css('--fg'),
      cursorWidth: 1,
      normalize: true,
      autoScroll: true,
      autoCenter: false,
      plugins: [
        regions,
        TimelinePlugin.create({ formatTimeCallback: (t) => formatTime(t, 0) }),
        MinimapPlugin.create({ container: minimapEl, height: 40, waveColor: css('--wave'), progressColor: css('--wave-progress'), overlayColor: 'rgba(128,128,128,0.25)' }),
        HoverPlugin.create({ formatTimeCallback: (t) => formatTime(t, 1), lineColor: css('--muted'), labelBackground: css('--panel'), labelColor: css('--fg') }),
      ],
    });
    cleanups.push(() => ws.destroy());
    // Peaks and duration up front: the browser never downloads or decodes
    // the whole recording, it only streams the preview while playing.
    ws.load(previewURL, [data.peaks], data.duration).catch((e) => {
      if (alive) waveSection.prepend(h('p', { class: 'error' }, 'Could not load the preview: ' + e.message));
    });

    ws.on('play', () => { playButton.textContent = '⏸ Pause'; });
    ws.on('pause', () => { playButton.textContent = '▶ Play'; });
    ws.on('timeupdate', (t) => {
      clock.textContent = `${formatTime(t, 1)} / ${formatTime(duration, 0)}`;
      if (stopAt !== null && t >= stopAt) {
        stopAt = null;
        skipNow = false;
        ws.pause();
        return;
      }
      if ((skipBox.checked || skipNow) && ws.isPlaying()) {
        const cut = skipped().find((c) => t >= c.start - 0.03 && t < c.end);
        if (cut) ws.setTime(cut.end);
      }
    });
    ws.on('pause', () => { skipNow = false; });
    clock.textContent = `0:00.0 / ${formatTime(duration, 0)}`;

    ws.once('ready', () => {
      data.silences.forEach((s, i) => {
        const r = regions.addRegion({ id: 'silence-' + i, start: s.start, end: s.end, color: css('--silence'), drag: false, resize: false });
        if (r.element) r.element.style.pointerEvents = 'none'; // selections can start on a silence
      });
      regions.enableDragSelection({ color: css('--selection') });
      const initial = sermon.adjusted || sermon.saved;
      if (initial) applySettings(initial);
      else renderSections();
    });
    // A drag on the waveform makes a region: it becomes a section. Sections
    // added by the page are created with their id, and are kept by addSection.
    regions.on('region-created', (r) => {
      if (/^(silence|cut|section)-/.test(r.id)) return;
      r.setOptions({ id: 'section-' + (++sectionSeq) });
      sections.push({ region: r });
      tidySections();
    });
    // Shift-drag draws a cut. It is handled here, not by the regions plugin,
    // because it must work on top of the selection, which a plain drag would move.
    waveEl.addEventListener('pointerdown', (down) => {
      if (!down.shiftKey || down.button !== 0 || !sections.length) return;
      down.preventDefault();
      down.stopPropagation();
      const timeAt = (e) => {
        const box = ws.getWrapper().getBoundingClientRect();
        return Math.max(0, Math.min(((e.clientX - box.left) / box.width) * duration, duration));
      };
      const from = timeAt(down);
      let cut = null;
      const move = (e) => {
        if (!cut && Math.abs(e.clientX - down.clientX) < 3) return;
        const start = Math.min(from, timeAt(e)), end = Math.max(from, timeAt(e));
        if (!cut) cut = addCut(start, Math.max(end, start + 0.05));
        else { cut.region.setOptions({ start, end }); syncCuts(); }
      };
      const up = () => {
        window.removeEventListener('pointermove', move);
        if (cut) { sortCuts(); renderCuts(); changed(); }
        const noSeek = (e) => e.stopPropagation(); // the click that ends the drag must not move the playhead
        waveEl.addEventListener('click', noSeek, true);
        setTimeout(() => waveEl.removeEventListener('click', noSeek, true), 0);
      };
      window.addEventListener('pointermove', move);
      window.addEventListener('pointerup', up, { once: true });
    }, true);
    regions.on('region-update', (r) => {
      if (sectionOf(r)) { syncSections(); syncCuts(); changed(); } else if (cutOf(r)) syncCuts();
    });
    regions.on('region-updated', (r) => {
      if (sectionOf(r)) tidySections();
      else if (cutOf(r)) { sortCuts(); renderCuts(); changed(); }
    });

    // applyZoom zooms to the slider's value. With at ({ time, x }), time stays
    // x pixels from the left edge (under the mouse); without, the playhead is kept in view.
    function applyZoom(at) {
      const fit = waveEl.clientWidth / duration;
      const px = fit * Math.pow(Math.max(MAX_ZOOM / fit, 1), zoom.value / 100);
      ws.zoom(px);
      if (at) ws.setScroll(Math.max(0, at.time * (ws.getWrapper().scrollWidth / duration) - at.x));
      else reveal(ws.getCurrentTime());
    }
    // The wheel zooms at the mouse: always with ctrl/cmd (also a trackpad pinch),
    // without unless Settings asks for the key. Shift or sideways scrolling is
    // left to the browser, which moves along the waveform.
    waveEl.addEventListener('wheel', (e) => {
      const key = e.ctrlKey || e.metaKey;
      if (!key && (e.shiftKey || wheelZoomNeedsKey() || Math.abs(e.deltaX) > Math.abs(e.deltaY))) return;
      const pixels = e.deltaY * (e.deltaMode === 1 ? 33 : e.deltaMode === 2 ? 600 : 1);
      const next = Math.min(100, Math.max(0, +zoom.value - Math.max(-10, Math.min(pixels / 20, 10))));
      if (next === +zoom.value && !key) return; // all the way in or out: the page scrolls
      e.preventDefault();
      if (next === +zoom.value) return;
      const box = ws.getWrapper().getBoundingClientRect();
      const x = e.clientX - waveEl.getBoundingClientRect().left;
      const time = Math.max(0, Math.min(((e.clientX - box.left) / box.width) * duration, duration));
      zoom.value = next;
      applyZoom({ time, x });
    }, { passive: false });

    function onKey(e) {
      if (typing(e) || e.ctrlKey || e.metaKey || e.altKey || document.querySelector('dialog[open]')) return;
      const step = e.shiftKey ? 0.1 : 1;
      const keys = {
        ' ': () => ws.playPause(),
        s: markStart, e: markEnd, p: playSelection, c: addCutHere,
        '[': () => jump(-1), ']': () => jump(1),
        '+': () => { zoom.value = Math.min(100, +zoom.value + 10); applyZoom(); },
        '=': () => { zoom.value = Math.min(100, +zoom.value + 10); applyZoom(); },
        '-': () => { zoom.value = Math.max(0, +zoom.value - 10); applyZoom(); },
        '0': () => { zoom.value = 0; applyZoom(); },
        ArrowLeft: () => seek(ws.getCurrentTime() - step),
        ArrowRight: () => seek(ws.getCurrentTime() + step),
        Home: () => seek(0),
        End: () => seek(duration),
      };
      const fn = keys[e.key];
      if (fn) {
        e.preventDefault();
        fn();
      }
    }
    document.addEventListener('keydown', onKey);
    cleanups.push(() => document.removeEventListener('keydown', onKey));

    const spans = (list) => list.map((x) => ({ start: x.region.start, end: x.region.end }));
    window.__editor = { ws, regions, get sections() { return spans(sections); }, get cuts() { return spans(cuts); }, addSection, addCut }; // for tests and the console
    cleanups.push(() => { delete window.__editor; });
  }

  // ---- sections: the parts of the recording that make the sermon ----

  // rebuild replaces a list's rows. Removing a focused field makes the browser
  // send it a change event; edits ignore those (rebuilding), as the rows are new.
  let rebuilding = false;
  function rebuild(list, ...rows) {
    rebuilding = true;
    try { list.replaceChildren(...rows); } finally { rebuilding = false; }
  }

  const sectionOf = (r) => sections.find((x) => x.region === r);
  const first = () => sections[0].region.start;
  const last = () => sections[sections.length - 1].region.end;

  // addSection adds a section between two times; it returns it, or null if empty.
  function addSection(start, end) {
    start = Math.max(0, Math.min(start, duration));
    end = Math.max(0, Math.min(end, duration));
    if (end - start < 0.05) return null;
    const section = { region: regions.addRegion({ id: 'section-' + (++sectionSeq), start, end, color: css('--selection'), drag: true, resize: true }) };
    sections.push(section);
    tidySections();
    return section;
  }

  // tidySections sorts the sections and makes those that overlap or touch one
  // (as the server does), then shows them.
  function tidySections() {
    sections.sort((a, b) => a.region.start - b.region.start);
    for (let i = 1; i < sections.length;) {
      const a = sections[i - 1].region, b = sections[i].region;
      if (b.start > a.end) { i++; continue; }
      if (b.end > a.end) a.setOptions({ end: b.end });
      b.remove();
      sections.splice(i, 1);
    }
    renderSections();
    changed();
  }

  function removeSection(x) {
    x.region.remove();
    sections.splice(sections.indexOf(x), 1);
    renderSections();
    changed();
  }

  // setEdge moves the start or end of a section to t; false if it would be empty.
  function setEdge(x, which, t) {
    const r = x.region;
    if (which === 'start' ? t > r.end - 0.05 : t < r.start + 0.05) return false;
    r.setOptions({ [which]: t });
    tidySections();
    return true;
  }

  // renderSections rebuilds the list of sections; syncSections only refreshes its
  // values. With none, an empty row makes the first one from typed times.
  function renderSections() {
    if (!sections.length) {
      const startIn = h('input', { type: 'text', class: 'time', placeholder: 'start', 'aria-label': 'Sermon from' });
      const endIn = h('input', { type: 'text', class: 'time', placeholder: 'end', 'aria-label': 'Sermon to' });
      const edit = () => {
        if (rebuilding) return;
        const start = startIn.value.trim() === '' ? 0 : parseTime(startIn.value);
        const end = endIn.value.trim() === '' ? duration : parseTime(endIn.value);
        startIn.classList.toggle('invalid', Number.isNaN(start));
        endIn.classList.toggle('invalid', Number.isNaN(end) || end <= start);
        if (!Number.isNaN(start) && !Number.isNaN(end) && addSection(start, end)) reveal(start);
      };
      for (const input of [startIn, endIn]) {
        input.addEventListener('change', edit);
        input.addEventListener('keydown', (e) => { if (e.key === 'Enter') { edit(); input.blur(); } });
      }
      rebuild(sectionList, h('div', { class: 'section' }, h('strong', {}, 'Sermon'),
        h('label', {}, 'From ', startIn), h('label', {}, 'to ', endIn)));
      syncSections();
      syncCuts();
      return;
    }
    rebuild(sectionList, ...sections.map((x, i) => {
      const r = x.region;
      const name = sections.length > 1 ? `Section ${i + 1}` : 'Sermon';
      const startIn = x.startIn = h('input', { type: 'text', class: 'time', 'aria-label': `${name} from` });
      const endIn = x.endIn = h('input', { type: 'text', class: 'time', 'aria-label': `${name} to` });
      x.info = h('span', { class: 'muted' });
      const edit = () => {
        if (rebuilding) return;
        const start = startIn.value.trim() === '' ? 0 : parseTime(startIn.value);
        const end = endIn.value.trim() === '' ? duration : parseTime(endIn.value);
        startIn.classList.toggle('invalid', Number.isNaN(start));
        endIn.classList.toggle('invalid', Number.isNaN(end) || end <= start);
        if (!Number.isNaN(start) && !Number.isNaN(end) && end - start >= 0.05) {
          r.setOptions({ start: Math.max(0, start), end: Math.min(end, duration) });
          tidySections();
          reveal(start);
        }
      };
      for (const input of [startIn, endIn]) {
        input.addEventListener('change', edit);
        input.addEventListener('keydown', (e) => { if (e.key === 'Enter') { edit(); input.blur(); } });
      }
      x.row = h('div', { class: 'section' },
        h('strong', {}, name),
        h('label', {}, 'From ', x.startIn), h('label', {}, 'to ', x.endIn), x.info,
        h('button', { title: 'Play this section', onclick: () => playFor(r.start, r.end - r.start) }, 'Play'),
        h('button', { onclick: () => removeSection(x) }, 'Remove'));
      return x.row;
    }));
    syncSections();
    syncCuts();
  }

  function syncSections() {
    for (const x of sections) {
      const r = x.region;
      if (document.activeElement !== x.startIn) x.startIn.value = formatTime(r.start);
      if (document.activeElement !== x.endIn) x.endIn.value = formatTime(r.end);
      x.info.textContent = 'length ' + formatTime(r.end - r.start, 1);
    }
    sectionHint.textContent = sections.length
      ? 'Drag on an empty part of the waveform to add another section; the parts between sections are left out.'
      : 'Drag on the waveform to select the sermon, or set its start and end at the playhead (s, e).';
  }

  // skipped are the parts left out between the first section's start and the
  // last one's end: the gaps between sections and the cuts, in time order.
  function skipped() {
    const list = cuts.map((c) => ({ start: c.region.start, end: c.region.end }));
    for (let i = 1; i < sections.length; i++) list.push({ start: sections[i - 1].region.end, end: sections[i].region.start });
    return list.sort((a, b) => a.start - b.start);
  }

  // ---- cuts: parts of the sermon that are left out ----

  const cutOf = (r) => cuts.find((c) => c.region === r);
  const sortCuts = () => cuts.sort((a, b) => a.region.start - b.region.start);

  // addCut adds a cut between two times; it returns the cut, or null if empty.
  function addCut(start, end) {
    start = Math.max(0, Math.min(start, duration));
    end = Math.max(0, Math.min(end, duration));
    if (end - start < 0.05) return null;
    const cut = { region: regions.addRegion({ id: 'cut-' + (++cutSeq), start, end, color: css('--cut'), drag: true, resize: true }) };
    cuts.push(cut);
    sortCuts();
    renderCuts();
    changed();
    return cut;
  }

  // addCutHere cuts out the next 10 s from the playhead (inside the sermon);
  // the edges are then adjusted by dragging or typing.
  function addCutHere() {
    if (!sections.length) return;
    const start = Math.min(Math.max(ws.getCurrentTime(), first()), last() - 0.1);
    addCut(start, Math.min(start + 10, last()));
  }

  function removeCut(c) {
    c.region.remove();
    cuts.splice(cuts.indexOf(c), 1);
    renderCuts();
    changed();
  }

  // renderCuts rebuilds the list of cuts; syncCuts only refreshes its values.
  function renderCuts() {
    rebuild(cutList, ...cuts.map((c, i) => {
      const r = c.region;
      c.startIn = h('input', { type: 'text', class: 'time', 'aria-label': `Cut ${i + 1} from` });
      c.endIn = h('input', { type: 'text', class: 'time', 'aria-label': `Cut ${i + 1} to` });
      c.info = h('span', { class: 'muted' });
      const edit = () => {
        if (rebuilding) return;
        const start = parseTime(c.startIn.value), end = parseTime(c.endIn.value);
        c.startIn.classList.toggle('invalid', Number.isNaN(start));
        c.endIn.classList.toggle('invalid', Number.isNaN(end) || end <= start);
        if (!Number.isNaN(start) && !Number.isNaN(end) && end > start) {
          r.setOptions({ start: Math.max(0, start), end: Math.min(end, duration) });
          sortCuts();
          renderCuts();
          changed();
        }
      };
      for (const input of [c.startIn, c.endIn]) {
        input.addEventListener('change', edit);
        input.addEventListener('keydown', (e) => { if (e.key === 'Enter') { edit(); input.blur(); } });
      }
      const here = (which) => () => {
        const t = ws.getCurrentTime();
        if (which === 'start' && t < r.end - 0.05) r.setOptions({ start: t });
        if (which === 'end' && t > r.start + 0.05) r.setOptions({ end: t });
        sortCuts();
        renderCuts();
        changed();
      };
      c.row = h('div', { class: 'cut' },
        h('strong', {}, `Cut ${i + 1}`),
        h('label', {}, 'From ', c.startIn), h('label', {}, 'to ', c.endIn), c.info,
        h('button', { title: 'The cut starts at the playhead', onclick: here('start') }, 'Set start'),
        h('button', { title: 'The cut ends at the playhead', onclick: here('end') }, 'Set end'),
        h('button', { title: 'Play 3 s before and after the cut, as it will sound', onclick: () => {
          const from = Math.max(r.start - 3, 0);
          playFor(from, r.end + 3 - from, true);
        } }, 'Hear join'),
        h('button', { onclick: () => removeCut(c) }, 'Remove'));
      return c.row;
    }));
    syncCuts();
  }

  function syncCuts() {
    for (const c of cuts) {
      const r = c.region;
      if (document.activeElement !== c.startIn) c.startIn.value = formatTime(r.start);
      if (document.activeElement !== c.endIn) c.endIn.value = formatTime(r.end);
      const outside = !sections.some((x) => r.start < x.region.end && r.end > x.region.start);
      c.row.classList.toggle('outside', outside);
      c.info.textContent = (outside ? 'outside the sermon · ' : '') + 'length ' + formatTime(r.end - r.start, 1);
    }
    cutHint.textContent = !sections.length ? 'Select the sermon first; then cut out the parts of it that should go, such as a song.'
      : cuts.length ? '' : 'Nothing cut out. Use the button, press c, or shift-drag on the waveform.';
    if (addCutButton) addCutButton.disabled = !sections.length;
    const parts = sections.length > 1 ? `${sections.length} sections` : '';
    const what = [parts, cuts.length ? (cuts.length === 1 ? '1 cut' : `${cuts.length} cuts`) : ''].filter(Boolean).join(' and ');
    lengthOut.textContent = sections.length ? 'Length ' + formatTime(keptLength(), 1) + (what ? ` (${what})` : '') : '';
  }

  // keptLength is the sections without the cuts: what the outputs will last.
  function keptLength() {
    let length = last() - first(), at = first();
    for (const c of skipped()) { // in time order; overlaps count once
      const from = Math.max(c.start, at), to = Math.min(c.end, last());
      if (to > from) { length -= to - from; at = to; }
    }
    return length;
  }

  // ---- keeping the selection, cuts and fades: sermon.json in the folder has the
  // saved version and the adjustments, which are kept as the page changes ----

  let restoring = false; // putting settings back is not a change
  let sermon = info.sermon || { saved: null, adjusted: null };
  let lastSent = JSON.stringify(sermon.adjusted || sermon.saved);

  // currentSettings is what is on the page, or null while there is nothing (valid) to keep.
  function currentSettings() {
    if (!sections.length || restoring) return null;
    const secs = sections.map((x) => ({ start: x.region.start, end: x.region.end }));
    const list = cuts.map((c) => ({ start: c.region.start, end: c.region.end }));
    if (secs.some((x) => x.end - x.start < 0.05)) return null;
    if (list.some((c) => c.end - c.start < 0.05 || c.start < first() || c.end > last())) return null;
    if (!(opts.fadeIn >= 0) || !(opts.fadeOut >= 0)) return null;
    return { sections: secs, cuts: list, fadeIn: opts.fadeIn, fadeOut: opts.fadeOut };
  }

  // persistAdjusted keeps what is on the page as the adjustments.
  async function persistAdjusted() {
    const state = currentSettings();
    if (!state || JSON.stringify(state) === lastSent) return;
    saveNote.textContent = 'Keeping…';
    try {
      sermon = await api('PUT', 'api/sermon?path=' + q(path), state);
      lastSent = JSON.stringify(state);
      saveNote.textContent = 'Changes kept.';
    } catch (e) {
      saveNote.textContent = 'Not kept: ' + e.message;
    }
    renderVersion();
  }
  const saveSoon = debounce(() => { if (alive) persistAdjusted(); }, 700);

  // applySettings puts settings on the page: the sections, the cuts and the fades.
  function applySettings(set) {
    restoring = true;
    try {
      for (const x of [...sections, ...cuts]) x.region.remove();
      sections.length = cuts.length = 0;
      for (const x of set.sections) addSection(x.start, x.end);
      if (sections.length) {
        for (const c of set.cuts) addCut(c.start, c.end);
        reveal(first());
      }
      for (const key of ['fadeIn', 'fadeOut']) {
        opts[key] = set[key];
        const input = view.querySelector(`input[name=${key}]`);
        if (input) input.value = set[key];
      }
    } finally {
      restoring = false;
    }
    renderSections();
    renderCuts();
    check();
  }

  const describe = (set) => [
    set.sections.map((x) => `${formatTime(x.start, 0)} to ${formatTime(x.end, 0)}`).join(' and '),
    set.cuts.length === 1 ? '1 cut' : `${set.cuts.length} cuts`,
    set.fadeIn || set.fadeOut ? `fade ${set.fadeIn} s in, ${set.fadeOut} s out` : 'no fades',
  ].join(', ');

  function renderVersion() {
    const { saved, adjusted } = sermon;
    const at = sermon.savedAt ? ' ' + formatDateTime(sermon.savedAt) : '';
    versionOut.replaceChildren(
      saved ? h('p', {}, h('strong', {}, 'Saved' + at + ': '), describe(saved)) : h('p', { class: 'muted' }, 'Nothing is saved yet.'),
      adjusted ? h('p', {}, h('strong', {}, 'Adjusted, not saved: '), describe(adjusted)) : '');
    saveButton.disabled = !adjusted;
    revertButton.disabled = !adjusted || !saved;
  }

  // saveVersion makes the adjustments the saved version, after asking.
  async function saveVersion() {
    await persistAdjusted();
    if (!sermon.adjusted) return;
    const ok = await confirmDialog({
      title: 'Save these settings?',
      body: [
        sermon.saved ? [h('p', {}, 'This replaces the saved version:'), h('ul', {}, h('li', {}, describe(sermon.saved)))] : '',
        h('p', {}, sermon.saved ? 'With:' : 'The saved version will be:'),
        h('ul', {}, h('li', {}, describe(sermon.adjusted))),
      ],
      confirm: 'Save',
    });
    if (!ok) return;
    try {
      sermon = await api('POST', 'api/sermon/save?path=' + q(path));
      saveNote.textContent = 'Saved.';
    } catch (e) {
      saveNote.textContent = 'Not saved: ' + e.message;
    }
    renderVersion();
  }

  // revertVersion drops the adjustments and goes back to the saved version, after asking.
  async function revertVersion() {
    if (!sermon.adjusted || !sermon.saved) return;
    const ok = await confirmDialog({
      title: 'Go back to the saved version?',
      body: [
        h('p', {}, 'Your adjustments are discarded:'), h('ul', {}, h('li', {}, describe(sermon.adjusted))),
        h('p', {}, 'Back to the saved version:'), h('ul', {}, h('li', {}, describe(sermon.saved))),
      ],
      confirm: 'Revert',
      danger: true,
    });
    if (!ok) return;
    try {
      sermon = await api('POST', 'api/sermon/revert?path=' + q(path));
      lastSent = JSON.stringify(sermon.saved);
      applySettings(sermon.saved);
      saveNote.textContent = 'Back to the saved version.';
    } catch (e) {
      saveNote.textContent = 'Not reverted: ' + e.message;
    }
    renderVersion();
  }

  function seek(t) {
    t = Math.max(0, Math.min(t, duration));
    ws.setTime(t);
    reveal(t);
  }

  // reveal scrolls a zoomed waveform so t is in view.
  function reveal(t) {
    if (!ws) return;
    const wrapper = ws.getWrapper();
    const visible = (wrapper.parentElement?.clientWidth || wrapper.clientWidth) / (wrapper.scrollWidth / duration);
    const left = ws.getScroll() / (wrapper.scrollWidth / duration);
    if (t < left || t > left + visible) ws.setScrollTime(Math.max(0, t - visible / 2));
  }

  // sectionAt is the section around t, or null.
  const sectionAt = (t) => sections.find((x) => t >= x.region.start && t <= x.region.end) || null;

  // markStart starts the section at the playhead there, or else the next one;
  // with none after it, a new section runs from there to the end.
  function markStart() {
    const t = ws.getCurrentTime();
    const x = sectionAt(t) || sections.find((y) => y.region.start > t);
    if (x) setEdge(x, 'start', t);
    else addSection(t, duration);
  }

  // markEnd ends the section at the playhead there, or else the one before;
  // with none before it, a new section runs from the beginning to there.
  function markEnd() {
    const t = ws.getCurrentTime();
    const x = sectionAt(t) || [...sections].reverse().find((y) => y.region.end < t);
    if (x) setEdge(x, 'end', t);
    else addSection(0, t);
  }

  function jump(dir) {
    const t = ws.getCurrentTime();
    const edge = dir < 0
      ? [...silenceEdges].reverse().find((e) => e < t - 0.05) ?? 0
      : silenceEdges.find((e) => e > t + 0.05) ?? duration;
    seek(edge);
  }

  // playFor plays from a time and stops after some seconds; skipCuts makes it
  // jump over the cuts even when "play without the cuts" is off.
  function playFor(from, seconds, skipCuts = false) {
    ws.setTime(from);
    stopAt = from + seconds;
    skipNow = skipCuts;
    ws.play();
  }

  // playSelection plays the sermon: all the sections, without the gaps
  // between them (and without the cuts if "play without the cuts" is on).
  function playSelection() {
    if (sections.length) playFor(first(), last() - first());
  }

  function hearStart() {
    if (sections.length) playFor(first(), Math.min(5, last() - first()));
  }

  function hearEnd() {
    if (sections.length) playFor(Math.max(last() - 5, first()), Math.min(5, last() - first()));
  }

  // ---- options ----
  function optionsForm() {
    const field = (key, label, input) => h('label', {}, h('span', { class: 'name' }, label), input);
    const num = (key, label, step) => field(key, label, h('input', {
      type: 'number', name: key, value: opts[key], step,
      oninput: (e) => { opts[key] = e.target.value === '' ? NaN : Number(e.target.value); changed(); },
    }));
    const text = (key, label, placeholder) => field(key, label, h('input', {
      type: 'text', name: key, value: opts[key], placeholder,
      oninput: (e) => { opts[key] = e.target.value; changed(); },
    }));
    const tag = (key, label) => h('label', { class: 'tag' }, h('span', { class: 'name' }, label), h('input', {
      type: 'text', name: key, value: tags[key], maxlength: 200,
      oninput: (e) => { tags[key] = e.target.value; changed(); },
    }));
    // Mono or stereo: a switch between the two, not a box to tick.
    const stereo = h('input', {
      type: 'checkbox', role: 'switch', name: 'stereo', checked: opts.stereo, 'aria-label': 'Stereo',
      onchange: (e) => {
        followChannels(e.target.checked);
        opts.stereo = e.target.checked;
        showChannelWarning();
        changed();
      },
    });
    const channels = h('div', { class: 'channels' },
      h('span', { class: 'name' }, 'Channels'),
      h('label', { class: 'switch' }, h('span', { class: 'side off' }, 'Mono'), stereo, h('span', { class: 'track', 'aria-hidden': 'true' }), h('span', { class: 'side on' }, 'Stereo')));
    return h('div', { class: 'panel' },
      h('div', { class: 'options' },
        h('div', {}, h('strong', {}, 'Sound'), channels, channelWarning),
        info.tags ? h('div', {}, h('strong', {}, 'Tags'), tag('title', 'Title'), tag('artist', 'Artist'), tag('album', 'Album'))
          : h('div', {}, h('strong', {}, 'Tags'), h('span', { class: 'muted' }, info.tagsError || 'No tags'))),
      h('details', {}, h('summary', {}, 'More sound options'),
        h('div', { class: 'options' },
          num('noiseReduction', 'Noise reduction (dB)', 1), num('targetLufs', 'Target loudness (LUFS)', 0.5),
          num('truePeak', 'Max true peak (dBTP)', 0.1), text('mp3Bitrate', 'MP3 bitrate'), text('opusBitrate', 'Opus bitrate'))));
  }

  // followChannels moves the loudness and bitrates to the new defaults when
  // switching mono/stereo, unless they were changed by hand.
  function followChannels(stereo) {
    const from = stereo ? config.defaults.mono : config.defaults.stereo;
    const to = stereo ? config.defaults.stereo : config.defaults.mono;
    for (const key of ['targetLufs', 'mp3Bitrate', 'opusBitrate']) {
      if (String(opts[key]) === String(from[key])) {
        opts[key] = to[key];
        const input = view.querySelector(`input[name=${key}]`);
        if (input) input.value = to[key];
      }
    }
  }

  function request(force = false) {
    return {
      ...opts,
      force,
      ...(info.tags ? tags : {}),
      noiseReduction: Math.round(opts.noiseReduction),
      sections: sections.map((x) => ({ start: x.region.start, end: x.region.end })),
      cuts: sections.length ? cuts.map((c) => ({ start: c.region.start, end: c.region.end })) : [],
    };
  }

  // ---- check and run ----
  const check = debounce(async () => {
    if (!alive) return;
    const req = request();
    if ([req.fadeIn, req.fadeOut, req.noiseReduction, req.targetLufs, req.truePeak].some(Number.isNaN)) {
      showCheck({ error: 'Fill in all numbers.' });
      return;
    }
    try {
      // All three are checked: whether each is there and up to date does not
      // depend on which are chosen.
      showCheck(await api('POST', 'api/check?path=' + q(path), { ...req, flac: true, mp3: true, opus: true }));
    } catch (e) {
      showCheck({ error: e.message });
    }
  }, 250);

  function changed() {
    saveOptions(opts);
    check();
    saveSoon();
  }

  function showCheck(res) {
    lastCheck = res;
    checkOut.replaceChildren(
      res.error ? h('p', { class: 'error' }, res.error) : '',
      ...(res.notices || []).map((n) => h('p', { class: 'notice' }, n)));
    renderOutputs();
  }

  function busy() { return job && (job.state === 'queued' || job.state === 'running'); }

  // ---- output: the formats, what is there, and what Regenerate makes ----
  const FORMATS = [['flac', 'FLAC'], ['mp3', 'MP3'], ['opus', 'Opus']];

  // outputState is a format's file: there or not, and whether Regenerate
  // would make it again (stale: made from other settings; unknown: no record).
  function outputState(format) {
    const file = info.outputs.find((o) => o.format === format) || null;
    const st = (lastCheck.outputs || {})[format];
    return { file, stale: Boolean(file && st && (st.stale || !st.known)), unknown: Boolean(file && st && !st.known) };
  }

  // toMake is what Regenerate makes: the chosen formats that are missing or out
  // of date; toDelete what it deletes: the files of the formats not chosen.
  const toMake = () => FORMATS.map(([f]) => f).filter((f) => opts[f] && (!outputState(f).file || outputState(f).stale));
  const toDelete = () => FORMATS.map(([f]) => f).filter((f) => !opts[f] && outputState(f).file);
  const labels = (list) => list.map((f) => FORMATS.find(([x]) => x === f)[1]).join(', ');

  function renderOutputs() {
    pills.replaceChildren(...FORMATS.map(([format, label]) => {
      const { file, stale, unknown } = outputState(format);
      const chosen = Boolean(opts[format]);
      const note = !file ? (chosen ? 'will be made' : 'not made')
        : !chosen ? 'will be deleted'
          : stale ? 'will regenerate' : 'up to date';
      return h('div', { 'data-format': format, class: 'pill' + (file ? ' exists' : '') + (chosen ? ' chosen' : '') + (stale ? ' stale' : '') },
        h('button', {
          class: 'pick', 'aria-pressed': String(chosen), title: chosen ? `Leave ${label} out` : `Make ${label} too`,
          onclick: () => { opts[format] = !opts[format]; renderOutputs(); },
        }, h('span', { class: 'box', 'aria-hidden': 'true' }, chosen ? '☑' : '☐'), ' ', label, file ? ' ✓' : ''),
        h('span', { class: 'state' + (file && !chosen ? ' delete' : chosen && (stale || !file) ? ' will' : '') }, note));
    }));
    const make = toMake(), del = toDelete();
    const blocked = make.length > 0 && Boolean(lastCheck.error); // deleting does not need a valid edit
    outputNote.textContent = blocked ? ''
      : make.length || del.length
        ? 'Regenerate ' + [make.length ? 'saves the edit and makes: ' + labels(make) : '', del.length ? 'deletes: ' + labels(del) : '']
          .filter(Boolean).join(', and ') + '.'
        : 'Everything chosen is up to date.';
    regenerateButton.disabled = blocked || busy() || !(make.length || del.length);
  }

  // regenerate saves the edit, then makes the chosen formats that are missing
  // or out of date and deletes the files of those not chosen; it asks first
  // when that replaces or deletes files, or changes the saved version.
  async function regenerate() {
    await persistAdjusted();
    const make = toMake(), del = toDelete();
    if (!make.length && !del.length) return;
    const replaced = make.map(outputState).filter((x) => x.file).map((x) => x.file.name);
    const deleted = del.map((f) => outputState(f).file.name);
    const changes = make.length > 0 && (Boolean(sermon.adjusted) || !sermon.saved);
    if (replaced.length || deleted.length || changes) {
      const ok = await confirmDialog({
        title: 'Regenerate?',
        body: [
          make.length ? h('p', {}, 'The sermon is cut and processed from the recording, with the sections, cuts and fades shown here.') : '',
          changes ? h('p', {}, sermon.saved ? 'They replace the saved version.' : 'They become the saved version.') : '',
          replaced.length ? [h('p', {}, 'These files are replaced:'), h('ul', {}, replaced.map((n) => h('li', {}, n)))] : '',
          deleted.length ? [h('p', {}, 'These files are deleted:'), h('ul', {}, deleted.map((n) => h('li', {}, n)))] : '',
        ],
        confirm: 'Regenerate',
        danger: replaced.length > 0 || deleted.length > 0,
      });
      if (!ok) return;
    }
    regenerateButton.disabled = true;
    try {
      if (del.length) {
        const inf = await api('POST', 'api/outputs/remove?path=' + q(path) + del.map((f) => '&format=' + f).join(''));
        info.outputs = inf.outputs;
        renderOutputs();
        check();
      }
      if (!make.length) return;
      if (sermon.adjusted) sermon = await api('POST', 'api/sermon/save?path=' + q(path));
      renderVersion();
      const req = request(replaced.length > 0);
      for (const [f] of FORMATS) req[f] = make.includes(f);
      job = await api('POST', 'api/run?path=' + q(path), req);
      showJob(job);
      follow(job.id);
    } catch (e) {
      showCheck({ ...lastCheck, error: e.message });
    }
  }

  function follow(id) {
    if (events) events.close();
    events = new EventSource(forTenant('api/jobs/' + id + '/events'));
    events.onmessage = (e) => {
      job = JSON.parse(e.data);
      showJob(job);
      if (!busy()) {
        events.close();
        events = null;
        refreshInfo().then(check).catch(() => {}); // the outputs changed
      }
    };
  }

  function showJob(j) {
    const running = j.state === 'queued' || j.state === 'running';
    const title = { queued: 'Waiting for another run to finish…', running: 'Processing…', done: 'Done', failed: 'Failed', cancelled: 'Cancelled' }[j.state];
    const steps = j.steps.map((s, i) => {
      const last = i === j.steps.length - 1;
      return h('li', { class: last && running ? 'current' : last && j.state === 'failed' ? 'failed' : '' }, s);
    });
    const log = h('pre', { class: 'log' }, j.log.join('\n'));
    jobPanel.replaceChildren(h('div', { class: 'panel' },
      h('h2', {}, title),
      h('ul', { class: 'steps' }, steps),
      running ? h('progress', { max: 1, value: j.progress }) : '',
      j.warnings.length ? h('div', {}, j.warnings.map((w) => h('p', { class: 'warn' }, '⚠ ' + w))) : '',
      j.error ? h('p', { class: 'error' }, j.error) : '',
      j.state === 'cancelled' ? h('p', {}, 'Cancelled. Nothing half-written was left behind.') : '',
      j.state === 'done' ? h('p', {}, 'Done. Listen to and download the files from the folder list.') : '',
      h('details', { open: running }, h('summary', {}, 'Log'), log),
      running ? h('button', { onclick: cancel }, 'Cancel') : ''));
    log.scrollTop = log.scrollHeight;
    renderOutputs();
  }

  async function cancel() {
    try {
      job = await api('POST', 'api/jobs/' + job.id + '/cancel');
      showJob(job);
    } catch (e) {
      jobPanel.prepend(h('p', { class: 'error' }, e.message));
    }
  }

  if (job) {
    showJob(job);
    if (busy()) follow(job.id);
  }
  if (info.raw) {
    prepareWaveform().catch((e) => {
      if (alive) waveSection.replaceChildren(h('p', { class: 'error' }, e.message));
    });
  } else {
    waveSection.replaceChildren(h('p', {}, 'Nothing to cut yet: upload a recording, or put <date>_raw.wav in the folder.'));
  }
  renderVersion();
  renderOutputs();
  check();
  return teardown;
}

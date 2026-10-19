// The editor for one date folder: cut the sermon on the waveform, set the
// options, run, and follow the job.
import WaveSurfer from './vendor/wavesurfer/wavesurfer.esm.js';
import RegionsPlugin from './vendor/wavesurfer/plugins/regions.esm.js';
import TimelinePlugin from './vendor/wavesurfer/plugins/timeline.esm.js';
import MinimapPlugin from './vendor/wavesurfer/plugins/minimap.esm.js';
import HoverPlugin from './vendor/wavesurfer/plugins/hover.esm.js';
import { confirmDialog } from './dialog.js';
import { api, q, h, parseTime, formatTime, formatSize, typing, debounce, crumbs, forTenant, suggestNames } from './util.js';

const STORE = 'sermon-studio-options';
const OLD_STORE = 'sermon-normalizer-options'; // before the rename
const MAX_ZOOM = 200; // px per second: 5 ms per pixel, enough to cut between words

const css = (name) => getComputedStyle(document.documentElement).getPropertyValue(name).trim();

// savedOptions are the choices of the last run, except per-run ones.
function savedOptions(config) {
  const opts = { ...config.defaults.mono };
  try { Object.assign(opts, JSON.parse(localStorage.getItem(STORE) || localStorage.getItem(OLD_STORE) || '{}')); } catch {}
  delete opts.force; // from before it was a button
  return opts;
}

function saveOptions(opts) {
  const { start, end, title, artist, album, archive, keepWav, ...keep } = opts; // the last two are from older versions
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
  // Tags are per sermon: the church's defaults for this folder, editable for this run.
  const tags = { title: '', artist: '', album: '', ...(info.tags || {}) };
  let job = info.job;
  let sel = null; // the selection region
  let regions = null; // the waveform's regions plugin: selection, cuts, silences
  const cuts = []; // parts cut out of the selection: { region, row, ... }, in time order
  let cutSeq = 0;
  let ws = null;
  let duration = 0;
  let silenceEdges = [];
  let setSelection = () => false; // set once the waveform exists
  let stopAt = null; // playback pauses here (hear start/end, play selection)
  let addCutButton = null; // disabled until there is a selection
  let skipNow = false; // this playback jumps over the cuts even with "play without the cuts" off
  let lastCheck = { error: 'Checking…' };
  let events = null; // the job's event stream
  cleanups.push(() => events && events.close());

  // ---- layout ----
  const waveSection = h('section', {});
  const startInput = h('input', { type: 'text', class: 'time', placeholder: 'start', 'aria-label': 'Start' });
  const endInput = h('input', { type: 'text', class: 'time', placeholder: 'end', 'aria-label': 'End' });
  const lengthOut = h('span', { class: 'muted' });
  const selection = h('div', { class: 'selection' },
    h('label', {}, 'Start ', startInput), h('label', {}, 'End ', endInput), lengthOut);
  const cutList = h('div', { class: 'cuts' });
  const cutHint = h('p', { class: 'muted' });
  const skipBox = h('input', { type: 'checkbox', checked: true });
  const recordingLine = h('p', { class: 'muted' });
  const showRecording = (raw) => {
    recordingLine.textContent = raw ? `Recording: ${raw.name} · ${formatSize(raw.size)}` : 'No raw recording in this folder.';
  };
  showRecording(info.raw);
  const filesSection = h('section', {});
  const checkOut = h('div', {});
  const runButton = h('button', { class: 'primary', disabled: true, title: 'Make the files that are missing', onclick: () => run() }, 'Run');
  const generateButton = h('button', { disabled: true, title: 'Save the cuts and make all the files again', onclick: generate }, 'Save and generate');
  const saveNote = h('span', { class: 'muted' });
  const versionOut = h('div', {});
  const saveButton = h('button', { disabled: true, title: 'Make the current settings the saved version', onclick: saveVersion }, 'Save…');
  const revertButton = h('button', { disabled: true, title: 'Throw away the adjustments and go back to the saved version', onclick: revertVersion }, 'Revert…');
  const jobPanel = h('section', {});

  // Another date or name for this sermon: renames the folder and its files.
  const renamePanel = h('div', { class: 'panel', hidden: true });
  const renameButton = h('button', { class: 'link', title: 'Change the date or name of this sermon', onclick: () => {
    const [, d = '', n = ''] = info.date.match(/^(\d{4}-\d{2}-\d{2})(?: (.*))?$/) || [];
    const date = h('input', { type: 'date', value: d, required: true, 'aria-label': 'Date' });
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
    recordingLine,
    filesSection,
    waveSection,
    h('h2', {}, 'Options'),
    optionsForm(),
    h('div', { class: 'panel' }, checkOut, h('p', {}, runButton, ' ', generateButton)),
    jobPanel,
  );

  // ---- files: the outputs to listen to and download, and the sources ----
  const fileURL = (f, download) => forTenant('api/file?' + (download ? 'download=1&' : '') + 'path=' + q(f.path));
  let lastOutputs = info.outputs.map((o) => o.name); // the files a new run would replace
  function showFiles(inf) {
    lastOutputs = inf.outputs.map((o) => o.name);
    const outputs = inf.outputs.map((o) => h('div', { class: 'out' },
      h('strong', {}, o.name), h('span', { class: 'muted' }, formatSize(o.size)),
      h('audio', { controls: true, preload: 'none', src: fileURL(o) }),
      h('a', { href: fileURL(o, true) }, 'Download')));
    filesSection.replaceChildren(
      outputs.length ? h('div', { class: 'outputs' }, outputs) : '',
      inf.raw ? h('p', { class: 'muted' }, 'Original: ', h('a', { href: fileURL(inf.raw, true) }, inf.raw.name), ` (the whole recording, ${formatSize(inf.raw.size)})`) : '');
  }
  async function refreshFiles() {
    const inf = await api('GET', 'api/folder?path=' + q(path));
    showRecording(inf.raw);
    showFiles(inf);
  }
  showFiles(info);

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
      await refreshFiles();
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
    const zoom = h('input', { type: 'range', min: 0, max: 100, value: 0, 'aria-label': 'Zoom', oninput: () => applyZoom() });
    const clock = h('span', { class: 'clock' });
    const playButton = h('button', { onclick: () => ws.playPause(), title: 'Play / pause (space)' }, '▶ Play');
    const button = (label, title, fn) => h('button', { title, onclick: fn }, label);
    addCutButton = h('button', { title: 'Cut out a part of the sermon, starting at the playhead (c)', onclick: addCutHere }, 'Cut out here');
    waveSection.replaceChildren(
      minimapEl, waveEl,
      h('div', { class: 'controls' },
        h('div', { class: 'group' },
          h('strong', {}, 'Navigate'),
          h('div', { class: 'toolbar' },
            playButton,
            button('◀ silence', 'Previous silence edge ([)', () => jump(-1)),
            button('silence ▶', 'Next silence edge (])', () => jump(1)),
            h('span', { class: 'sep' }),
            button('−', 'Zoom out (-)', () => { zoom.value = Math.max(0, +zoom.value - 10); applyZoom(); }),
            zoom,
            button('+', 'Zoom in (+)', () => { zoom.value = Math.min(100, +zoom.value + 10); applyZoom(); }),
            clock)),
        h('div', { class: 'group' },
          h('strong', {}, 'Sermon'),
          h('div', { class: 'toolbar' },
            button('Set start', 'The sermon starts at the playhead (s)', markStart),
            button('Set end', 'The sermon ends at the playhead (e)', markEnd),
            h('span', { class: 'sep' }),
            button('Hear start', 'Play 5 s from the start of the sermon', () => sel && playFor(sel.start, 5)),
            button('Hear end', 'Play the last 5 s of the sermon', () => sel && playFor(Math.max(sel.end - 5, 0), sel.end - Math.max(sel.end - 5, 0))),
            button('Play sermon', 'Play the whole selection (p)', playSelection)),
          selection),
        h('div', { class: 'group' },
          h('strong', {}, 'Cut out'),
          h('div', { class: 'toolbar' },
            addCutButton,
            h('label', { class: 'check', title: 'Playback jumps over the parts that are cut out' }, skipBox, ' Play without the cuts')),
          cutHint,
          cutList),
        h('div', { class: 'group' },
          h('strong', {}, 'Saved version'),
          versionOut,
          h('div', { class: 'toolbar' }, saveButton, revertButton, saveNote),
          h('p', { class: 'muted' }, 'The selection, cuts and fades are kept in the folder as you change them. Save makes them the saved version; Revert goes back to it. Run uses what is on the page.'))),
      h('p', { class: 'keys' },
        'Drag on the waveform to select the sermon; hold ', h('kbd', {}, 'shift'), ' and drag to cut out a part of it. Drag the edges to adjust. Grey marks silences. Keys: ',
        h('kbd', {}, 'space'), ' play · ', h('kbd', {}, 's'), '/', h('kbd', {}, 'e'), ' start/end at the playhead · ', h('kbd', {}, 'c'), ' cut out here · ',
        h('kbd', {}, '['), '/', h('kbd', {}, ']'), ' silences · ', h('kbd', {}, '←'), '/', h('kbd', {}, '→'), ' 1 s (shift 0.1 s) · ',
        h('kbd', {}, '+'), '/', h('kbd', {}, '-'), ' zoom · ', h('kbd', {}, 'p'), ' play sermon'),
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
        const cut = cuts.find((c) => t >= c.region.start - 0.03 && t < c.region.end);
        if (cut) ws.setTime(cut.region.end);
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
    });
    regions.on('region-created', (r) => {
      if (r.id.startsWith('silence-') || r.id.startsWith('cut-')) return;
      if (sel && sel !== r) sel.remove();
      sel = r;
      selectionChanged();
    });
    // Shift-drag draws a cut. It is handled here, not by the regions plugin,
    // because it must work on top of the selection, which a plain drag would move.
    waveEl.addEventListener('pointerdown', (down) => {
      if (!down.shiftKey || down.button !== 0 || !sel) return;
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
      if (r === sel) selectionChanged();
      else if (cutOf(r)) syncCuts();
    });
    regions.on('region-updated', (r) => {
      if (r === sel) selectionChanged();
      else if (cutOf(r)) { sortCuts(); renderCuts(); changed(); }
    });

    function applyZoom() {
      const fit = waveEl.clientWidth / duration;
      const px = fit * Math.pow(Math.max(MAX_ZOOM / fit, 1), zoom.value / 100);
      ws.zoom(px);
      reveal(ws.getCurrentTime());
    }
    waveEl.addEventListener('wheel', (e) => {
      if (!e.ctrlKey && !e.metaKey) return;
      e.preventDefault();
      zoom.value = Math.min(100, Math.max(0, +zoom.value + (e.deltaY < 0 ? 5 : -5)));
      applyZoom();
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

    // setSelection is used by the fields and buttons; the waveform makes its own.
    function setSelectionImpl(start, end) {
      start = Math.max(0, Math.min(start, duration));
      end = Math.max(0, Math.min(end, duration));
      if (end <= start) return false;
      if (sel) sel.setOptions({ start, end });
      else regions.addRegion({ start, end, color: css('--selection'), drag: true, resize: true });
      selectionChanged();
      return true;
    }
    setSelection = setSelectionImpl;
    window.__editor = { ws, regions, get selection() { return sel; }, get cuts() { return cuts.map((c) => ({ start: c.region.start, end: c.region.end })); }, setSelection, addCut }; // for tests and the console
    cleanups.push(() => { delete window.__editor; });
  }

  // ---- selection ----

  function selectionChanged() {
    if (!sel) return;
    if (document.activeElement !== startInput) startInput.value = formatTime(sel.start);
    if (document.activeElement !== endInput) endInput.value = formatTime(sel.end);
    startInput.classList.remove('invalid');
    endInput.classList.remove('invalid');
    syncCuts();
    changed();
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
    if (!sel) return;
    const start = Math.min(Math.max(ws.getCurrentTime(), sel.start), sel.end - 0.1);
    addCut(start, Math.min(start + 10, sel.end));
  }

  function removeCut(c) {
    c.region.remove();
    cuts.splice(cuts.indexOf(c), 1);
    renderCuts();
    changed();
  }

  // renderCuts rebuilds the list of cuts; syncCuts only refreshes its values.
  function renderCuts() {
    cutList.replaceChildren(...cuts.map((c, i) => {
      const r = c.region;
      c.startIn = h('input', { type: 'text', class: 'time', 'aria-label': `Cut ${i + 1} from` });
      c.endIn = h('input', { type: 'text', class: 'time', 'aria-label': `Cut ${i + 1} to` });
      c.info = h('span', { class: 'muted' });
      const edit = () => {
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
      const outside = !sel || r.start < sel.start - 0.001 || r.end > sel.end + 0.001;
      c.row.classList.toggle('outside', outside);
      c.info.textContent = (outside ? 'outside the sermon · ' : '') + 'length ' + formatTime(r.end - r.start, 1);
    }
    cutHint.textContent = !sel ? 'Select the sermon first; then cut out the parts of it that should go, such as a song.'
      : cuts.length ? '' : 'Nothing cut out. Use the button, press c, or shift-drag on the waveform.';
    if (addCutButton) addCutButton.disabled = !sel;
    lengthOut.textContent = sel ? 'Length ' + formatTime(keptLength(), 1) + (cuts.length ? ' after the cuts' : '') : '';
  }

  // keptLength is the sermon without the cuts: what the outputs will last.
  function keptLength() {
    let length = sel.end - sel.start, at = sel.start;
    for (const c of cuts) { // in time order; overlaps count once
      const from = Math.max(c.region.start, at, sel.start), to = Math.min(c.region.end, sel.end);
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
    if (!sel || restoring) return null;
    const list = cuts.map((c) => ({ start: c.region.start, end: c.region.end }));
    if (list.some((c) => c.end - c.start < 0.05 || c.start < sel.start - 0.001 || c.end > sel.end + 0.001)) return null;
    if (!(opts.fadeIn >= 0) || !(opts.fadeOut >= 0)) return null;
    return { start: sel.start, end: sel.end, cuts: list, fadeIn: opts.fadeIn, fadeOut: opts.fadeOut };
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

  // applySettings puts settings on the page: the selection, the cuts and the fades.
  function applySettings(set) {
    restoring = true;
    try {
      for (const c of cuts) c.region.remove();
      cuts.length = 0;
      if (setSelection(set.start, set.end)) {
        for (const c of set.cuts) addCut(c.start, c.end);
        reveal(set.start);
      }
      for (const key of ['fadeIn', 'fadeOut']) {
        opts[key] = set[key];
        const input = view.querySelector(`input[name=${key}]`);
        if (input) input.value = set[key];
      }
    } finally {
      restoring = false;
    }
    renderCuts();
    check();
  }

  const describe = (set) => [
    `${formatTime(set.start, 0)} to ${formatTime(set.end, 0)}`,
    set.cuts.length === 1 ? '1 cut' : `${set.cuts.length} cuts`,
    set.fadeIn || set.fadeOut ? `fade ${set.fadeIn} s in, ${set.fadeOut} s out` : 'no fades',
  ].join(', ');

  function renderVersion() {
    const { saved, adjusted } = sermon;
    const at = sermon.savedAt ? ' ' + new Date(sermon.savedAt).toLocaleString() : '';
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

  function fieldsChanged() {
    const start = startInput.value.trim() === '' ? 0 : parseTime(startInput.value);
    const end = endInput.value.trim() === '' ? duration : parseTime(endInput.value);
    startInput.classList.toggle('invalid', Number.isNaN(start));
    endInput.classList.toggle('invalid', Number.isNaN(end) || end <= start);
    if (!Number.isNaN(start) && !Number.isNaN(end) && setSelection(start, end)) reveal(start);
  }
  for (const input of [startInput, endInput]) {
    input.addEventListener('change', fieldsChanged);
    input.addEventListener('keydown', (e) => { if (e.key === 'Enter') { fieldsChanged(); input.blur(); } });
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

  function markStart() {
    const t = ws.getCurrentTime();
    if (sel && t < sel.end) setSelection(t, sel.end);
    else setSelection(t, duration);
  }

  function markEnd() {
    const t = ws.getCurrentTime();
    if (sel && t > sel.start) setSelection(sel.start, t);
    else setSelection(0, t);
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

  function playSelection() {
    if (sel) playFor(sel.start, sel.end - sel.start);
  }

  // ---- options ----
  function optionsForm() {
    const field = (key, label, input) => h('label', {}, h('span', { class: 'name' }, label), input);
    const check = (key, label) => h('label', { class: 'check' }, h('input', {
      type: 'checkbox', name: key, checked: opts[key],
      onchange: (e) => {
        if (key === 'stereo') followChannels(e.target.checked);
        opts[key] = e.target.checked;
        changed();
      },
    }), label);
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
    const form = h('div', { class: 'panel' },
      h('div', { class: 'options' },
        h('div', {}, h('strong', {}, 'Outputs'), check('flac', 'FLAC master'), check('mp3', 'MP3'), check('opus', 'Opus')),
        h('div', {}, h('strong', {}, 'Sound'), check('stereo', 'Stereo (off = mono)'), num('fadeIn', 'Fade in (s)', 0.5), num('fadeOut', 'Fade out (s)', 0.5)),
        info.tags ? h('div', {}, h('strong', {}, 'Tags'), tag('title', 'Title'), tag('artist', 'Artist'), tag('album', 'Album'))
          : h('div', {}, h('strong', {}, 'Tags'), h('span', { class: 'muted' }, info.tagsError || 'No tags'))),
      h('details', {}, h('summary', {}, 'More sound options'),
        h('div', { class: 'options' },
          num('noiseReduction', 'Noise reduction (dB)', 1), num('targetLufs', 'Target loudness (LUFS)', 0.5),
          num('truePeak', 'Max true peak (dBTP)', 0.1), text('mp3Bitrate', 'MP3 bitrate'), text('opusBitrate', 'Opus bitrate'))));
    return form;
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
      start: sel ? sel.start : 0,
      end: sel ? sel.end : 0,
      cuts: sel ? cuts.map((c) => ({ start: c.region.start, end: c.region.end })) : [],
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
      showCheck(await api('POST', 'api/check?path=' + q(path), req));
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
    updateRunButton();
  }

  function busy() { return job && (job.state === 'queued' || job.state === 'running'); }
  function updateRunButton() {
    runButton.disabled = generateButton.disabled = Boolean(lastCheck.error) || busy();
  }

  // generate makes every file again, after asking: it replaces what is there.
  async function generate() {
    await persistAdjusted();
    const existing = lastOutputs;
    const changes = Boolean(sermon.adjusted) || !sermon.saved; // the page differs from the saved version
    const ok = await confirmDialog({
      title: 'Save and generate?',
      body: [
        h('p', {}, 'The sermon is cut and processed again from the recording, with the selection, cuts and fades shown here.'),
        changes ? h('p', {}, sermon.saved ? 'They replace the saved version.' : 'They become the saved version.') : '',
        existing.length
          ? [h('p', {}, 'These files are replaced:'), h('ul', {}, existing.map((n) => h('li', {}, n)))]
          : h('p', {}, 'No files are made yet, so nothing is replaced.'),
      ],
      confirm: 'Save and generate',
      danger: existing.length > 0,
    });
    if (!ok) return;
    try {
      if (sermon.adjusted) sermon = await api('POST', 'api/sermon/save?path=' + q(path));
      renderVersion();
    } catch (e) {
      showCheck({ ...lastCheck, error: e.message });
      return;
    }
    await run(true);
  }

  async function run(force = false) {
    runButton.disabled = generateButton.disabled = true;
    try {
      await persistAdjusted();
      job = await api('POST', 'api/run?path=' + q(path), request(force));
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
        check(); // existing outputs changed
        refreshFiles().catch(() => {});
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
      j.state === 'done' ? h('p', {}, 'The files are listed at the top of the page.') : '',
      h('details', { open: running }, h('summary', {}, 'Log'), log),
      running ? h('button', { onclick: cancel }, 'Cancel') : ''));
    log.scrollTop = log.scrollHeight;
    updateRunButton();
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
  check();
  return teardown;
}

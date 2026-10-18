// The editor for one date folder: cut the sermon on the waveform, set the
// options, run, and follow the job.
import WaveSurfer from './vendor/wavesurfer/wavesurfer.esm.js';
import RegionsPlugin from './vendor/wavesurfer/plugins/regions.esm.js';
import TimelinePlugin from './vendor/wavesurfer/plugins/timeline.esm.js';
import MinimapPlugin from './vendor/wavesurfer/plugins/minimap.esm.js';
import HoverPlugin from './vendor/wavesurfer/plugins/hover.esm.js';
import { api, q, h, parseTime, formatTime, formatSize, typing, debounce, crumbs } from './util.js';

const STORE = 'sermon-normalizer-options';
const MAX_ZOOM = 200; // px per second: 5 ms per pixel, enough to cut between words

const css = (name) => getComputedStyle(document.documentElement).getPropertyValue(name).trim();

// savedOptions are the choices of the last run, except per-run ones.
function savedOptions(config) {
  const opts = { ...config.defaults.mono, archive: config.archive || '' };
  try { Object.assign(opts, JSON.parse(localStorage.getItem(STORE) || '{}')); } catch {}
  opts.force = false;
  return opts;
}

function saveOptions(opts) {
  const { start, end, useEdit, force, ...keep } = opts;
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
  let useEdit = Boolean(info.edit && info.editInFolder);
  let job = info.job;
  let sel = null; // the selection region
  let ws = null;
  let duration = 0;
  let silenceEdges = [];
  let setSelection = () => false; // set once the waveform exists
  let stopAt = null; // playback pauses here (hear start/end, play selection)
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
  const editChoice = info.edit && info.editInFolder
    ? h('label', {}, h('input', { type: 'checkbox', checked: useEdit, onchange: (e) => { useEdit = e.target.checked; changed(); } }),
      ` Use ${info.edit.name} (cut in another editor) instead of the selection`)
    : null;
  const recordingLine = h('p', { class: 'muted' });
  const showRecording = (raw) => {
    recordingLine.textContent = raw ? `Recording: ${raw.name} · ${formatSize(raw.size)}` : 'No raw recording in this folder.';
  };
  showRecording(info.raw);
  const checkOut = h('div', {});
  const runButton = h('button', { class: 'primary', disabled: true, onclick: run }, 'Run');
  const jobPanel = h('section', {});

  view.replaceChildren(
    crumbs(path.split('/').slice(0, -1).join('/'), info.date),
    h('h1', {}, info.date),
    recordingLine,
    waveSection,
    editChoice ? h('p', {}, editChoice) : '',
    h('h2', {}, 'Options'),
    optionsForm(),
    h('div', { class: 'panel' }, checkOut, h('p', {}, runButton)),
    jobPanel,
  );

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
      showRecording((await api('GET', 'api/folder?path=' + q(path))).raw);
      check();
    }
    const peaks = await api('GET', 'api/peaks?path=' + q(path));
    if (!alive) return;
    buildWaveform(peaks, 'api/preview?path=' + q(path) + '&v=' + st.key);
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
    waveSection.replaceChildren(
      minimapEl, waveEl,
      h('div', { class: 'toolbar' },
        playButton,
        button('Play selection', 'p', playSelection),
        button('Hear start', 'Play 5 s from the start of the selection', () => sel && playFor(sel.start, 5)),
        button('Hear end', 'Play the last 5 s of the selection', () => sel && playFor(Math.max(sel.end - 5, 0), sel.end - Math.max(sel.end - 5, 0))),
        h('span', { class: 'sep' }),
        button('Set start', 'Selection starts at the playhead (s)', markStart),
        button('Set end', 'Selection ends at the playhead (e)', markEnd),
        h('span', { class: 'sep' }),
        button('◀ silence', 'Previous silence edge ([)', () => jump(-1)),
        button('silence ▶', 'Next silence edge (])', () => jump(1)),
        h('span', { class: 'sep' }),
        button('−', 'Zoom out (-)', () => { zoom.value = Math.max(0, +zoom.value - 10); applyZoom(); }),
        zoom,
        button('+', 'Zoom in (+)', () => { zoom.value = Math.min(100, +zoom.value + 10); applyZoom(); }),
        clock),
      selection,
      h('p', { class: 'keys' },
        'Drag on the waveform to select the sermon; drag the edges to adjust. Grey marks silences. Keys: ',
        h('kbd', {}, 'space'), ' play · ', h('kbd', {}, 's'), '/', h('kbd', {}, 'e'), ' start/end at the playhead · ',
        h('kbd', {}, '['), '/', h('kbd', {}, ']'), ' silences · ', h('kbd', {}, '←'), '/', h('kbd', {}, '→'), ' 1 s (shift 0.1 s) · ',
        h('kbd', {}, '+'), '/', h('kbd', {}, '-'), ' zoom · ', h('kbd', {}, 'p'), ' play selection'),
    );

    const regions = RegionsPlugin.create();
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
        ws.pause();
      }
    });
    clock.textContent = `0:00.0 / ${formatTime(duration, 0)}`;

    ws.once('ready', () => {
      data.silences.forEach((s, i) => {
        const r = regions.addRegion({ id: 'silence-' + i, start: s.start, end: s.end, color: css('--silence'), drag: false, resize: false });
        if (r.element) r.element.style.pointerEvents = 'none'; // selections can start on a silence
      });
      regions.enableDragSelection({ color: css('--selection') });
    });
    regions.on('region-created', (r) => {
      if (r.id.startsWith('silence-')) return;
      if (sel && sel !== r) sel.remove();
      sel = r;
      selectionChanged();
    });
    regions.on('region-update', (r) => { if (r === sel) selectionChanged(); });
    regions.on('region-updated', (r) => { if (r === sel) selectionChanged(); });

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
      if (typing(e) || e.ctrlKey || e.metaKey || e.altKey) return;
      const step = e.shiftKey ? 0.1 : 1;
      const keys = {
        ' ': () => ws.playPause(),
        s: markStart, e: markEnd, p: playSelection,
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
    window.__editor = { ws, regions, get selection() { return sel; }, setSelection }; // for tests and the console
    cleanups.push(() => { delete window.__editor; });
  }

  // ---- selection ----

  function selectionChanged() {
    if (!sel) return;
    if (document.activeElement !== startInput) startInput.value = formatTime(sel.start);
    if (document.activeElement !== endInput) endInput.value = formatTime(sel.end);
    startInput.classList.remove('invalid');
    endInput.classList.remove('invalid');
    lengthOut.textContent = 'Length ' + formatTime(sel.end - sel.start, 1);
    changed();
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

  function playFor(from, seconds) {
    ws.setTime(from);
    stopAt = from + seconds;
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
    const form = h('div', { class: 'panel' },
      h('div', { class: 'options' },
        h('div', {}, h('strong', {}, 'Outputs'), check('flac', 'FLAC master'), check('mp3', 'MP3'), check('opus', 'Opus')),
        h('div', {}, h('strong', {}, 'Sound'), check('stereo', 'Stereo (off = mono)'), num('fadeIn', 'Fade in (s)', 0.5), num('fadeOut', 'Fade out (s)', 0.5)),
        h('div', {}, h('strong', {}, 'Files'), text('archive', 'Archive folder', 'none'), check('keepWav', 'Keep WAV'), check('force', 'Force (redo existing)'))),
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

  function request() {
    return {
      ...opts,
      noiseReduction: Math.round(opts.noiseReduction),
      start: !useEdit && sel ? sel.start : 0,
      end: !useEdit && sel ? sel.end : 0,
      useEdit,
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
  }

  function showCheck(res) {
    lastCheck = res;
    checkOut.replaceChildren(
      res.error ? h('p', { class: 'error' }, res.error) : '',
      ...(res.notices || []).map((n) => h('p', { class: 'notice' }, n)));
    updateRunButton();
  }

  function busy() { return job && (job.state === 'queued' || job.state === 'running'); }
  function updateRunButton() { runButton.disabled = Boolean(lastCheck.error) || busy(); }

  async function run() {
    runButton.disabled = true;
    try {
      job = await api('POST', 'api/run?path=' + q(path), request());
      showJob(job);
      follow(job.id);
    } catch (e) {
      showCheck({ ...lastCheck, error: e.message });
    }
  }

  function follow(id) {
    if (events) events.close();
    events = new EventSource('api/jobs/' + id + '/events');
    events.onmessage = (e) => {
      job = JSON.parse(e.data);
      showJob(job);
      if (!busy()) {
        events.close();
        events = null;
        check(); // existing outputs changed
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
      j.outputs.length ? h('div', { class: 'outputs' }, j.outputs.map((o) => h('div', { class: 'out' },
        h('strong', {}, o.name), h('span', { class: 'muted' }, formatSize(o.size)),
        h('audio', { controls: true, preload: 'none', src: 'api/file?path=' + q(o.path) }),
        h('a', { href: 'api/file?download=1&path=' + q(o.path) }, 'Download')))) : '',
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
    waveSection.replaceChildren(h('p', {}, info.edit
      ? `Only an edit file (${info.edit.name}) is here; it will be used as the sermon.`
      : 'Nothing to cut yet: import a recording from the inbox, or put <date>_raw.wav in the folder.'));
    useEdit = Boolean(info.edit);
  }
  check();
  return teardown;
}

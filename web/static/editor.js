// The editor for one date folder: cut the sermon on the waveform, set the
// options, run, and follow the job.
import WaveSurfer from './vendor/wavesurfer/wavesurfer.esm.js';
import RegionsPlugin from './vendor/wavesurfer/plugins/regions.esm.js';
import TimelinePlugin from './vendor/wavesurfer/plugins/timeline.esm.js';
import MinimapPlugin from './vendor/wavesurfer/plugins/minimap.esm.js';
import HoverPlugin from './vendor/wavesurfer/plugins/hover.esm.js';
import { confirmDialog, infoDialog } from './dialog.js';
import { t, tn, tr, lang } from './i18n.js';
import { api, q, h, href, converts, isVideo, parseTime, formatTime, formatSize, typing, debounce, forTenant, currentTenant, dateInput, formatDateTime, wheelZoomNeedsKey, kbdText } from './util.js';

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
    tn(fmt.channels, '{n} channel', '{n} channels'),
    fmt.bitrate ? `${fmt.bitrate.toLocaleString(lang)} kb/s` : '',
  ].filter(Boolean).join(' · ');
}

// SOUND_KEYS are the processing choices that are kept with a sermon.
const SOUND_KEYS = ['stereo', 'noiseReduction', 'roomSound', 'leveling', 'compression', 'targetLufs', 'truePeak', 'mp3Bitrate', 'opusBitrate'];

// DEFAULT_KEYS are what the church's default processing sets: the sound and the fades.
const DEFAULT_KEYS = [...SOUND_KEYS, 'fadeIn', 'fadeOut'];

// FORMAT_KEYS are chosen per recording.
const FORMAT_KEYS = ['flac', 'mp3', 'opus'];

export async function editorView(view, path, config) {
  const info = await api('GET', 'api/folder?path=' + q(path));
  let alive = true;
  const cleanups = [];
  const teardown = () => {
    alive = false;
    for (const fn of cleanups) fn();
  };

  const opts = { ...config.defaults.start }; // the church's defaults
  // The processing kept with this recording comes before them.
  const keptSound = info.sermon && (info.sermon.adjusted || info.sermon.saved)?.sound;
  if (keptSound) Object.assign(opts, keptSound);
  // Going back to the church's defaults, or making this processing the default (see useDefaults).
  const sameAsDefaults = () => DEFAULT_KEYS.every((k) => String(opts[k]) === String(config.defaults.start[k]));
  const defaultsNote = h('span', { class: 'muted', role: 'status' });
  const useDefaultsButton = h('button', { type: 'button', name: 'use-defaults', onclick: useDefaults }, t('Use the defaults'));
  const makeDefaultsButton = h('button', { type: 'button', name: 'make-defaults', onclick: makeDefaults }, t('Make these the defaults'));

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
  let skipNow = false; // this playback jumps over the cuts even when the whole recording is played
  // What the Play button plays: what will be published (the sections without the cuts and the gaps, and the
  // pauses kept), or the recording as it is. The choice is the browser's.
  let playMode = localStorage.getItem('playMode') === 'recording' ? 'recording' : 'published';
  let pausing = false; // playback is waiting out a pause
  let lastTime = 0;
  let markSignature = '';
  let lastCheck = { error: t('Checking…') };
  let events = null; // the job's event stream
  cleanups.push(() => events && events.close());

  // ---- layout ----
  const waveSection = h('section', {});
  const lengthOut = h('p', { class: 'length' });
  const sectionList = h('div', { class: 'sections' });
  const sectionHint = h('p', { class: 'muted section-hint' });
  const cutList = h('div', { class: 'cuts' });
  const cutHint = h('p', { class: 'muted' });
  const cutHelp = h('p', { class: 'muted' }, t('Mark a part that should not be published, such as a cough, a song or something sensitive.'));
  const inputPanel = h('div', { class: 'panel input-file' });
  const channelWarning = h('p', { class: 'notice', role: 'status' });
  const pills = h('div', { class: 'pills', role: 'group', 'aria-label': t('Formats') });
  const outputNote = h('p', { class: 'muted' });
  const checkOut = h('div', {});
  const saveButton = h('button', { class: 'primary', disabled: true, title: t('Make this the saved version and make the files'), onclick: save }, t('Save'));
  const saveNote = h('span', { class: 'muted', role: 'status' });
  const versionOut = h('strong', {});
  const revertButton = h('button', { disabled: true, title: t('Throw away the adjustments and go back to the saved version'), onclick: revertVersion }, t('Revert…'));
  const undoButton = h('button', { disabled: true, title: t('Undo the last change to the edit (ctrl+z)'), 'aria-label': t('Undo'), onclick: () => undo() }, '↶', h('span', { class: 'wide-only' }, ' ' + t('Undo')));
  const redoButton = h('button', { disabled: true, title: t('Redo what was undone (ctrl+shift+z)'), 'aria-label': t('Redo'), onclick: () => redo() }, '↷', h('span', { class: 'wide-only' }, ' ' + t('Redo')));
  const jobPanel = h('section', {});

  // Another date or name for this sermon: renames the folder and its files.
  const renamePanel = h('div', { class: 'panel', hidden: true });
  const renameButton = h('button', { class: 'link', title: t('Change the date or name of this sermon'), onclick: () => {
    const [, d = '', n = ''] = info.date.match(/^(\d{4}-\d{2}-\d{2})(?: (.*))?$/) || [];
    const date = dateInput({ name: 'date', value: d, required: true, 'aria-label': t('Date') });
    const name = h('input', { type: 'text', value: n, maxlength: 60, placeholder: t('optional, e.g. Evening'), 'aria-label': t('Name') });
    const msg = h('span', { class: 'error' });
    const save = h('button', { class: 'primary', onclick: async () => {
      try {
        const res = await api('POST', 'api/rename?path=' + q(path), { date: date.value, name: name.value });
        location.hash = href('folder', res.path);
      } catch (err) { msg.textContent = err.message; }
    } }, t('Rename'));
    renamePanel.replaceChildren(
      h('label', {}, t('Date'), ' ', date), ' ', h('label', {}, t('Name'), ' ', name), ' ', save, ' ',
      h('button', { onclick: () => { renamePanel.hidden = true; } }, t('Cancel')), ' ', msg,
      h('p', { class: 'muted' }, t('The folder and its files are renamed. Files already made keep their tags.')));
    renamePanel.hidden = false;
  } }, t('Rename…'));

  // The bar under the page: what the edit is (saved or not), what Save does, Revert and Save. It sticks to
  // the bottom of the window, so Save is at hand wherever the page is scrolled.
  const saveBar = h('div', { class: 'save-bar' },
    h('div', { class: 'save-state' }, versionOut, ' ', saveNote),
    h('div', { class: 'save-what' }, outputNote, checkOut),
    h('div', { class: 'save-actions' }, undoButton, redoButton, revertButton, saveButton));

  let processingEl = processingPanel();
  view.replaceChildren(
    h('div', { class: 'crumbs' }, h('a', { href: href('recording', path) }, t('← Recordings'))),
    h('h1', {}, info.date, ' ', renameButton),
    renamePanel,
    h('section', { class: 'step', 'aria-labelledby': 'step-input' }, h('h2', { id: 'step-input' }, t('Input file')), inputPanel),
    h('section', { class: 'step', 'aria-labelledby': 'step-edit' }, h('h2', { id: 'step-edit' }, t('Edit')), waveSection),
    h('section', { class: 'step', 'aria-labelledby': 'step-metadata' }, h('h2', { id: 'step-metadata' }, t('Metadata added to the file')), metadataPanel()),
    h('section', { class: 'step', 'aria-labelledby': 'step-processing' }, h('h2', { id: 'step-processing' }, t('Processing')), processingEl),
    h('section', { class: 'step', 'aria-labelledby': 'step-output' }, h('h2', { id: 'step-output' }, t('Output')),
      h('div', { class: 'panel' }, pills),
      jobPanel),
    saveBar,
  );

  // ---- input file: what the recording is ----
  const fileURL = (f, download) => forTenant('api/file?' + (download ? 'download=1&' : '') + 'path=' + q(f.path));

  function showInput() {
    const raw = info.raw, rec = info.recording, from = info.convertedFrom;
    if (!raw) {
      inputPanel.replaceChildren(h('p', {}, t('No recording yet.')));
      return;
    }
    const fmt = rec && rec.format;
    const rows = [
      [t('File'), [raw.name, h('span', { class: 'muted' }, ' · ' + formatSize(raw.size))]],
      [t('Format'), fmt ? describeFormat(fmt) : h('span', { class: 'muted' }, t('Reading…'))],
      [t('Length'), fmt ? formatTime(fmt.duration, 0) : h('span', { class: 'muted' }, '…')],
      [t('Source'), from && isVideo(from.name)
        ? t('{codec}, the sound of the original video ({name}, {size}), copied out unchanged; verified identical',
          { codec: describeCodec(fmt), name: from.name, size: formatSize(from.size) })
        : from
          ? t('{codec}, converted from the original {original} ({name}, {size}); verified identical',
            { codec: describeCodec(fmt) || 'FLAC', original: describeCodec(from.format) || 'WAV', name: from.name, size: formatSize(from.size) })
          : (fmt ? t('{codec} as it was received', { codec: describeCodec(fmt) }) : '…')],
      [t('Channels'), channelsText()],
    ];
    const md = fmt && fmt.metadata ? Object.entries(fmt.metadata) : [];
    const open = inputPanel.querySelector('details.input-more')?.open || false;
    inputPanel.replaceChildren(h('div', { class: 'input-line' },
      h('details', { class: 'input-more', open },
        h('summary', { title: t('Show the details of the recording') },
          h('strong', {}, raw.name), ' · ', formatSize(raw.size),
          ' · ', fmt ? formatTime(fmt.duration, 0) : h('span', { class: 'muted' }, t('Reading…')),
          ' · ', channelsText()),
        h('dl', { class: 'facts' }, rows.flatMap(([k, v]) => [h('dt', {}, k), h('dd', {}, v)])),
        md.length ? h('details', { class: 'metadata' }, h('summary', {}, t('Metadata in the file ({n})', { n: md.length })),
          h('dl', { class: 'facts' }, md.flatMap(([k, v]) => [h('dt', {}, k), h('dd', {}, v)]))) : ''),
      h('a', { class: 'button', href: fileURL(raw, true) }, t('Download original'))));
    showChannelWarning();
  }

  // verdict is what the recording's channels are, for people.
  function verdict(v) {
    return {
      mono: t('Mono (one channel)'),
      stereo: t('Stereo'),
      identical: t('Mono (both channels are the same)'),
      more: t('More than two channels'),
      'one-sided': v.quietBy
        ? t('Mono (the {side} side is {by} dB quieter)', { side: t(v.quiet), by: v.quietBy })
        : t('Mono (the {side} side is silent)', { side: t(v.quiet) }),
    }[v.kind] || v.kind;
  }

  function channelsText() {
    const rec = info.recording;
    if (!rec) return h('span', { class: 'muted' }, t('Checking…'));
    return rec.channels ? verdict(rec.channels) : h('span', { class: 'muted' }, rec.error ? tr(rec.error) : t('Not known'));
  }

  // A recording that is mono in effect gains nothing from stereo, or worse.
  function showChannelWarning() {
    const v = info.recording && info.recording.channels;
    const mono = v && ['mono', 'identical', 'one-sided'].includes(v.kind);
    channelWarning.textContent = opts.stereo && mono
      ? t(v.kind === 'one-sided'
        ? '⚠ The recording is {verdict}: stereo gives the sermon on one side only. Choose mono.'
        : '⚠ The recording is {verdict}: stereo gives the same sound in bigger files. Choose mono.', { verdict: verdict(v).toLowerCase() })
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
    const note = h('p', { class: 'muted' }, t('Preparing the waveform…'));
    waveSection.replaceChildren(note, bar);
    let st = await api('POST', 'api/analysis?path=' + q(path));
    while (alive && st.state === 'running') {
      bar.value = st.progress;
      note.textContent = st.stage === 'convert' ? (info.raw && isVideo(info.raw.name) ? t('Taking the sound out of the video…') : t('Converting the recording to FLAC…')) : t('Preparing the waveform…');
      await new Promise((r) => setTimeout(r, 500));
      st = await api('GET', 'api/analysis?path=' + q(path));
    }
    if (!alive) return;
    if (st.state !== 'done') {
      waveSection.replaceChildren(h('p', { class: 'error' }, t('Could not read the recording: {error}', { error: st.error ? tr(st.error) : st.state })));
      return;
    }
    if (info.raw && converts(info.raw.name)) { // it became FLAC (or .mka) meanwhile
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
    const zoom = h('input', { type: 'range', min: 0, max: 100, step: 'any', value: 0, 'aria-label': t('Zoom'), oninput: () => applyZoom() });
    const clock = h('span', { class: 'clock' });
    const playButton = h('button', { onclick: () => ws.playPause(), title: t('Play / pause (space)') }, t('▶ Play'));
    const button = (label, title, fn) => h('button', { title, onclick: fn }, label);
    const modeButton = (mode, label, title) => h('button', { type: 'button', class: 'seg', name: 'play-' + mode, title, 'aria-pressed': String(playMode === mode),
      onclick: (e) => {
        playMode = mode;
        localStorage.setItem('playMode', mode);
        for (const b of e.currentTarget.parentElement.children) b.setAttribute('aria-pressed', String(b === e.currentTarget));
      } }, label);
    addCutButton = h('button', { title: t('Start a cut at the playhead; press again where it ends (c)'), onclick: addCutHere }, t('Cut out here'));
    waveSection.replaceChildren(
      minimapEl, waveEl,
      h('div', { class: 'controls' },
        h('div', { class: 'group wide' },
          h('strong', {}, t('Navigate')),
          h('div', { class: 'toolbar' },
            playButton,
            h('span', { class: 'mode', role: 'group', 'aria-label': t('What Play plays') },
              modeButton('published', t('The published parts'), t('Play what will be published: the sections, without the cuts and the parts between the sections')),
              modeButton('recording', t('The recording'), t('Play the recording as it is, with the cuts and the parts between the sections'))),
            h('span', { class: 'sep' }),
            button(t('◀ silence'), t('Previous silence edge ([)'), () => jump(-1)),
            button(t('silence ▶'), t('Next silence edge (])'), () => jump(1)),
            h('span', { class: 'sep' }),
            button(t('Hear start'), t('Play 5 s from the start of the sermon'), hearStart),
            button(t('Hear end'), t('Play the last 5 s of the sermon'), hearEnd),
            button(t('Hear the whole sermon'), t('Play all the sections as they will be published (p)'), playSelection),
            h('span', { class: 'sep' }),
            h('span', { class: 'zoom' },
              button('−', t('Zoom out (-)'), () => { zoom.value = Math.max(0, +zoom.value - 10); applyZoom(); }),
              zoom,
              button('+', t('Zoom in (+)'), () => { zoom.value = Math.min(100, +zoom.value + 10); applyZoom(); })),
            clock)),
        h('div', { class: 'group' },
          h('strong', {}, t('Sections')),
          h('div', { class: 'toolbar' },
            button(t('Set start'), t('The section at the playhead (or the next one) starts there (s)'), markStart),
            button(t('Set end'), t('The section at the playhead (or the one before) ends there (e)'), markEnd),
            h('span', { class: 'sep' }),
            button(t('Split here'), t('Split the section at the playhead into two (x)'), splitHere)),
          sectionHint,
          sectionList,
          lengthOut),
        h('div', { class: 'group' },
          h('strong', {}, t('Cut out')),
          h('div', { class: 'toolbar' },
            addCutButton),
          cutHelp,
          cutHint,
          cutList)),
      h('p', { class: 'touch-hint' },
        t('Drag along the waveform to select the sermon. Drag on an empty part to add another section. Drag the edges to adjust. To cut a part out, press Cut out here where it starts and End the cut here where it ends. Grey marks silence.')),
      h('p', { class: 'keys' },
        kbdText(t('Drag on the waveform to select the sermon. Drag on an empty part to add another section, or hold <kbd>shift</kbd> and drag to cut out a part. Drag the edges to adjust. Grey marks silence.')), ' ',
        h('button', { type: 'button', class: 'link', title: t('All the keys of the editor (?)'), onclick: showKeys }, t('Keys…'))),
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
        TimelinePlugin.create({ formatTimeCallback: (s) => formatTime(s, 0) }),
        MinimapPlugin.create({ container: minimapEl, height: 40, waveColor: css('--wave'), progressColor: css('--wave-progress'), overlayColor: 'rgba(128,128,128,0.25)' }),
        HoverPlugin.create({ formatTimeCallback: (s) => formatTime(s, 1), lineColor: css('--muted'), labelBackground: css('--panel'), labelColor: css('--fg') }),
      ],
    });
    cleanups.push(() => ws.destroy());
    // Peaks and duration up front: the browser never downloads or decodes
    // the whole recording, it only streams the preview while playing.
    ws.load(previewURL, [data.peaks], data.duration).catch((e) => {
      if (alive) waveSection.prepend(h('p', { class: 'error' }, t('Could not load the preview: {error}', { error: e.message })));
    });

    ws.on('play', () => { playButton.textContent = t('⏸ Pause'); });
    ws.on('pause', () => { playButton.textContent = t('▶ Play'); });
    ws.on('timeupdate', (now) => followOpenCut(now));
    ws.on('timeupdate', (now) => {
      clock.textContent = `${formatTime(now, 1)} / ${formatTime(duration, 0)}`;
      if (stopAt !== null && now >= stopAt) {
        stopAt = null;
        skipNow = false;
        ws.pause();
        return;
      }
      if ((playMode === 'published' || skipNow) && ws.isPlaying()) {
        // A pause is waited out where it falls, once.
        const wait = activePauses().find((p) => lastTime < p.at && now >= p.at && now - p.at < 0.5);
        lastTime = now;
        if (wait && !pausing) {
          pausing = true;
          ws.pause();
          setTimeout(() => { if (pausing && alive) { pausing = false; ws.play(); } }, wait.seconds * 1000);
          return;
        }
        const cut = skipped().find((c) => now >= c.start - 0.03 && now < c.end);
        if (cut) ws.setTime(cut.end);
      }
      lastTime = now;
    });
    ws.on('pause', () => { if (!pausing) skipNow = false; });
    ws.on('play', () => { pausing = false; });
    clock.textContent = `0:00.0 / ${formatTime(duration, 0)}`;

    ws.once('ready', () => {
      data.silences.forEach((s, i) => {
        const r = regions.addRegion({ id: 'silence-' + i, start: s.start, end: s.end, color: css('--silence'), drag: false, resize: false });
        if (r.element) r.element.style.pointerEvents = 'none'; // selections can start on a silence
      });
      regions.enableDragSelection({ color: css('--selection') });
      const initial = sermon.adjusted || sermon.saved;
      if (initial) applySettings(initial);
      else proposeSection(data.silences);
      present = snapshot(); // what undo goes back to, at most
      loadHistory();
    });
    buildFades();
    // A drag on the waveform makes a region: it becomes a section. Sections
    // added by the page are created with their id, and are kept by addSection.
    regions.on('region-created', (r) => {
      if (/^(silence|cut|section|pause)-/.test(r.id)) return;
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
    // A click on a section or cut selects it (its row is marked); a double-click zooms to it.
    regions.on('region-clicked', (r) => select(sectionOf(r) || cutOf(r) || null));
    regions.on('region-double-clicked', (r) => {
      const x = sectionOf(r) || cutOf(r);
      if (!x) return;
      select(x);
      zoomTo(r.start, r.end);
    });

    // zoomTo zooms so start..end fills most of the waveform, and scrolls to it.
    function zoomTo(start, end) {
      const fit = waveEl.clientWidth / duration;
      const want = (waveEl.clientWidth * 0.9) / Math.max(end - start, 0.05);
      const top = Math.max(MAX_ZOOM / fit, 1);
      zoom.value = top > 1 ? Math.min(100, Math.max(0, (100 * Math.log(Math.max(want / fit, 1))) / Math.log(top))) : 0;
      applyZoom({ time: start, x: waveEl.clientWidth * 0.05 });
    }

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
        s: markStart, e: markEnd, p: playSelection, c: addCutHere, x: splitHere,
        '[': () => jump(-1), ']': () => jump(1),
        '+': () => { zoom.value = Math.min(100, +zoom.value + 10); applyZoom(); },
        '=': () => { zoom.value = Math.min(100, +zoom.value + 10); applyZoom(); },
        '-': () => { zoom.value = Math.max(0, +zoom.value - 10); applyZoom(); },
        '0': () => { zoom.value = 0; applyZoom(); },
        ArrowLeft: () => seek(ws.getCurrentTime() - step),
        ArrowRight: () => seek(ws.getCurrentTime() + step),
        Home: () => seek(0),
        End: () => seek(duration),
        Delete: removeSelected,
        Backspace: removeSelected,
        ',': () => nudge(-step / 10),
        '.': () => nudge(step / 10),
        '<': () => nudge(-step / 10),
        '>': () => nudge(step / 10),
        Escape: () => { if (!cancelCut()) select(null); },
        '?': showKeys,
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
    window.__editor = { ws, regions, get sections() { return spans(sections); }, get cuts() { return spans(cuts); }, addSection, addCut, splitHere, setFade, undo, redo, get selected() { return selected ? { kind: sections.includes(selected) ? 'section' : 'cut', start: selected.region.start, end: selected.region.end } : null; }, get history() { record(); return { undo: undoStack.length, redo: redoStack.length }; }, get fades() { return { fadeIn: opts.fadeIn, fadeOut: opts.fadeOut }; } }; // for tests and the console
    cleanups.push(() => { delete window.__editor; });
  }

  // ---- fades: the sermon fades in from its start and out to its end; the knobs on the waveform set them ----

  let fadeLayer = null;
  const fadeKnobs = {};
  const fadeMax = () => Math.max(0, Math.min(30, Math.floor((keptLength() / 2) * 10) / 10));

  // setFade sets a fade (opts.fadeIn or opts.fadeOut) to seconds, to a tenth, from 0 to what the sermon allows.
  function setFade(key, seconds) {
    if (!sections.length) return;
    const other = key === 'fadeIn' ? opts.fadeOut : opts.fadeIn;
    const v = Math.max(0, Math.min(Math.round(seconds * 10) / 10, fadeMax(), Math.max(keptLength() - (other || 0), 0)));
    if (!(v >= 0) || v === opts[key]) return;
    opts[key] = v;
    changed();
    drawFades();
  }

  function buildFades() {
    fadeLayer = h('div', { style: 'position:absolute;inset:0;pointer-events:none;z-index:5' });
    ws.getWrapper().appendChild(fadeLayer);
    for (const [key, label, side] of [['fadeIn', t('Fade in (s)'), 'in'], ['fadeOut', t('Fade out (s)'), 'out']]) {
      const shade = h('div', { style: 'position:absolute;top:0;height:100%;background:linear-gradient(' + (side === 'in' ? '90deg' : '270deg') +
        ', rgba(124,58,237,0.38), rgba(124,58,237,0.05));clip-path:polygon(' + (side === 'in' ? '0 0,100% 0,0 100%' : '0 0,100% 0,100% 100%') + ')' });
      const text = h('span', { style: 'position:absolute;top:2px;font-size:11px;white-space:nowrap;color:var(--fg);background:var(--panel);border-radius:4px;padding:0 4px;opacity:0.9' });
      const knob = h('div', { role: 'slider', tabindex: 0, 'aria-label': label, 'aria-valuemin': 0, title: side === 'in' ? t('Drag to fade in from the start of the sermon') : t('Drag to fade out to the end of the sermon'),
        style: 'position:absolute;top:18px;width:16px;height:16px;margin-left:-8px;border-radius:50%;background:var(--grad);border:2px solid var(--panel);box-shadow:0 1px 4px rgba(0,0,0,0.4);cursor:ew-resize;pointer-events:auto;touch-action:none' });
      const timeAt = (e) => {
        const box = ws.getWrapper().getBoundingClientRect();
        return ((e.clientX - box.left) / box.width) * duration;
      };
      knob.addEventListener('pointerdown', (down) => {
        down.preventDefault();
        down.stopPropagation();
        knob.setPointerCapture(down.pointerId);
        const move = (e) => setFade(key, side === 'in' ? timeAt(e) - first() : last() - timeAt(e));
        knob.addEventListener('pointermove', move);
        knob.addEventListener('pointerup', () => knob.removeEventListener('pointermove', move), { once: true });
      });
      knob.addEventListener('keydown', (e) => {
        const step = e.shiftKey ? 0.1 : 0.5;
        const dir = { ArrowRight: 1, ArrowUp: 1, ArrowLeft: -1, ArrowDown: -1 }[e.key];
        if (!dir) return;
        e.preventDefault();
        e.stopPropagation();
        setFade(key, (opts[key] || 0) + dir * step);
      });
      for (const el of [shade, text, knob]) fadeLayer.appendChild(el);
      fadeKnobs[key] = { shade, text, knob, side };
    }
    drawFades();
  }

  // drawFades puts the shades and knobs where the fades are: from the start of the first section, and to the
  // end of the last. Positions are in percent of the waveform, so zooming moves them along.
  function drawFades() {
    if (!fadeLayer) return;
    fadeLayer.hidden = !sections.length;
    if (!sections.length) return;
    for (const key of ['fadeIn', 'fadeOut']) {
      const { shade, text, knob, side } = fadeKnobs[key];
      const v = Math.min(opts[key] || 0, fadeMax());
      const pct = (time) => (time / duration) * 100;
      const from = side === 'in' ? first() : last() - v, to = side === 'in' ? first() + v : last();
      shade.style.left = pct(from) + '%';
      shade.style.width = pct(to - from) + '%';
      shade.hidden = v <= 0;
      knob.style.left = pct(side === 'in' ? to : from) + '%';
      knob.setAttribute('aria-valuenow', String(v));
      knob.setAttribute('aria-valuemax', String(fadeMax()));
      knob.setAttribute('aria-valuetext', t('{n} s', { n: v }));
      text.textContent = v > 0 ? (side === 'in' ? t('fade in') : t('fade out')) + ' ' + t('{n} s', { n: v }) : '';
      text.style.left = side === 'in' ? 'calc(' + pct(to) + '% + 12px)' : 'auto';
      text.style.right = side === 'out' ? 'calc(' + (100 - pct(from)) + '% + 12px)' : 'auto';
      text.hidden = v <= 0;
    }
  }

  // ---- the first section: a recording not cut yet opens with the sermon set to all of it but the
  // silence at its start and end. It is only a start: nothing is kept until something is changed. ----

  let proposed = false; // the one section is that start, untouched
  function proposeSection(silences) {
    const lead = silences.find((s) => s.start <= 0.5);
    const tail = [...silences].reverse().find((s) => s.end >= duration - 0.5);
    const start = lead ? lead.end : 0, end = tail ? tail.start : duration;
    restoring = true;
    try {
      if (end - start >= 1) addSection(start, end);
    } finally {
      restoring = false;
    }
    proposed = sections.length > 0;
    lastSent = JSON.stringify(currentSettings());
    renderSections();
    check();
  }

  // adopt makes that start the user's own: from here on it is kept like any edit, even when a change
  // leaves it as it was.
  function adopt() {
    proposed = false;
    lastSent = '';
    syncSections();
  }

  // ---- sections: the parts of the recording that make the sermon ----

  // rebuild replaces a list's rows. Removing a focused field makes the browser
  // send it a change event; edits ignore those (rebuilding), as the rows are new.
  let rebuilding = false;
  function rebuild(list, ...rows) {
    rebuilding = true;
    try { list.replaceChildren(...rows); } finally { rebuilding = false; }
  }

  // showLabel writes a section's or cut's label on its region in the waveform.
  const showLabel = (x) => x.region.setContent(x.label || undefined);
  const labelInput = (x, name) => h('input', {
    type: 'text', class: 'span-label', maxlength: 60, value: x.label || '', placeholder: t('label (optional)'), 'aria-label': t('{name} label', { name }),
    oninput: (e) => { x.label = e.target.value; showLabel(x); changed(); },
  });

  // pauseInput is the field for the silence (seconds) put in at the end of a section or the start of a cut.
  const pauseInput = (x, name, title) => h('label', { class: 'pause', title }, t('Pause'), ' ', h('input', {
    type: 'number', class: 'pause', min: 0, max: 30, step: 0.5, value: x.pause || 0, 'aria-label': t('{name} pause', { name }),
    oninput: (e) => { x.pause = Math.min(30, Math.max(0, Number(e.target.value) || 0)); syncCuts(); changed(); },
  }));

  const sectionOf = (r) => sections.find((x) => x.region === r);
  const first = () => sections[0].region.start;
  const last = () => sections[sections.length - 1].region.end;

  // addSection adds a section between two times; it returns it, or null if empty. pause is the silence put in
  // where it ends (seconds).
  function addSection(start, end, label = '', pause = 0) {
    start = Math.max(0, Math.min(start, duration));
    end = Math.max(0, Math.min(end, duration));
    if (end - start < 0.05) return null;
    const section = { region: regions.addRegion({ id: 'section-' + (++sectionSeq), start, end, color: css('--selection'), drag: true, resize: true }), label, pause };
    showLabel(section);
    sections.push(section);
    tidySections();
    return section;
  }

  // tidySections sorts the sections and makes those that overlap one (as the server does; ones that only
  // touch stay two), then shows them.
  function tidySections() {
    sections.sort((a, b) => a.region.start - b.region.start);
    for (let i = 1; i < sections.length;) {
      const a = sections[i - 1].region, b = sections[i].region;
      if (b.start >= a.end) { i++; continue; }
      if (b.end >= a.end) sections[i - 1].pause = sections[i].pause; // the pause is where the section ends
      if (b.end > a.end) a.setOptions({ end: b.end });
      if (!sections[i - 1].label) { sections[i - 1].label = sections[i].label; showLabel(sections[i - 1]); }
      b.remove();
      sections.splice(i, 1);
    }
    renderSections();
    changed();
  }

  // splitHere splits the section at the playhead into two, which touch. The pause (where the section
  // ends) goes with the second.
  function splitHere() {
    if (!ws) return;
    const now = ws.getCurrentTime();
    const x = sections.find((y) => now > y.region.start + 0.05 && now < y.region.end - 0.05);
    if (!x) return;
    const end = x.region.end, pause = x.pause || 0;
    x.pause = 0;
    x.region.setOptions({ end: now });
    const second = { region: regions.addRegion({ id: 'section-' + (++sectionSeq), start: now, end, color: css('--selection'), drag: true, resize: true }), label: '', pause };
    showLabel(second);
    sections.push(second);
    tidySections();
  }

  // showKeys lists every key of the editor, and what the mouse wheel does.
  function showKeys() {
    const row = (keys, what) => h('tr', {}, h('th', { scope: 'row' }, kbdText(keys)), h('td', {}, what));
    infoDialog({
      title: t('Keys'),
      body: [
        h('table', { class: 'keys-table' }, h('tbody', {},
          row(t('<kbd>space</kbd>'), t('Play or pause')),
          row('<kbd>p</kbd>', t('Play the whole sermon, as it will be published')),
          row('<kbd>s</kbd> / <kbd>e</kbd>', t('The section at the playhead starts or ends there')),
          row('<kbd>x</kbd>', t('Split the section at the playhead')),
          row('<kbd>c</kbd>', t('Start a cut at the playhead; again: end it there')),
          row('<kbd>[</kbd> / <kbd>]</kbd>', t('The previous or next silence')),
          row('<kbd>←</kbd> / <kbd>→</kbd>', t('1 s back or on (shift: 0.1 s)')),
          row('<kbd>Home</kbd> / <kbd>End</kbd>', t('The start or the end of the recording')),
          row('<kbd>+</kbd> / <kbd>-</kbd> / <kbd>0</kbd>', t('Zoom in, out, or all the way out')),
          row('<kbd>Delete</kbd>', t('Remove the selected section or cut')),
          row('<kbd>,</kbd> / <kbd>.</kbd>', t('Move the edge of the selected one that is nearest the playhead by 0.1 s (shift: 0.01 s)')),
          row('<kbd>Esc</kbd>', t('Let go of the selection, or of a cut being made')),
          row('<kbd>ctrl</kbd>+<kbd>z</kbd> / <kbd>ctrl</kbd>+<kbd>shift</kbd>+<kbd>z</kbd>', t('Undo or redo')),
          row('<kbd>?</kbd>', t('This list')))),
        h('p', { class: 'muted' }, kbdText(wheelZoomNeedsKey()
          ? t('<kbd>ctrl</kbd>+scroll zooms at the mouse, <kbd>shift</kbd>+scroll moves along (Settings › Waveform).')
          : t('Scroll zooms at the mouse, <kbd>shift</kbd>+scroll moves along (Settings › Waveform).'))),
      ],
    });
  }

  // ---- the selected section or cut: clicked on the waveform, or its row in use; the row and the region are marked ----

  let selected = null;
  function select(x) {
    selected = x;
    markSelected();
  }
  function markSelected() {
    if (selected && !sections.includes(selected) && !cuts.includes(selected)) selected = null;
    for (const y of [...sections, ...cuts]) {
      const on = y === selected;
      if (y.row) y.row.classList.toggle('selected', on);
      if (!y.region.element) continue;
      y.region.element.style.boxShadow = on ? 'inset 0 0 0 2px var(--accent)' : '';
      // A cut is always above the section it is in, whichever was drawn last, so a click on it gets the cut.
      y.region.element.style.zIndex = sections.includes(y) ? '1' : '2';
    }
  }

  function removeSelected() {
    if (!selected) return;
    if (sections.includes(selected)) removeSection(selected);
    else removeCut(selected);
    selected = null;
  }

  // nudge moves the edge of the selected section or cut that is nearest the playhead by seconds (0.1 s, shift 0.01 s).
  function nudge(seconds) {
    if (!selected || !ws) return;
    const r = selected.region, now = ws.getCurrentTime();
    const which = Math.abs(now - r.start) <= Math.abs(now - r.end) ? 'start' : 'end';
    const time = Math.max(0, Math.min(r[which] + seconds, duration));
    if (which === 'start' ? time > r.end - 0.05 : time < r.start + 0.05) return;
    r.setOptions({ [which]: time });
    if (sections.includes(selected)) tidySections();
    else { sortCuts(); renderCuts(); changed(); }
  }

  function removeSection(x) {
    x.region.remove();
    sections.splice(sections.indexOf(x), 1);
    renderSections();
    changed();
  }

  // setEdge moves the start or end of a section to t; false if it would be empty.
  function setEdge(x, which, time) {
    const r = x.region;
    if (which === 'start' ? time > r.end - 0.05 : time < r.start + 0.05) return false;
    r.setOptions({ [which]: time });
    tidySections();
    return true;
  }

  // renderSections rebuilds the list of sections; syncSections only refreshes its
  // values. With none, an empty row makes the first one from typed times.
  function renderSections() {
    if (!sections.length) {
      const startIn = h('input', { type: 'text', class: 'time', placeholder: t('start'), 'aria-label': t('{name} from', { name: t('Section') }) });
      const endIn = h('input', { type: 'text', class: 'time', placeholder: t('end'), 'aria-label': t('{name} to', { name: t('Section') }) });
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
      rebuild(sectionList, h('div', { class: 'section' }, h('strong', {}, t('Section')),
        h('label', {}, t('From'), ' ', startIn), h('label', {}, t('to'), ' ', endIn)));
      syncSections();
      syncCuts();
      return;
    }
    rebuild(sectionList, ...sections.map((x, i) => {
      const r = x.region;
      const name = sections.length > 1 ? t('Section {n}', { n: i + 1 }) : t('Section');
      const startIn = x.startIn = h('input', { type: 'text', class: 'time', 'aria-label': t('{name} from', { name }) });
      const endIn = x.endIn = h('input', { type: 'text', class: 'time', 'aria-label': t('{name} to', { name }) });
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
        h('label', {}, t('From'), ' ', x.startIn), h('label', {}, t('to'), ' ', x.endIn), x.info,
        labelInput(x, name),
        x === sections[sections.length - 1] ? '' : pauseInput(x, name, t('Silence put in after this section, in seconds')),
        h('button', { title: t('Play this section'), onclick: () => playFor(r.start, r.end - r.start) }, t('Play')),
        i < sections.length - 1 ? h('button', { title: t('Play 3 s before the end of this section and 3 s from the start of the next, as it will sound'), onclick: () => {
          const next = sections[i + 1].region, from = Math.max(r.end - 3, 0);
          playFor(from, next.start + 3 - from, true);
        } }, t('Hear join')) : '',
        h('button', { onclick: () => removeSection(x) }, t('Remove')));
      x.row.addEventListener('focusin', () => select(x));
      x.row.addEventListener('pointerdown', () => select(x));
      return x.row;
    }));
    markSelected();
    syncSections();
    syncCuts();
  }

  function syncSections() {
    for (const x of sections) {
      const r = x.region;
      if (document.activeElement !== x.startIn) x.startIn.value = formatTime(r.start);
      if (document.activeElement !== x.endIn) x.endIn.value = formatTime(r.end);
      x.info.textContent = t('length {time}', { time: formatTime(r.end - r.start, 1) });
    }
    sectionHint.textContent = !sections.length
      ? t('Drag on the waveform to select the sermon, or set its start and end at the playhead (s, e).')
      : proposed
        ? t('The sermon is set to the recording without the silence at its start and end. Move its edges, or set its start and end at the playhead (s, e).')
        : t('Drag on an empty part of the waveform to add another section. Parts between sections are left out.');
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
  function addCut(start, end, label = '', pause = 0) {
    start = Math.max(0, Math.min(start, duration));
    end = Math.max(0, Math.min(end, duration));
    if (end - start < 0.05) return null;
    const cut = { region: regions.addRegion({ id: 'cut-' + (++cutSeq), start, end, color: css('--cut'), drag: true, resize: true }), label, pause };
    showLabel(cut);
    cuts.push(cut);
    sortCuts();
    renderCuts();
    changed();
    return cut;
  }

  // ---- cutting while listening: c (or Cut out here) starts a cut at the playhead, which then follows the
  // playhead (played or moved) until c (End the cut here) ends it there; Escape lets it go. The open cut is
  // only drawn: it is not one of the cuts, so playback does not jump over it. ----

  let openCut = null; // { start, region }

  function addCutHere() {
    if (!ws || !sections.length) return;
    if (openCut) { closeCut(); return; }
    const start = Math.min(Math.max(ws.getCurrentTime(), first()), last() - 0.05);
    const region = regions.addRegion({ id: 'cut-open', start, end: start + 0.05, color: css('--cut'), drag: false, resize: false });
    if (region.element) region.element.style.pointerEvents = 'none';
    openCut = { start, region };
    showOpenCut();
  }

  function followOpenCut(now) {
    if (!openCut) return;
    const a = Math.min(openCut.start, now), b = Math.max(openCut.start, now);
    openCut.region.setOptions({ start: a, end: Math.max(b, a + 0.05) });
  }

  function closeCut() {
    const { start, region } = openCut, now = ws.getCurrentTime();
    region.remove();
    openCut = null;
    showOpenCut();
    if (Math.abs(now - start) >= 0.05) addCut(Math.min(start, now), Math.max(start, now));
  }

  // cancelCut lets an open cut go; false if there was none.
  function cancelCut() {
    if (!openCut) return false;
    openCut.region.remove();
    openCut = null;
    showOpenCut();
    return true;
  }

  function showOpenCut() {
    if (!addCutButton) return;
    addCutButton.textContent = openCut ? t('End the cut here') : t('Cut out here');
    addCutButton.title = openCut ? t('The cut ends at the playhead (c); Escape lets it go') : t('Start a cut at the playhead; press again where it ends (c)');
    addCutButton.classList.toggle('primary', Boolean(openCut));
    syncCuts();
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
      c.startIn = h('input', { type: 'text', class: 'time', 'aria-label': t('Cut {n} from', { n: i + 1 }) });
      c.endIn = h('input', { type: 'text', class: 'time', 'aria-label': t('Cut {n} to', { n: i + 1 }) });
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
        const now = ws.getCurrentTime();
        if (which === 'start' && now < r.end - 0.05) r.setOptions({ start: now });
        if (which === 'end' && now > r.start + 0.05) r.setOptions({ end: now });
        sortCuts();
        renderCuts();
        changed();
      };
      c.row = h('div', { class: 'cut' },
        h('strong', {}, t('Cut {n}', { n: i + 1 })),
        h('label', {}, t('From'), ' ', c.startIn), h('label', {}, t('to'), ' ', c.endIn), c.info,
        labelInput(c, t('Cut {n}', { n: i + 1 })),
        pauseInput(c, t('Cut {n}', { n: i + 1 }), t('Silence put in where the cut is, in seconds')),
        h('button', { title: t('The cut starts at the playhead'), onclick: here('start') }, t('Set start')),
        h('button', { title: t('The cut ends at the playhead'), onclick: here('end') }, t('Set end')),
        h('button', { title: t('Play 3 s before and after the cut, as it will sound'), onclick: () => {
          const from = Math.max(r.start - 3, 0);
          playFor(from, r.end + 3 - from, true);
        } }, t('Hear join')),
        h('button', { onclick: () => removeCut(c) }, t('Remove')),
        c.warn = h('p', { class: 'cut-warn', role: 'status' }));
      c.row.addEventListener('focusin', () => select(c));
      c.row.addEventListener('pointerdown', () => select(c));
      return c.row;
    }));
    markSelected();
    syncCuts();
  }

  function syncCuts() {
    for (const c of cuts) {
      const r = c.region;
      if (document.activeElement !== c.startIn) c.startIn.value = formatTime(r.start);
      if (document.activeElement !== c.endIn) c.endIn.value = formatTime(r.end);
      // How much of the cut is in a section: none, some or all. A cut outside
      // the sections is allowed (what is outside is left out anyway), but it does nothing.
      const inside = sections.reduce((n, x) => n + Math.max(0, Math.min(r.end, x.region.end) - Math.max(r.start, x.region.start)), 0);
      const none = inside < 0.001, partly = !none && inside < r.end - r.start - 0.001;
      c.row.classList.toggle('outside', none);
      c.row.classList.toggle('partly', partly);
      c.info.textContent = t('length {time}', { time: formatTime(r.end - r.start, 1) });
      const n = cuts.indexOf(c) + 1;
      c.warn.textContent = none ? '⚠ ' + t('Cut {n} does not have any effect as it is not in an included section', { n })
        : partly ? '⚠ ' + t('Cut {n} is only partially part of a section', { n }) : '';
      c.warn.hidden = !c.warn.textContent;
    }
    if (!sections.length && openCut) cancelCut();
    cutHint.textContent = !sections.length ? t('Select the sermon first.')
      : openCut ? t('Play or move the playhead to where the cut ends, then press End the cut here (c). Escape lets it go.')
        : cuts.length ? '' : t('Nothing cut out. Press the button (or c) where a cut starts and again where it ends, or shift-drag on the waveform.');
    if (addCutButton) addCutButton.disabled = !sections.length;
    const parts = sections.length > 1 ? tn(sections.length, '{n} section', '{n} sections') : '';
    const pauses = activePauses();
    const what = [parts, cuts.length ? tn(cuts.length, '{n} cut', '{n} cuts') : '', pauses.length ? tn(pauses.length, '{n} pause', '{n} pauses') : ''].filter(Boolean).join(' ' + t('and') + ' ');
    lengthOut.textContent = sections.length ? t('Length {time}', { time: formatTime(keptLength() + pauses.reduce((n, p) => n + p.seconds, 0), 1) }) + (what ? ` (${what})` : '') : '';
    drawPauseMarks(pauses);
    drawFades();
  }

  // activePauses are the pauses that take effect: where a section ends (but the last), and where a cut
  // starts inside the sermon.
  function activePauses() {
    if (!sections.length) return [];
    const list = [];
    sections.forEach((x, i) => { if (i < sections.length - 1 && x.pause > 0) list.push({ at: x.region.end, seconds: x.pause }); });
    for (const c of cuts) if (c.pause > 0 && c.region.start > first() && c.region.start < last()) list.push({ at: c.region.start, seconds: c.pause });
    return list.sort((a, b) => a.at - b.at);
  }

  // drawPauseMarks puts a marker on the waveform where each pause is.
  function drawPauseMarks(pauses) {
    if (!regions) return;
    const signature = JSON.stringify(pauses);
    if (signature === markSignature) return;
    markSignature = signature;
    for (const r of regions.getRegions()) if (r.id.startsWith('pause-')) r.remove();
    pauses.forEach((p, i) => {
      const r = regions.addRegion({ id: 'pause-' + i, start: p.at, content: h('span', { style: 'white-space: nowrap; padding: 0 0.3rem' }, '⏸ ' + t('{n} s', { n: p.seconds })), color: 'rgba(124, 58, 237, 0.9)', drag: false, resize: false });
      if (r.element) r.element.style.pointerEvents = 'none';
    });
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

  // spanOf is a section or cut as it is kept: its times and its label, if any.
  const spanOf = (x) => ({ start: x.region.start, end: x.region.end, ...(x.label ? { label: x.label } : {}), ...(x.pause > 0 ? { pause: x.pause } : {}) });

  // currentSettings is what is on the page, or null while there is nothing (valid) to keep.
  function currentSettings() {
    if (!sections.length || restoring) return null;
    const secs = sections.map(spanOf);
    const list = cuts.map(spanOf);
    if (secs.some((x) => x.end - x.start < 0.05)) return null;
    if (list.some((c) => c.end - c.start < 0.05)) return null; // one outside the sermon is fine
    if (!(opts.fadeIn >= 0) || !(opts.fadeOut >= 0)) return null;
    if ([opts.noiseReduction, opts.roomSound, opts.targetLufs, opts.truePeak].some(Number.isNaN)) return null;
    return { sections: secs, cuts: list, fadeIn: opts.fadeIn, fadeOut: opts.fadeOut, sound: Object.fromEntries(SOUND_KEYS.map((k) => [k, opts[k]])) };
  }

  // persistAdjusted keeps what is on the page as the adjustments.
  async function persistAdjusted() {
    const state = currentSettings();
    if (!state || JSON.stringify(state) === lastSent) return;
    saveNote.textContent = t('Keeping…');
    try {
      sermon = await api('PUT', 'api/sermon?path=' + q(path), state);
      lastSent = JSON.stringify(state);
      saveNote.textContent = t('Changes kept.');
    } catch (e) {
      saveNote.textContent = t('Not kept: {error}', { error: e.message });
    }
    renderVersion();
  }
  const saveSoon = debounce(() => { if (alive) persistAdjusted(); }, 700);

  // applySettings puts settings on the page: the sections, the cuts and the fades.
  function applySettings(set, { show = true } = {}) {
    restoring = true;
    try {
      for (const x of [...sections, ...cuts]) x.region.remove();
      sections.length = cuts.length = 0;
      for (const x of set.sections) addSection(x.start, x.end, x.label, x.pause);
      for (const c of set.cuts) addCut(c.start, c.end, c.label, c.pause);
      if (sections.length && show) reveal(first());
      for (const key of ['fadeIn', 'fadeOut']) opts[key] = set[key];
      if (set.sound) {
        Object.assign(opts, set.sound);
        redrawProcessing();
      }
    } finally {
      restoring = false;
    }
    renderSections();
    renderCuts();
    check();
  }

  // ---- undo and redo: the edit on the waveform (sections, cuts, their labels and pauses, the fades) and the
  // processing, not the tags; in a field, ctrl+z is the field's own ----

  const undoStack = [], redoStack = [];
  let present = null; // the edit as it was last recorded
  let pointerHeld = false; // a drag is going on: it becomes one step when it ends

  // snapshot is the edit on the page, also when there is no section.
  const snapshot = () => JSON.stringify({ sections: sections.map(spanOf), cuts: cuts.map(spanOf), fadeIn: opts.fadeIn || 0, fadeOut: opts.fadeOut || 0,
    sound: Object.fromEntries(SOUND_KEYS.map((k) => [k, opts[k]])) });

  // record makes the edit as it is now a step that can be undone, if it changed.
  function record() {
    recordSoon.cancel();
    if (pointerHeld || restoring || present === null) return;
    const now = snapshot();
    if (now === present) return;
    undoStack.push(present);
    if (undoStack.length > 200) undoStack.shift();
    redoStack.length = 0;
    present = now;
    showHistory();
  }
  const recordSoon = debounce(() => { if (alive) record(); }, 400);
  window.addEventListener('pointerdown', onPointerDown, true);
  window.addEventListener('pointerup', onPointerUp, true);
  cleanups.push(() => {
    window.removeEventListener('pointerdown', onPointerDown, true);
    window.removeEventListener('pointerup', onPointerUp, true);
  });
  function onPointerDown() { pointerHeld = true; }
  function onPointerUp() { pointerHeld = false; recordSoon(); }

  function step(from, to) {
    record(); // a change not recorded yet comes first
    if (!from.length) return;
    to.push(present);
    present = from.pop();
    applySettings(JSON.parse(present), { show: false });
    if (proposed) adopt();
    showHistory();
    persistAdjusted(); // putting settings back is not kept by itself
  }
  const undo = () => step(undoStack, redoStack);
  const redo = () => step(redoStack, undoStack);

  function showHistory() {
    undoButton.disabled = !undoStack.length;
    redoButton.disabled = !redoStack.length;
    keepHistory();
  }

  // The steps are kept in this browser, so they outlive a reload: for the last recordings edited, and only
  // while they lead to the edit the page opens with. Changed elsewhere (another browser or computer), they
  // would undo into something else, and are dropped.
  const historyKey = 'history:' + currentTenant() + ':' + path;
  function loadHistory() {
    try {
      const kept = JSON.parse(localStorage.getItem(historyKey));
      if (kept && kept.present === present) {
        undoStack.push(...kept.undo);
        redoStack.push(...kept.redo);
        showHistory();
      }
      // Only the last 20 recordings' steps are kept.
      const all = Object.keys(localStorage).filter((k) => k.startsWith('history:'))
        .map((k) => [k, JSON.parse(localStorage.getItem(k))?.at || 0]).sort((a, b) => b[1] - a[1]);
      for (const [k] of all.slice(20)) localStorage.removeItem(k);
    } catch { /* none kept, or not readable: start without */ }
  }
  // keepHistory is called with every step (they come at most every 0.4 s), so a reload loses none.
  function keepHistory() {
    if (!alive || present === null) return;
    try {
      localStorage.setItem(historyKey, JSON.stringify({ present, undo: undoStack, redo: redoStack, at: Date.now() }));
    } catch { /* the browser's storage is full: undo still works until the page is left */ }
  }

  // ctrl+z undoes, ctrl+shift+z and ctrl+y redo; in a field they are the field's own.
  function onUndoKey(e) {
    if (typing(e) || !(e.ctrlKey || e.metaKey) || e.altKey || document.querySelector('dialog[open]')) return;
    const key = e.key.toLowerCase();
    if (key === 'z' && !e.shiftKey) undo();
    else if ((key === 'z' && e.shiftKey) || key === 'y') redo();
    else return;
    e.preventDefault();
  }
  document.addEventListener('keydown', onUndoKey);
  cleanups.push(() => document.removeEventListener('keydown', onUndoKey));

  const describe = (set) => [
    set.sections.map((x) => t('{from} to {to}', { from: formatTime(x.start, 0), to: formatTime(x.end, 0) })).join(' ' + t('and') + ' '),
    tn(set.cuts.length, '{n} cut', '{n} cuts'),
    ...(set.sections.some((x) => x.pause > 0) || set.cuts.some((x) => x.pause > 0) ? [t('with pauses')] : []),
    set.fadeIn || set.fadeOut ? t('fade {in} s in, {out} s out', { in: set.fadeIn, out: set.fadeOut }) : t('no fades'),
  ].join(', ');

  // renderVersion shows in the bar whether the edit is saved; the versions themselves are in its tooltip.
  function renderVersion() {
    const { saved, adjusted } = sermon;
    const at = sermon.savedAt ? ' ' + formatDateTime(sermon.savedAt) : '';
    versionOut.textContent = adjusted ? t('Adjusted, not saved') : saved ? t('Saved') + at : t('Nothing is saved yet.');
    versionOut.className = adjusted ? 'unsaved' : '';
    versionOut.title = [
      saved ? t('Saved') + at + ': ' + describe(saved) : t('Nothing is saved yet.'),
      adjusted ? t('Adjusted, not saved:') + ' ' + describe(adjusted) : '',
    ].filter(Boolean).join('\n');
    revertButton.disabled = !adjusted || !saved;
    renderOutputs();
  }

  // revertVersion drops the adjustments and goes back to the saved version, after asking.
  async function revertVersion() {
    if (!sermon.adjusted || !sermon.saved) return;
    const ok = await confirmDialog({
      title: t('Go back to the saved version?'),
      body: [
        h('p', {}, t('Your adjustments are discarded:')), h('ul', {}, h('li', {}, describe(sermon.adjusted))),
        h('p', {}, t('Back to the saved version:')), h('ul', {}, h('li', {}, describe(sermon.saved))),
      ],
      confirm: t('Revert'),
      danger: true,
    });
    if (!ok) return;
    try {
      sermon = await api('POST', 'api/sermon/revert?path=' + q(path));
      lastSent = JSON.stringify(sermon.saved);
      applySettings(sermon.saved);
      record();
      saveNote.textContent = t('Back to the saved version.');
    } catch (e) {
      saveNote.textContent = t('Not reverted: {error}', { error: e.message });
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
  // jump over the cuts even when the whole recording is the Play button's choice.
  function playFor(from, seconds, skipCuts = false) {
    ws.setTime(from);
    stopAt = from + seconds;
    skipNow = skipCuts;
    ws.play();
  }

  // playSelection plays the sermon as it will be published: all the sections, without the gaps
  // between them and the cuts, with the pauses.
  function playSelection() {
    if (sections.length) playFor(first(), last() - first(), true);
  }

  function hearStart() {
    if (sections.length) playFor(first(), Math.min(5, last() - first()), true);
  }

  function hearEnd() {
    if (sections.length) playFor(Math.max(last() - 5, first()), Math.min(5, last() - first()), true);
  }

  // ---- the metadata written into the files, and the processing that makes them ----

  // metadataPanel is the tags: title, artist and album, in every file.
  function metadataPanel() {
    const tag = (key, label) => h('label', { class: 'tag' }, h('span', { class: 'name' }, label), h('input', {
      type: 'text', name: key, value: tags[key], maxlength: 200,
      oninput: (e) => { tags[key] = e.target.value; changed(); },
    }));
    return h('div', { class: 'panel' },
      info.tags
        ? h('div', { class: 'options' }, tag('title', t('Title')), tag('artist', t('Artist')), tag('album', t('Album')))
        : h('span', { class: 'muted' }, info.tagsError ? tr(info.tagsError) : t('No tags')),
      h('p', { class: 'muted' }, t('Written into the FLAC, MP3 and Opus files. A field left empty is left out.')));
  }

  // redrawProcessing shows the processing choices again, after they were set from outside (a revert).
  function redrawProcessing() {
    const next = processingPanel();
    processingEl.replaceWith(next);
    processingEl = next;
    showChannelWarning();
    showDefaults();
  }

  // ---- the church's default processing: this recording can go back to it, or make its own the default ----

  function showDefaults() {
    const same = sameAsDefaults();
    defaultsNote.textContent = same ? t('The same as the church\'s defaults.') : t('Not the same as the church\'s defaults.');
    useDefaultsButton.disabled = makeDefaultsButton.disabled = same;
  }

  function useDefaults() {
    record(); // its own step, also right after another change
    const d = config.defaults.start;
    for (const k of SOUND_KEYS) opts[k] = d[k];
    for (const k of ['fadeIn', 'fadeOut']) {
      if (sections.length) setFade(k, d[k]); // as long as the sermon allows
      else opts[k] = d[k];
    }
    redrawProcessing();
    changed();
  }

  async function makeDefaults() {
    const ok = await confirmDialog({ title: t('Make these the defaults'), confirm: t('Make them the defaults'),
      body: t('New recordings start with this processing and these fades. Recordings that have their own keep it.') });
    if (!ok) return;
    const levels = { targetLufs: opts.targetLufs, mp3Bitrate: opts.mp3Bitrate, opusBitrate: opts.opusBitrate };
    const p = {
      ...config.defaults.processing,
      stereo: opts.stereo, noiseReduction: Math.round(opts.noiseReduction), roomSound: opts.roomSound,
      leveling: opts.leveling, compression: opts.compression, truePeak: opts.truePeak,
      fadeIn: opts.fadeIn, fadeOut: opts.fadeOut,
      [opts.stereo ? 'stereoLevels' : 'mono']: levels,
    };
    try {
      await api('PUT', 'api/processing', { processing: p });
      Object.assign(config, await api('GET', 'api/config'));
      showDefaults();
    } catch (e) {
      defaultsNote.textContent = t('Not saved: {error}', { error: e.message });
    }
  }

  // processingPanel is how the sermon is made into files: channels, noise, levels, loudness and bitrates.
  // The choices are kept with the sermon.
  function processingPanel() {
    const field = (label, input) => h('label', {}, h('span', { class: 'name' }, label), input);
    const num = (key, label, step, min, max) => field(label, h('input', {
      type: 'number', name: key, value: opts[key], step, min, max,
      oninput: (e) => { opts[key] = e.target.value === '' ? NaN : Number(e.target.value); changed(); },
    }));
    const text = (key, label) => field(label, h('input', {
      type: 'text', name: key, value: opts[key],
      oninput: (e) => { opts[key] = e.target.value; changed(); },
    }));
    const choice = (key, label, values) => field(label, h('select', { name: key, onchange: (e) => { opts[key] = e.target.value; changed(); } },
      values.map(([value, text]) => h('option', { value, selected: opts[key] === value }, text))));
    const group = (title, ...children) => h('div', { class: 'pgroup' }, h('strong', {}, title), ...children);
    const hint = (text) => h('p', { class: 'muted hint' }, text);
    // Mono or stereo: a switch between the two, not a box to tick.
    const stereo = h('input', {
      type: 'checkbox', role: 'switch', name: 'stereo', checked: opts.stereo, 'aria-label': t('Stereo'),
      onchange: (e) => {
        followChannels(e.target.checked);
        opts.stereo = e.target.checked;
        showChannelWarning();
        changed();
      },
    });
    const channels = h('div', { class: 'channels' },
      h('label', { class: 'switch' }, h('span', { class: 'side off' }, t('Mono')), stereo, h('span', { class: 'track', 'aria-hidden': 'true' }), h('span', { class: 'side on' }, t('Stereo'))));
    const roomValue = h('output', { class: 'muted' }, `${opts.roomSound}%`);
    const room = field(t('Keep room sound'), h('span', { class: 'range' }, h('input', {
      type: 'range', name: 'roomSound', min: 0, max: 100, step: 5, value: opts.roomSound,
      oninput: (e) => { opts.roomSound = Number(e.target.value); roomValue.textContent = `${opts.roomSound}%`; changed(); },
    }), roomValue));
    const strengths = (off, a, b, c) => [['off', off], ['gentle', a], ['normal', b], ['strong', c]];

    const sample = h('audio', { class: 'player', controls: true, hidden: true, 'aria-label': t('The sample') });
    const sampleStatus = h('span', { class: 'muted', role: 'status' });
    const sampleButton = h('button', { type: 'button', name: 'sample', onclick: async () => {
      sampleButton.disabled = true;
      sample.hidden = true;
      sampleStatus.className = 'muted';
      sampleStatus.textContent = t('Making the sample…');
      try {
        const body = Object.fromEntries(SOUND_KEYS.map((k) => [k, opts[k]]));
        const resp = await fetch(forTenant('api/sample?path=' + q(path) + '&at=' + (ws ? ws.getCurrentTime() : 0)), {
          method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body),
        });
        if (!resp.ok) throw new Error(tr((await resp.json().catch(() => ({}))).error || `${resp.status} ${resp.statusText}`));
        URL.revokeObjectURL(sample.src);
        sample.src = URL.createObjectURL(await resp.blob());
        sample.hidden = false;
        sampleStatus.textContent = '';
        sample.play().catch(() => {});
      } catch (e) {
        sampleStatus.className = 'error';
        sampleStatus.textContent = t('Could not make the sample: {error}', { error: e.message });
      } finally {
        sampleButton.disabled = false;
      }
    } }, t('Hear a sample'));

    return h('div', { class: 'panel processing' },
      group(t('Defaults'), h('p', {}, useDefaultsButton, ' ', makeDefaultsButton), defaultsNote),
      group(t('Channels'), channels, channelWarning),
      group(t('Noise'),
        num('noiseReduction', t('Noise reduction (dB)'), 1, 0, 40),
        room,
        hint(t('Taking out noise also takes out some of the room\'s own sound, such as its reverb. Less reduction, or more room sound kept, sounds more natural; more sounds cleaner.'))),
      group(t('Levels'),
        choice('leveling', t('Leveling'), strengths(t('Off'), t('Gentle'), t('Normal'), t('Strong'))),
        choice('compression', t('Compression'), [['off', t('Off')], ['light', t('compression|Light')], ['normal', t('Normal')], ['strong', t('Strong')]]),
        hint(t('Leveling brings quiet speech and loud parts closer. Compression narrows the loudness range further: light is usually enough.'))),
      group(t('Loudness'),
        num('targetLufs', t('Target loudness (LUFS)'), 0.5, -70, -5),
        num('truePeak', t('Max true peak (dBTP)'), 0.1, -20, 0),
        hint(t('Podcasts are usually -19 LUFS in mono and -16 in stereo. The peaks are kept under the true peak.'))),
      group(t('Encoding'), text('mp3Bitrate', t('MP3 bitrate')), text('opusBitrate', t('Opus bitrate'))),
      group(t('Try the settings'),
        h('p', {}, sampleButton, ' ', sampleStatus),
        sample,
        hint(t('Makes 30 seconds from the playhead with these settings, to listen to. The cuts, pauses and fades are not applied.'))));
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
      sections: sections.map(spanOf),
      cuts: sections.length ? cuts.map(spanOf) : [],
    };
  }

  // ---- check and run ----
  const check = debounce(async () => {
    if (!alive) return;
    const req = request();
    if ([req.fadeIn, req.fadeOut, req.noiseReduction, req.targetLufs, req.truePeak].some(Number.isNaN)) {
      showCheck({ error: t('Fill in all numbers.') });
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
    if (proposed && !restoring) adopt();
    if (!restoring) recordSoon();
    showDefaults();
    check();
    saveSoon();
  }

  function showCheck(res) {
    lastCheck = res;
    checkOut.replaceChildren(
      res.error ? h('p', { class: 'error' }, tr(res.error)) : '',
      ...(res.notices || []).map((n) => h('p', { class: 'notice' }, tr(n))));
    renderOutputs();
  }

  function busy() { return job && (job.state === 'queued' || job.state === 'running'); }

  // ---- output: the formats, what is there, and what Save makes ----
  const FORMATS = [['flac', 'FLAC'], ['mp3', 'MP3'], ['opus', 'Opus']];

  // outputState is a format's file: there or not, and whether Save would make
  // it again (stale: made from other settings; unknown: no record).
  function outputState(format) {
    const file = info.outputs.find((o) => o.format === format) || null;
    const st = (lastCheck.outputs || {})[format];
    return { file, stale: Boolean(file && st && (st.stale || !st.known)), unknown: Boolean(file && st && !st.known) };
  }

  // toMake is what Save makes: the chosen formats that are missing or out
  // of date; toDelete what it deletes: the files of the formats not chosen.
  const toMake = () => FORMATS.map(([f]) => f).filter((f) => opts[f] && (!outputState(f).file || outputState(f).stale));
  const toDelete = () => FORMATS.map(([f]) => f).filter((f) => !opts[f] && outputState(f).file);
  const labels = (list) => list.map((f) => FORMATS.find(([x]) => x === f)[1]).join(', ');

  function renderOutputs() {
    pills.replaceChildren(...FORMATS.map(([format, label]) => {
      const { file, stale, unknown } = outputState(format);
      const chosen = Boolean(opts[format]);
      const note = !file ? (chosen ? t('will be made') : t('not made'))
        : !chosen ? t('will be deleted')
          : stale ? t('out of date, will be made again') : t('up to date');
      return h('div', { 'data-format': format, class: 'pill' + (file ? ' exists' : '') + (chosen ? ' chosen' : '') + (stale ? ' stale' : '') },
        h('button', {
          class: 'pick', 'aria-pressed': String(chosen), title: chosen ? t('Leave {format} out', { format: label }) : t('Make {format} too', { format: label }),
          onclick: () => { opts[format] = !opts[format]; renderOutputs(); },
        }, h('span', { class: 'box', 'aria-hidden': 'true' }, chosen ? '☑' : '☐'), ' ', label, file ? ' ✓' : ''),
        h('span', { class: 'state' + (file && !chosen ? ' delete' : chosen && (stale || !file) ? ' will' : '') }, note));
    }));
    const make = toMake(), del = toDelete(), keep = unsaved();
    const blocked = (make.length > 0 || keep) && Boolean(lastCheck.error); // deleting does not need a valid edit
    outputNote.textContent = blocked ? ''
      : make.length || del.length || keep
        ? t('Save {what}.', { what: [keep ? t('keeps the edit') : '', make.length ? t('makes: {formats}', { formats: labels(make) }) : '', del.length ? t('deletes: {formats}', { formats: labels(del) }) : '']
          .filter(Boolean).join(', ') })
        : t('Saved, and everything chosen is up to date.');
    saveButton.disabled = blocked || busy() || !(make.length || del.length || keep);
  }

  // unsaved: the edit on the page is not the saved version.
  const unsaved = () => Boolean(sermon.adjusted) || (!sermon.saved && sections.length > 0);

  // save makes the edit the saved version, then deletes the files of the
  // formats not chosen (after asking) and makes the chosen ones that are
  // missing or out of date. The edit is saved even when the files cannot be
  // made now: the recordings page shows them as out of date.
  async function save() {
    if (proposed) adopt(); // the start the page made has not been kept yet
    await persistAdjusted();
    const make = toMake(), del = toDelete();
    const replaced = make.map(outputState).filter((x) => x.file).map((x) => x.file.name);
    const deleted = del.map((f) => outputState(f).file.name);
    if (deleted.length) {
      const ok = await confirmDialog({
        title: t('Delete files?'),
        body: [h('p', {}, t('These formats are not chosen any more, so their files are deleted:')),
          h('ul', {}, deleted.map((n) => h('li', {}, n)))],
        confirm: t('Save and delete'),
        danger: true,
      });
      if (!ok) return;
    }
    saveButton.disabled = true;
    try {
      if (unsaved()) {
        sermon = await api('POST', 'api/sermon/save?path=' + q(path));
        saveNote.textContent = t('Saved.');
        renderVersion();
      }
    } catch (e) {
      saveNote.textContent = t('Not saved: {error}', { error: e.message });
      renderOutputs();
      return;
    }
    try {
      if (del.length) {
        const inf = await api('POST', 'api/outputs/remove?path=' + q(path) + del.map((f) => '&format=' + f).join(''));
        info.outputs = inf.outputs;
        renderOutputs();
        check();
      }
      if (!make.length) return;
      const req = request(replaced.length > 0);
      for (const [f] of FORMATS) req[f] = make.includes(f);
      job = await api('POST', 'api/run?path=' + q(path), req);
      showJob(job);
      follow(job.id);
    } catch (e) {
      showCheck({ ...lastCheck, error: t('Saved, but the files were not made: {error}', { error: e.message }) });
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
    const title = { queued: t('Waiting for another run to finish…'), running: t('Processing…'), done: t('Done'), failed: t('Failed'), cancelled: t('Cancelled') }[j.state];
    const steps = j.steps.map((s, i) => {
      const last = i === j.steps.length - 1;
      return h('li', { class: last && running ? 'current' : last && j.state === 'failed' ? 'failed' : '' }, tr(s));
    });
    const log = h('pre', { class: 'log' }, j.log.join('\n'));
    jobPanel.replaceChildren(h('div', { class: 'panel' },
      h('h2', {}, title),
      h('ul', { class: 'steps' }, steps),
      running ? h('progress', { max: 1, value: j.progress }) : '',
      j.warnings.length ? h('div', {}, j.warnings.map((w) => h('p', { class: 'warn' }, '⚠ ' + tr(w)))) : '',
      j.error ? h('p', { class: 'error' }, tr(j.error)) : '',
      j.state === 'cancelled' ? h('p', {}, t('Cancelled. Nothing half-written was left behind.')) : '',
      j.state === 'done' ? h('p', {}, t('Done. Listen to and download the files under Recordings.')) : '',
      h('details', { open: running }, h('summary', {}, t('Log')), log),
      running ? h('button', { onclick: cancel }, t('Cancel')) : ''));
    log.scrollTop = log.scrollHeight;
    renderOutputs();
  }

  async function cancel() {
    try {
      job = await api('POST', 'api/jobs/' + job.id + '/cancel');
      showJob(job);
    } catch (e) {
      jobPanel.prepend(h('p', { class: 'error' }, tr(e.message)));
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
    waveSection.replaceChildren(h('p', {}, t('Nothing to cut yet. Upload a recording first.')));
  }
  renderVersion();
  renderOutputs();
  check();
  return teardown;
}

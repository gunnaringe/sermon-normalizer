// The details of a date folder in the folder list: what a sermon has (info,
// play, download, make a missing format), or the warning for a recording that
// has not been processed.
import { api, q, h, formatSize, formatTime, forTenant } from './util.js';

const FORMATS = [['flac', 'FLAC'], ['mp3', 'MP3'], ['opus', 'Opus']];
const fileURL = (f, download) => forTenant('api/file?' + (download ? 'download=1&' : '') + 'path=' + q(f.path));
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// keptSeconds is what the saved version lasts: its sections without the cuts.
function keptSeconds(s) {
  const len = (x) => x.end - x.start;
  const inside = (c) => s.sections.reduce((n, x) => n + Math.max(0, Math.min(c.end, x.end) - Math.max(c.start, x.start)), 0);
  return s.sections.reduce((n, x) => n + len(x), 0) - s.cuts.reduce((n, c) => n + inside(c), 0);
}

// the format the one player plays: what every browser handles first
const PLAY_ORDER = ['mp3', 'opus', 'flac'];

const original = (info) => info.raw
  ? h('a', { class: 'button', href: fileURL(info.raw, true), title: info.raw.name + ', ' + formatSize(info.raw.size) }, 'Download the original recording')
  : '';

// sermonDetail returns the panel for a folder; it fills in when the folder
// has been read, and again after a format was made.
export function sermonDetail(entry) {
  const panel = h('div', { class: 'detail' }, h('p', { class: 'muted' }, 'Loading…'));
  const editURL = '#/folder/' + entry.path;

  async function show() {
    const info = await api('GET', 'api/folder?path=' + q(entry.path));
    panel.replaceChildren(...(info.outputs.length ? processed(info) : unprocessed(info)));
  }

  function unprocessed(info) {
    return [
      h('div', { class: 'raw-warning', role: 'alert' },
        h('strong', {}, 'Not processed. '),
        'This is the full, unedited raw recording of the whole service, not the sermon. ',
        'Open it in the editor to select the sermon and make the files.'),
      h('p', { class: 'actions' }, h('a', { class: 'button', href: editURL }, 'Open in the editor'), original(info)),
    ];
  }

  function processed(info) {
    const saved = info.sermon && info.sermon.saved;
    const tags = info.tags || {};
    const facts = [
      saved
        ? `Length ${formatTime(keptSeconds(saved), 0)}` + (saved.sections.length > 1 ? ` (${saved.sections.length} sections)` : '')
        : 'Not saved in the editor yet',
      tags.title && 'Title: ' + tags.title,
      tags.artist && 'Artist: ' + tags.artist,
      tags.album && 'Album: ' + tags.album,
    ].filter(Boolean);
    const status = h('p', { class: 'muted making', 'aria-live': 'polite' });
    const buttons = [];
    const downloads = FORMATS.map(([format, label]) => {
      const o = info.outputs.find((x) => x.format === format);
      if (o) return h('a', { class: 'button dl on', href: fileURL(o, true), title: formatSize(o.size) }, 'Download ' + label);
      const b = h('button', {
        class: 'dl',
        disabled: !saved,
        title: saved ? `Not made yet: make ${label}` : 'Save the sermon in the editor first',
        onclick: () => make(format, label, b, status, buttons),
      }, 'Download ' + label);
      buttons.push(b);
      return b;
    });
    const best = PLAY_ORDER.map((f) => info.outputs.find((x) => x.format === f)).find(Boolean);
    return [
      h('p', { class: 'facts' }, facts.join(' · ')),
      h('audio', { class: 'player', controls: true, preload: 'none', src: fileURL(best), 'aria-label': 'The sermon' }),
      h('p', { class: 'actions' }, downloads),
      status,
      h('p', { class: 'actions' }, h('a', { class: 'button', href: editURL }, 'Edit cuts'), original(info)),
    ];
  }

  async function make(format, label, button, status, buttons) {
    for (const b of buttons) b.disabled = true;
    button.classList.add('busy');
    status.textContent = `Making ${label}: the conversion is running…`;
    try {
      let job = await api('POST', 'api/make?path=' + q(entry.path) + '&format=' + format);
      while (job.state === 'queued' || job.state === 'running') {
        await sleep(1000);
        status.textContent = `Making ${label}: the conversion is running… ${Math.round((job.progress || 0) * 100)}%`;
        job = await api('GET', 'api/jobs/' + job.id);
      }
      if (job.state !== 'done') throw new Error(job.error || `${label} was not made (${job.state})`);
      await show();
    } catch (err) {
      button.classList.remove('busy');
      status.replaceChildren(h('span', { class: 'error' }, err.message));
      for (const b of buttons) b.disabled = false;
    }
  }

  show().catch((err) => panel.replaceChildren(h('p', { class: 'error' }, err.message)));
  return panel;
}

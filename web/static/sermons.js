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

function raw(info, label) {
  if (!info.raw) return '';
  return h('div', { class: 'out' },
    h('strong', {}, label), h('span', { class: 'muted' }, info.raw.name + ', ' + formatSize(info.raw.size)),
    h('audio', { controls: true, preload: 'none', src: fileURL(info.raw) }),
    h('a', { href: fileURL(info.raw, true) }, 'Download'));
}

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
      h('div', { class: 'outputs' }, raw(info, 'Raw recording')),
      h('p', {}, h('a', { class: 'button', href: editURL }, 'Open in the editor')),
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
    const status = h('p', { class: 'muted', 'aria-live': 'polite' });
    const buttons = [];
    const rows = FORMATS.map(([format, label]) => {
      const o = info.outputs.find((x) => x.format === format);
      if (o) {
        return h('div', { class: 'out' },
          h('strong', {}, label), h('span', { class: 'muted' }, formatSize(o.size)),
          h('audio', { controls: true, preload: 'none', src: fileURL(o) }),
          h('a', { href: fileURL(o, true) }, 'Download'));
      }
      const b = h('button', {
        disabled: !saved,
        title: saved ? '' : 'Save the sermon in the editor first',
        onclick: () => make(format, label, status, buttons),
      }, 'Make ' + label);
      buttons.push(b);
      return h('div', { class: 'out' }, h('strong', {}, label), h('span', { class: 'muted' }, 'Not made'), b);
    });
    return [
      h('p', { class: 'facts' }, facts.join(' · ')),
      h('div', { class: 'outputs' }, rows, raw(info, 'Original (the whole service, unedited)')),
      status,
      h('p', {}, h('a', { href: editURL }, 'Edit cuts')),
    ];
  }

  async function make(format, label, status, buttons) {
    for (const b of buttons) b.disabled = true;
    status.textContent = `Making ${label}…`;
    try {
      let job = await api('POST', 'api/make?path=' + q(entry.path) + '&format=' + format);
      while (job.state === 'queued' || job.state === 'running') {
        await sleep(1000);
        status.textContent = `Making ${label}… ${Math.round((job.progress || 0) * 100)}%`;
        job = await api('GET', 'api/jobs/' + job.id);
      }
      if (job.state !== 'done') throw new Error(job.error || `${label} was not made (${job.state})`);
      await show();
    } catch (err) {
      status.replaceChildren(h('span', { class: 'error' }, err.message));
      for (const b of buttons) b.disabled = false;
    }
  }

  show().catch((err) => panel.replaceChildren(h('p', { class: 'error' }, err.message)));
  return panel;
}

// The details of a date folder in the folder list: what a sermon has (info,
// play, download, make a missing format), or, for a recording that has not
// been processed, only a way into the editor.
import { api, q, h, href, formatSize, formatTime, forTenant } from './util.js';
import { alertDialog } from './dialog.js';

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

const MAX_LABELS = 10;

// sermonDetail returns the panel for a folder; it fills in when the folder
// has been read, and again after a format was made. ctx.suggestions() lists
// the labels already used; ctx.changed() is told when this one's labels change.
export function sermonDetail(entry, ctx = {}) {
  const panel = h('div', { class: 'detail' }, h('p', { class: 'muted' }, 'Loading…'));
  const editURL = href('folder', entry.path);

  async function show() {
    const info = await api('GET', 'api/folder?path=' + q(entry.path));
    panel.replaceChildren(...(info.outputs.length ? processed(info) : unprocessed(info)));
  }

  // The recording's own labels: pills with a cross, and a field to add one. The
  // labels already used are suggested as you type (or all of them, on focus);
  // a label is added by picking one, the + button, Enter, a comma, or leaving
  // the field. Searched on the recordings page; separate from the tags written
  // into files.
  function labelEditor() {
    const box = h('div', { class: 'labels' });
    const input = h('input', { type: 'text', class: 'label-input', name: 'label', placeholder: 'Add a label', maxlength: 30,
      'aria-label': 'Add a label', autocomplete: 'off', enterkeyhint: 'done' });
    const add = h('button', { type: 'button', class: 'label-add', name: 'add-label', title: 'Add the label', 'aria-label': 'Add the label', hidden: true,
      onclick: () => commit() }, '+');
    const menu = h('ul', { class: 'suggest', role: 'listbox', hidden: true });
    // Keep the focus in the field while a suggestion or the button is pressed.
    for (const el of [menu, add]) el.addEventListener('mousedown', (e) => e.preventDefault());
    const matches = () => {
      const typed = input.value.trim().toLowerCase();
      return (ctx.suggestions ? ctx.suggestions() : [])
        .filter((l) => !entry.labels.includes(l) && l.toLowerCase().includes(typed)).slice(0, 8);
    };
    const drawMenu = () => {
      const found = document.activeElement === input ? matches() : [];
      menu.hidden = found.length === 0;
      menu.replaceChildren(...found.map((l) => h('li', { role: 'option', onclick: () => save([...entry.labels, l]) }, l)));
      add.hidden = !input.value.trim();
    };
    const draw = () => {
      box.replaceChildren(
        ...entry.labels.map((l) => h('span', { class: 'chip own' }, l,
          h('button', { type: 'button', class: 'x', title: 'Remove ' + l, 'aria-label': 'Remove label ' + l,
            onclick: () => save(entry.labels.filter((x) => x !== l)) }, '×'))),
        ...(entry.labels.length < MAX_LABELS ? [h('span', { class: 'label-add-box' }, input, add, menu)] : []));
      drawMenu();
    };
    async function save(next) {
      try {
        const e = await api('PUT', 'api/labels?path=' + q(entry.path), { labels: next });
        entry.labels = e.labels;
        if (ctx.changed) ctx.changed();
        input.value = '';
      } catch (err) {
        alertDialog({ body: err.message });
      }
      draw();
      if (entry.labels.length < MAX_LABELS) input.focus();
    }
    const commit = () => { if (input.value.trim()) return save([...entry.labels, input.value]); };
    input.addEventListener('input', () => {
      if (input.value.includes(',')) {
        const [first, ...rest] = input.value.split(',');
        input.value = first;
        commit();
        input.value = rest.join(',');
        return;
      }
      drawMenu();
    });
    input.addEventListener('focus', drawMenu);
    input.addEventListener('blur', () => { menu.hidden = true; if (input.value.trim()) commit(); });
    input.addEventListener('keydown', (e) => {
      if (e.key === 'Escape') { input.value = ''; menu.hidden = true; return; }
      if (e.key !== 'Enter') return;
      e.preventDefault();
      commit();
    });
    draw();
    return box;
  }

  // copyLink puts the address of this recording on the clipboard: it opens the
  // recordings page at it, in this church.
  function copyLink() {
    const url = location.origin + location.pathname + href('recording', entry.path);
    const b = h('button', { type: 'button', name: 'copy-link', onclick: async () => {
      try {
        await navigator.clipboard.writeText(url);
        b.textContent = 'Link copied';
        setTimeout(() => { b.textContent = 'Copy link'; }, 2000);
      } catch (err) {
        alertDialog({ title: 'Link to this recording', body: url });
      }
    } }, 'Copy link');
    return b;
  }

  function unprocessed(info) {
    return [
      labelEditor(),
      h('p', { class: 'actions' }, h('a', { class: 'button', href: editURL }, 'Open in the editor'), copyLink()),
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
      labelEditor(),
      h('audio', { class: 'player', controls: true, preload: 'none', src: fileURL(best), 'aria-label': 'The sermon' }),
      h('p', { class: 'actions' }, downloads),
      status,
      h('p', { class: 'actions' }, h('a', { class: 'button', href: editURL }, 'Edit sermon'), copyLink()),
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

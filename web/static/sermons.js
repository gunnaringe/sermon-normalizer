// The details of a date folder in the folder list: what a sermon has (info,
// play, download, make a missing format), or, for a recording that has not
// been processed, only a way into the editor.
import { api, q, h, href, formatSize, formatTime, forTenant } from './util.js';
import { alertDialog, confirmDialog } from './dialog.js';
import { showCode, parseExpiry, recordingShares } from './shares.js';
import { t, tn, tr } from './i18n.js';

const FORMATS = [['flac', 'FLAC'], ['mp3', 'MP3'], ['opus', 'Opus']];
// SERMON_ONLY ends the format of the files without the intro and outro (flac_sermon ...).
const SERMON_ONLY = '_sermon';
const sermonOnly = (info) => info.outputs.filter((o) => o.format.endsWith(SERMON_ONLY));
const fileURL = (f, download) => forTenant('api/file?' + (download ? 'download=1&' : '') + 'path=' + q(f.path));
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
// DOWNLOAD_ICON is Feather's download icon (MIT), on a file in the Download menu.
const DOWNLOAD_ICON = '<svg viewBox="0 0 24 24" width="16" height="16"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/>'
  + '<polyline points="7 10 12 15 17 10"/><line x1="12" y1="15" x2="12" y2="3"/></svg>';
// SHARE_ICON is the header's share icon (Feather, MIT), for the buttons that share.
const SHARE_ICON = '<svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true"><circle cx="18" cy="5" r="3"/><circle cx="6" cy="12" r="3"/>'
  + '<circle cx="18" cy="19" r="3"/><line x1="8.59" y1="13.51" x2="15.42" y2="17.49"/><line x1="15.41" y1="6.51" x2="8.59" y2="10.49"/></svg>';

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
// the labels already used; ctx.changed() is told when this one's labels change. ctx.shares: the public share
// page is on. Deleting is on the edit page.
export function sermonDetail(entry, ctx = {}) {
  const panel = h('div', { class: 'detail' }, h('p', { class: 'muted' }, 'Loading…'));
  const editURL = href('folder', entry.path);

  async function show() {
    const info = await api('GET', 'api/folder?path=' + q(entry.path));
    panel.replaceChildren(...(info.outputs.length ? processed(info) : unprocessed(info)));
    placeMenu(); // drawn again open, after a format was made
  }

  // The recording's own labels: pills with a cross, and a field to add one. The
  // labels already used are suggested as you type (or all of them, on focus);
  // a label is added by picking one, the + button, Enter, a comma, or leaving
  // the field. Searched on the recordings page; separate from the tags written
  // into files.
  function labelEditor() {
    const box = h('div', { class: 'labels' });
    const input = h('input', { type: 'text', class: 'label-input', name: 'label', placeholder: t('Add a label'), maxlength: 30,
      'aria-label': t('Add a label'), autocomplete: 'off', enterkeyhint: 'done' });
    const add = h('button', { type: 'button', class: 'label-add', name: 'add-label', title: t('Add the label'), 'aria-label': t('Add the label'), hidden: true,
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
      menu.replaceChildren(...found.map((l) => h('li', { role: 'option', onclick: () => { input.value = ''; save((ls) => [...ls, l]); } }, l)));
      add.hidden = !input.value.trim();
    };
    const draw = () => {
      box.replaceChildren(
        ...entry.labels.map((l) => h('span', { class: 'chip own' }, l,
          h('button', { type: 'button', class: 'x', title: t('Remove {name}', { name: l }), 'aria-label': t('Remove label {name}', { name: l }),
            onclick: () => save((ls) => ls.filter((x) => x !== l)) }, '×'))),
        ...(entry.labels.length < MAX_LABELS ? [h('span', { class: 'label-add-box' }, input, add, menu)] : []));
      drawMenu();
    };
    // save makes a change to the labels. Changes are sent one after the other, each to the labels as the one
    // before left them: a label added while another is being saved is not lost. typed goes back in the
    // field if the change fails.
    let queue = Promise.resolve();
    function save(change, typed = '') {
      queue = queue.then(async () => {
        try {
          const e = await api('PUT', 'api/labels?path=' + q(entry.path), { labels: change(entry.labels) });
          entry.labels = e.labels;
          if (ctx.changed) ctx.changed();
        } catch (err) {
          if (typed && !input.value) input.value = typed;
          alertDialog({ body: err.message });
        }
        draw();
        if (entry.labels.length < MAX_LABELS) input.focus();
      });
      return queue;
    }
    // commit adds what is typed; the field is free for the next one at once.
    const commit = () => {
      const typed = input.value;
      if (!typed.trim()) return;
      input.value = '';
      drawMenu();
      return save((ls) => [...ls, typed], typed);
    };
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

  // shareLink shares the address of this recording, as the other shares do: the device's share sheet where
  // there is one, else the clipboard. It opens the recordings page at it, in this church, for those with a login.
  function shareLink() {
    const url = location.origin + location.pathname + href('recording', entry.path);
    const note = h('span', { class: 'muted', role: 'status' });
    const b = h('button', { type: 'button', name: 'share-link', class: 'with-icon', title: t('A link to this recording in the studio, for those who can log in'),
      onclick: async () => {
        if (navigator.share) {
          try { await navigator.share({ title: entry.name, url }); } catch { /* closed */ }
          return;
        }
        try {
          await navigator.clipboard.writeText(url);
          note.textContent = t('Link copied.');
          setTimeout(() => { note.textContent = ''; }, 2500);
        } catch (err) {
          alertDialog({ title: t('Link to this recording'), body: url });
        }
      } });
    b.innerHTML = SHARE_ICON;
    b.append(t('Share link'));
    return [b, note];
  }

  // The download menu closes on a click outside it, and on Escape. It is placed by the window (the list would cut
  // it off), under its button, again when the page scrolls. One set of listeners for the panel, whichever menu it shows.
  const menuEvents = [['pointerdown', closeMenu], ['keydown', closeMenu], ['scroll', placeMenu, true], ['resize', placeMenu]];
  for (const [name, fn, capture] of menuEvents) (name === 'resize' ? window : document).addEventListener(name, fn, capture);
  function gone() {
    if (panel.isConnected) return false;
    for (const [name, fn, capture] of menuEvents) (name === 'resize' ? window : document).removeEventListener(name, fn, capture);
    return true;
  }
  function placeMenu() {
    if (gone()) return;
    const menu = panel.querySelector('details.download-menu');
    if (!menu || !menu.open) return;
    const at = menu.querySelector('summary').getBoundingClientRect();
    const box = menu.querySelector('.menu-panel');
    box.style.top = Math.round(at.bottom + 6) + 'px';
    box.style.left = Math.round(Math.max(8, Math.min(at.left, innerWidth - box.offsetWidth - 8))) + 'px';
  }
  function closeMenu(e) {
    if (gone()) return;
    const menu = panel.querySelector('details.download-menu');
    if (!menu || !menu.open) return;
    if (e.type === 'keydown' ? e.key !== 'Escape' : menu.contains(e.target)) return;
    menu.open = false;
    if (e.type === 'keydown') menu.querySelector('summary').focus();
  }

  // The download menu stays open, and on the same version, when the panel is drawn again after a format was made.
  let menuOpen = false;
  let menuVersion = '';

  // downloadMenu is one Download button that opens the files: each format with its size, or Make for one not made
  // yet. With an intro or outro, the version is chosen first: with them, or the sermon only.
  function downloadMenu(info, saved, status) {
    const only = sermonOnly(info).length > 0;
    if (!only) menuVersion = '';
    const made = (format) => info.outputs.find((o) => o.format === format);
    const list = h('div', { class: 'menu-items' });
    const buttons = [];
    const draw = () => {
      buttons.length = 0;
      list.replaceChildren(...FORMATS.map(([format, label]) => {
        if (menuVersion && !made(format)) return ''; // the sermon only is made with its format
        const o = made(format + menuVersion);
        const name = menuVersion ? t('Download {format}, sermon only', { format: label }) : t('Download {format}', { format: label });
        if (o) {
          const go = h('span', { class: 'go icon', 'aria-hidden': 'true' });
          go.innerHTML = DOWNLOAD_ICON;
          return h('a', { class: 'menu-item', href: fileURL(o, true), 'aria-label': name },
            h('strong', {}, label), h('span', { class: 'muted' }, formatSize(o.size)), go);
        }
        const gone = !info.raw && info.originalDeleted; // nothing to make it from
        const b = h('button', { type: 'button', class: 'menu-item', name: 'make-' + format, disabled: !saved || gone,
          title: gone ? t('The original was deleted, so it cannot be made') : saved ? t('Not made yet: make {format}', { format: label }) : t('Save the sermon in the editor first'),
          onclick: () => make(format, label, b, status, buttons) },
        h('strong', {}, label), h('span', { class: 'muted' }, t('not made')), h('span', { class: 'go' }, t('Make')));
        buttons.push(b);
        return b;
      }));
    };
    const versionButton = (value, label) => h('button', { type: 'button', class: 'seg', 'aria-pressed': String(menuVersion === value),
      onclick: (e) => {
        menuVersion = value;
        for (const b of e.currentTarget.parentElement.children) b.setAttribute('aria-pressed', String(b === e.currentTarget));
        draw();
      } }, label);
    draw();
    const menu = h('details', { class: 'menu download-menu', open: menuOpen },
      h('summary', { class: 'button primary' }, t('Download'), h('span', { class: 'caret', 'aria-hidden': 'true' }, '▾')),
      h('div', { class: 'menu-panel' },
        only ? h('span', { class: 'mode', role: 'group', 'aria-label': t('Version') },
          versionButton('', t('With intro and outro')), versionButton(SERMON_ONLY, t('Sermon only'))) : '',
        list, status));
    menu.addEventListener('toggle', () => { menuOpen = menu.open; placeMenu(); });
    return menu;
  }

  // shareButton makes a download share of the files that have been made: a code and a link that let
  // someone without a login download them on the public share page, as often as it allows.
  function shareButton(info) {
    return h('button', { type: 'button', name: 'share-download', onclick: async () => {
      const made = FORMATS.filter(([f]) => info.outputs.some((o) => o.format === f));
      const boxes = made.map(([f]) => h('input', { type: 'checkbox', name: 'format', value: f, checked: f === 'mp3' || !made.some(([m]) => m === 'mp3') }));
      // With an intro or outro, the share gives one of the two versions.
      const version = sermonOnly(info).length ? h('select', { name: 'version', id: 'download-version' },
        h('option', { value: '' }, t('With intro and outro')), h('option', { value: 'sermon' }, t('Sermon only'))) : null;
      const label = h('input', { type: 'text', name: 'label', id: 'download-label', maxlength: 80, value: entry.name });
      const max = h('input', { type: 'number', name: 'maxDownloads', min: 0, step: 1, placeholder: t('no limit') });
      const expires = h('input', { type: 'text', name: 'expires', class: 'date', maxlength: 16, autocomplete: 'off', placeholder: t('yyyy-mm-dd [hh:mm]') });
      for (;;) {
        const ok = await confirmDialog({ title: t('Share for download'), confirm: t('Make share'), body: [
          h('p', { class: 'muted' }, t('Makes a code and a link that let someone without a login download this sermon on the share page.')),
          h('div', { class: 'share-form' },
            h('label', { for: 'download-label' }, t('Name')), label,
            h('span', {}, t('Formats')), h('span', {}, boxes.map((b, i) => h('label', { class: 'check' }, b, ' ' + made[i][1] + ' '))),
            version ? [h('label', { for: 'download-version' }, t('Version')), version] : '',
            h('label', {}, t('Max downloads')), max,
            h('label', {}, t('Expires')), expires),
        ] });
        if (!ok) return;
        try {
          const done = await api('POST', 'api/shares', {
            kind: 'download', recording: entry.path, label: label.value,
            formats: boxes.filter((b) => b.checked).map((b) => b.value),
            sermonOnly: Boolean(version && version.value === 'sermon'),
            maxDownloads: Number(max.value) || 0,
            expires: parseExpiry(expires.value),
          });
          await showCode(done.url, done.code, t('Download a sermon'));
          await recordingShares(entry.path, sharesBox);
          return;
        } catch (err) {
          await alertDialog({ body: err.message }); // then the form again, as it was filled in
        }
      }
    } }, t('Share for download…'));
  }

  // sharesBox lists the recording's own download shares (only when it has any).
  const sharesBox = h('div', { class: 'recording-shares' });

  function unprocessed(info) {
    return [
      labelEditor(),
      h('p', { class: 'actions' }, h('a', { class: 'button', href: editURL }, t('Open in the editor')), shareLink()),
    ];
  }

  function processed(info) {
    if (ctx.shares) recordingShares(entry.path, sharesBox).catch(() => {});
    const saved = info.sermon && info.sermon.saved;
    const tags = info.tags || {};
    const facts = [
      saved
        ? t('Length {time}', { time: formatTime(keptSeconds(saved), 0) }) + (saved.sections.length > 1 ? ' (' + tn(saved.sections.length, '{n} section', '{n} sections') + ')' : '')
        : t('Not saved in the editor yet'),
      tags.title && t('Title: {value}', { value: tags.title }),
      tags.artist && t('Artist: {value}', { value: tags.artist }),
      tags.album && t('Album: {value}', { value: tags.album }),
      !info.raw && info.originalDeleted && t('Original deleted'),
    ].filter(Boolean);
    // The intro and outro the files were made with, and whether making them again would give others.
    const mw = info.madeWith;
    const clipFacts = mw && [
      mw.intro || mw.outro
        ? t('Made with: {clips}', { clips: [mw.intro && t('intro {name}', { name: mw.intro.name }), mw.outro && t('outro {name}', { name: mw.outro.name })].filter(Boolean).join(', ') })
        : t('Made without intro or outro'),
      mw.changed ? t('The intro or outro has changed since: press Save in the editor to make the files again.') : '',
    ].filter(Boolean).join(' · ');
    const status = h('p', { class: 'muted making', 'aria-live': 'polite' });
    const best = PLAY_ORDER.map((f) => info.outputs.find((x) => x.format === f)).find(Boolean);
    return [
      h('p', { class: 'facts' }, facts.join(' · ')),
      clipFacts ? h('p', { class: 'facts' + (mw.changed ? ' warn' : '') }, clipFacts) : '',
      labelEditor(),
      h('audio', { class: 'player', controls: true, preload: 'none', src: fileURL(best), 'aria-label': t('The sermon') }),
      h('div', { class: 'actions' }, downloadMenu(info, saved, status), h('a', { class: 'button', href: editURL }, t('Edit sermon')),
        shareLink(), ctx.shares && info.outputs.length ? shareButton(info) : ''),
      ctx.shares ? sharesBox : '',
    ];
  }

  async function make(format, label, button, status, buttons) {
    for (const b of buttons) b.disabled = true;
    button.classList.add('busy');
    status.textContent = t('Making {format}: the conversion is running…', { format: label });
    try {
      let job = await api('POST', 'api/make?path=' + q(entry.path) + '&format=' + format);
      while (job.state === 'queued' || job.state === 'running') {
        await sleep(1000);
        status.textContent = t('Making {format}: the conversion is running… {percent}%', { format: label, percent: Math.round((job.progress || 0) * 100) });
        job = await api('GET', 'api/jobs/' + job.id);
      }
      if (job.state !== 'done') throw new Error(job.error ? tr(job.error) : t('{format} was not made ({state})', { format: label, state: job.state }));
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

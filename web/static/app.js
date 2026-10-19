// The app: a hash router over four views (folders, a folder's editor, upload, the inbox).
import { api, q, h, formatSize, crumbs, forTenant, currentTenant, setTenant } from './util.js';
import { editorView } from './editor.js';

const view = document.getElementById('view');
let config = { inbox: false, defaults: {} };
let teardown = null; // cleanup of the current view (player, event stream, keys)

async function route() {
  if (teardown) teardown();
  teardown = null;
  const hash = decodeURIComponent(location.hash.slice(1)) || '/';
  const [, section, ...rest] = hash.split('/');
  const path = rest.join('/');
  const nav = ['inbox', 'upload'].includes(section) ? section : 'folders';
  for (const a of document.querySelectorAll('header nav a')) a.classList.toggle('active', a.dataset.nav === nav);
  try {
    if (section === 'folder') teardown = await editorView(view, path, config);
    else if (section === 'upload') uploadView();
    else if (section === 'inbox' && config.inbox) teardown = await inboxView();
    else await foldersView(section === 'folders' ? path : '');
  } catch (err) {
    view.replaceChildren(h('p', { class: 'error' }, err.message));
  }
}

function badges(e) {
  const out = [];
  if (e.job) out.push(h('span', { class: 'badge busy' }, 'processing'));
  if (e.converting) out.push(h('span', { class: 'badge busy' }, 'converting'));
  if (e.raw) out.push(h('span', { class: 'badge' }, 'raw'));
  if (e.edit) out.push(h('span', { class: 'badge' }, 'edit'));
  for (const f of ['flac', 'mp3', 'opus']) {
    if (e.outputs.includes(f)) out.push(h('span', { class: 'badge on' }, '✓ ' + f));
  }
  return out;
}

async function foldersView(path) {
  const data = await api('GET', 'api/folders?path=' + q(path));
  const open = (e) => { location.hash = (e.date ? '#/folder/' : '#/folders/') + e.path; };
  const rows = data.entries.map((e) =>
    h('tr', { class: 'row', onclick: () => open(e) },
      h('td', {}, h('a', { href: (e.date ? '#/folder/' : '#/folders/') + e.path }, e.name + (e.date ? '' : '/'))),
      h('td', {}, badges(e))));
  view.replaceChildren(
    data.path ? crumbs(data.path) : '',
    h('h1', {}, data.path ? data.path.split('/').pop() : 'Folders'),
    rows.length
      ? h('table', { class: 'list' }, h('tbody', {}, rows))
      : h('p', { class: 'muted' }, 'No folders here yet. ', h('a', { href: '#/upload' }, 'Upload a recording'),
        config.inbox ? ', or import one from the inbox.' : '.'));
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// suggestDate guesses the recording date from the file name, else the
// file's own date.
function suggestDate(file) {
  for (const m of file.name.matchAll(/(20\d\d)[-_.]?(\d\d)[-_.]?(\d\d)/g)) {
    const d = `${m[1]}-${m[2]}-${m[3]}`;
    if (!Number.isNaN(Date.parse(d))) return { date: d, source: 'from the name' };
  }
  const t = new Date(file.lastModified);
  const pad = (n) => String(n).padStart(2, '0');
  return { date: `${t.getFullYear()}-${pad(t.getMonth() + 1)}-${pad(t.getDate())}`, source: "from the file's date" };
}

// sendFile uploads file into the date folder in chunks: each request stays
// small (proxies limit request bodies), and after a network error or a
// server restart it continues where the server has it.
async function sendFile(file, date, onProgress) {
  const chunk = Number(localStorage.getItem('uploadChunk')) || Math.min(32 << 20, config.chunk);
  let offset = 0;
  let failures = 0;
  let sent = false; // a chunk may have arrived even if its answer did not
  for (;;) {
    const url = forTenant(`api/upload?date=${q(date)}&name=${q(file.name)}&size=${file.size}&offset=${offset}&modified=${file.lastModified}`);
    let resp;
    let data = {};
    try {
      resp = await fetch(url, { method: 'POST', body: file.slice(offset, offset + chunk) });
      data = await resp.json().catch(() => ({}));
    } catch (e) {
      sent = true;
      if (++failures > 6) throw new Error('the connection keeps failing: ' + e.message);
      await sleep(1000 * 2 ** failures);
      continue;
    }
    if (resp.status === 201) return data.path;
    if (resp.ok) {
      offset = data.offset;
      sent = true;
      failures = 0;
      onProgress(offset / file.size);
      continue;
    }
    if (resp.status === 409 && typeof data.offset === 'number' && (sent || offset > 0)) {
      offset = data.offset; // continue from what the server has
      continue;
    }
    if (resp.status >= 500 && ++failures <= 6) {
      await sleep(1000 * 2 ** failures);
      continue;
    }
    throw new Error(data.error || `${resp.status} ${resp.statusText}`);
  }
}

function uploadView() {
  const input = h('input', { type: 'file', accept: '.wav,.flac,audio/wav,audio/flac', hidden: true });
  const chosen = h('div', {});
  const status = h('p', { class: 'muted' });
  const progress = h('progress', { max: 1, value: 0, hidden: true });
  const drop = h('div', { class: 'drop' },
    'Drop the recording (.wav or .flac) here, or ',
    h('a', { href: '#/upload', onclick: (e) => { e.preventDefault(); input.click(); } }, 'choose a file'), '.');

  function choose(file) {
    if (!file) return;
    if (!/\.(wav|flac)$/i.test(file.name)) {
      status.className = 'error';
      status.textContent = `${file.name}: only .wav and .flac recordings are accepted.`;
      return;
    }
    const guess = suggestDate(file);
    const date = h('input', { type: 'date', value: guess.date, required: true, 'aria-label': 'Date' });
    const button = h('button', { class: 'primary', onclick: async () => {
      if (!date.value) return;
      button.disabled = date.disabled = true;
      progress.hidden = false;
      progress.value = 0;
      status.className = 'muted';
      status.textContent = `Uploading ${file.name} (${formatSize(file.size)})…`;
      try {
        const path = await sendFile(file, date.value, (p) => { progress.value = p; });
        location.hash = '#/folder/' + path;
      } catch (e) {
        status.className = 'error';
        status.textContent = `${file.name}: ${e.message}`;
        button.disabled = date.disabled = false;
        progress.hidden = true;
      }
    } }, 'Upload');
    status.textContent = '';
    chosen.replaceChildren(h('div', { class: 'panel' },
      h('p', {}, h('strong', {}, file.name), ' · ', formatSize(file.size)),
      h('p', {}, h('label', {}, 'Date ', date), ' ', h('span', { class: 'muted' }, guess.source)),
      h('p', {}, button)));
  }

  input.addEventListener('change', () => choose(input.files[0]));
  drop.addEventListener('dragover', (e) => { e.preventDefault(); drop.classList.add('over'); });
  drop.addEventListener('dragleave', () => drop.classList.remove('over'));
  drop.addEventListener('drop', (e) => {
    e.preventDefault();
    drop.classList.remove('over');
    choose(e.dataTransfer.files[0]);
  });
  view.replaceChildren(
    h('h1', {}, 'Upload a recording'),
    h('p', { class: 'muted' }, 'The whole service goes straight into its date folder. A WAV is converted to FLAC, ' +
      'and the waveform prepared, as soon as it is in; the page for the date opens when the upload is done.'),
    drop, input, chosen, status, progress);
}

async function inboxView() {
  const list = h('div', {}, h('p', { class: 'muted' }, 'Loading…'));
  let alive = true;
  const states = {
    waiting: 'still arriving', converting: 'converting to FLAC…', preparing: 'preparing the waveform…', ready: 'ready',
  };

  async function refresh() {
    const data = await api('GET', 'api/inbox');
    if (!alive) return;
    if (!data.files.length) {
      list.replaceChildren(h('p', { class: 'muted' }, 'The inbox is empty.'));
      return;
    }
    // Leave rows alone while someone is choosing a date in them.
    if (list.contains(document.activeElement)) return;
    list.replaceChildren(h('table', { class: 'list' }, h('tbody', {}, data.files.map((f) => {
      const date = h('input', { type: 'date', value: f.suggestedDate, required: true });
      const err = h('span', { class: 'error' });
      const button = h('button', { class: 'primary', onclick: async () => {
        button.disabled = true;
        try {
          const res = await api('POST', 'api/inbox/import', { name: f.name, date: date.value });
          location.hash = '#/folder/' + res.path;
        } catch (e) {
          err.textContent = e.message;
          button.disabled = false;
        }
      } }, 'Import');
      button.disabled = f.state === 'converting';
      const source = { name: 'from the name', metadata: 'from the recording', modified: 'from the file date' }[f.dateSource];
      return h('tr', {},
        h('td', {}, h('strong', {}, f.name), h('div', { class: 'muted' }, formatSize(f.size), ' · ', new Date(f.modified).toLocaleString()),
          h('div', { class: f.state === 'error' ? 'error' : 'muted' }, f.state === 'error' ? 'Could not read it: ' + f.error : states[f.state])),
        h('td', {}, date, h('div', { class: 'muted' }, source)),
        h('td', {}, button, ' ', err));
    }))));
  }

  view.replaceChildren(
    h('h1', {}, 'Inbox'),
    h('p', { class: 'muted' }, 'Recordings put in the inbox folder (by a synced folder, a file share or another service) ' +
      'are converted to FLAC and prepared here. Import one to make its date folder, then cut it there.'),
    list);
  await refresh();
  const timer = setInterval(() => refresh().catch(() => {}), 5000);
  return () => { alive = false; clearInterval(timer); };
}

// churchPicker fills the dropdown in the header and switches church.
async function churchPicker() {
  const select = document.getElementById('church');
  const data = await api('GET', 'api/tenants');
  if (!data.tenants.some((t) => t.id === currentTenant())) setTenant(data.default);
  const fill = () => {
    select.replaceChildren(...data.tenants.map((t) => h('option', { value: t.id, selected: t.id === currentTenant() }, t.name)),
      data.canAdd ? h('option', { value: '+' }, '+ Add church…') : '');
    select.value = currentTenant();
  };
  fill();
  select.addEventListener('change', async () => {
    if (select.value === '+') {
      const name = prompt('Name of the new church');
      if (name && name.trim()) {
        try {
          const t = await api('POST', 'api/tenants', { name });
          data.tenants.push(t);
          setTenant(t.id);
        } catch (err) { alert(err.message); }
      }
    } else {
      setTenant(select.value);
    }
    fill();
    config = await api('GET', 'api/config');
    document.querySelector('[data-nav=inbox]').hidden = !config.inbox;
    if (location.hash && location.hash !== '#/') location.hash = '#/';
    else route();
  });
}

async function start() {
  await churchPicker();
  config = await api('GET', 'api/config');
  document.querySelector('[data-nav=inbox]').hidden = !config.inbox;
  window.addEventListener('hashchange', route);
  route();
}

start().catch((err) => view.replaceChildren(h('p', { class: 'error' }, err.message)));

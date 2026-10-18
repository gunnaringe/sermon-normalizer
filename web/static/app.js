// The app: a hash router over three views (folders, a folder's editor, the inbox).
import { api, q, h, formatSize, crumbs } from './util.js';
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
  for (const a of document.querySelectorAll('header nav a')) {
    a.classList.toggle('active', a.dataset.nav === (section === 'inbox' ? 'inbox' : 'folders'));
  }
  try {
    if (section === 'folder') teardown = await editorView(view, path, config);
    else if (section === 'inbox' && config.inbox) await inboxView();
    else await foldersView(section === 'folders' ? path : '');
  } catch (err) {
    view.replaceChildren(h('p', { class: 'error' }, err.message));
  }
}

function badges(e) {
  const out = [];
  if (e.job) out.push(h('span', { class: 'badge busy' }, 'processing'));
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
      : h('p', { class: 'muted' }, 'No folders here yet.' + (config.inbox ? ' New recordings come in through the inbox.' : '')));
}

async function inboxView() {
  const list = h('div', {}, h('p', { class: 'muted' }, 'Loading…'));
  const status = h('p', { class: 'muted' });
  const progress = h('progress', { max: 1, value: 0, hidden: true });
  const input = h('input', { type: 'file', accept: '.wav,.flac,audio/wav,audio/flac', multiple: true, hidden: true });
  const drop = h('div', { class: 'drop' },
    'Drop recordings (.wav, .flac) here, or ', h('a', { href: '#/inbox', onclick: (e) => { e.preventDefault(); input.click(); } }, 'choose files'), '.');

  async function refresh() {
    const data = await api('GET', 'api/inbox');
    if (!data.files.length) {
      list.replaceChildren(h('p', { class: 'muted' }, 'The inbox is empty.'));
      return;
    }
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
      const source = { name: 'from the name', metadata: 'from the recording', modified: 'from the file date' }[f.dateSource];
      return h('tr', {},
        h('td', {}, h('strong', {}, f.name), h('div', { class: 'muted' }, formatSize(f.size), ' · ', new Date(f.modified).toLocaleString())),
        h('td', {}, date, h('div', { class: 'muted' }, source)),
        h('td', {}, button, ' ', err));
    }))));
  }

  // upload sends one file with XHR (fetch cannot report upload progress).
  // lastModified goes first: the server keeps the recording's own date.
  function upload(file) {
    return new Promise((resolve, reject) => {
      const form = new FormData();
      form.append('lastModified', String(file.lastModified));
      form.append('file', file);
      const xhr = new XMLHttpRequest();
      xhr.open('POST', 'api/inbox/upload');
      xhr.upload.onprogress = (e) => { if (e.lengthComputable) progress.value = e.loaded / e.total; };
      xhr.onload = () => {
        if (xhr.status < 300) return resolve();
        let msg = xhr.statusText;
        try { msg = JSON.parse(xhr.responseText).error; } catch {}
        reject(new Error(msg));
      };
      xhr.onerror = () => reject(new Error('upload failed'));
      xhr.send(form);
    });
  }

  async function uploadAll(files) {
    progress.hidden = false;
    for (const file of files) {
      status.className = 'muted';
      status.textContent = `Uploading ${file.name} (${formatSize(file.size)})…`;
      progress.value = 0;
      try {
        await upload(file);
        status.textContent = `${file.name} uploaded.`;
      } catch (e) {
        status.className = 'error';
        status.textContent = `${file.name}: ${e.message}`;
      }
    }
    progress.hidden = true;
    await refresh();
  }

  input.addEventListener('change', () => uploadAll([...input.files]));
  drop.addEventListener('dragover', (e) => { e.preventDefault(); drop.classList.add('over'); });
  drop.addEventListener('dragleave', () => drop.classList.remove('over'));
  drop.addEventListener('drop', (e) => {
    e.preventDefault();
    drop.classList.remove('over');
    uploadAll([...e.dataTransfer.files]);
  });

  view.replaceChildren(
    h('h1', {}, 'Inbox'),
    h('p', { class: 'muted' }, 'New recordings wait here. Import one to make its date folder, then cut it there.'),
    list, h('h2', {}, 'Upload'), drop, input, status, progress);
  await refresh();
}

async function start() {
  config = await api('GET', 'api/config');
  document.querySelector('[data-nav=inbox]').hidden = !config.inbox;
  window.addEventListener('hashchange', route);
  route();
}

start().catch((err) => view.replaceChildren(h('p', { class: 'error' }, err.message)));

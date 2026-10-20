// The public upload page: enter a code (or come with #code=... in the link),
// then drop recordings. Every request carries the code in the Share-Code
// header; the code is kept in sessionStorage (this tab only), never in the
// address bar once read. Files are sent in chunks, so they pass Cloudflare's
// limits and continue after a dropped connection or a reload.
import { h, formatSize, formatDateTime } from './util.js';

const view = document.getElementById('view');
let code = '';
let info = null;

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function call(method, url, body) {
  const opts = { method, headers: { 'Share-Code': code } };
  if (body instanceof Blob) opts.body = body;
  else if (body !== undefined) {
    opts.headers['Content-Type'] = 'application/json';
    opts.body = JSON.stringify(body);
  }
  const resp = await fetch(url, opts);
  const data = await resp.json().catch(() => ({}));
  return { resp, data };
}

function waitText(secs) {
  return secs > 90 ? `${Math.ceil(secs / 60)} minutes` : `${secs} seconds`;
}

function errorText(resp, data) {
  if (resp.status === 429) return `${data.error || 'Too many attempts'} (wait ${waitText(Number(resp.headers.get('Retry-After')) || 60)}).`;
  return data.error || `${resp.status} ${resp.statusText}`;
}

// codeFromLink takes #code=... from the address and removes it.
function codeFromLink() {
  const m = location.hash.match(/code=([A-Za-z0-9 -]{1,64})/);
  if (location.hash) history.replaceState(null, '', location.pathname + location.search);
  return m ? m[1] : '';
}

function showCodeForm(message = '') {
  const input = h('input', { type: 'text', name: 'code', autocomplete: 'off', spellcheck: 'false', maxlength: 64,
    placeholder: 'XXXX-XXXX-XXXX-XXXX', 'aria-label': 'Code', value: code });
  const button = h('button', { class: 'primary', type: 'submit' }, 'Continue');
  const form = h('form', { class: 'code-form', onsubmit: (e) => {
    e.preventDefault();
    if (input.value.trim()) { button.disabled = true; tryCode(input.value.trim()); }
  } }, input, button);
  view.replaceChildren(
    h('h1', {}, 'Send a recording'),
    h('p', {}, 'Enter the code you were given.'),
    form,
    message ? h('p', { class: 'error', role: 'alert' }, message) : '');
  input.focus();
}

async function tryCode(c) {
  code = c;
  let r;
  try {
    r = await call('GET', 'api/share');
  } catch (e) {
    showCodeForm('Could not reach the server: ' + e.message);
    return;
  }
  if (!r.resp.ok) {
    sessionStorage.removeItem('shareCode');
    showCodeForm(errorText(r.resp, r.data));
    return;
  }
  sessionStorage.setItem('shareCode', code);
  info = r.data;
  showUpload();
}

function limitsText() {
  const parts = [`${info.allTypes ? 'Audio or video' : info.types.join(' or ')} files up to ${formatSize(info.maxFileSize)}`];
  if (info.uploadsLeft !== undefined) parts.push(`${info.uploadsLeft} more file${info.uploadsLeft === 1 ? '' : 's'}`);
  if (info.bytesLeft !== undefined) parts.push(`${formatSize(info.bytesLeft)} left`);
  if (info.expires) parts.push('until ' + formatDateTime(info.expires));
  return parts.join(' · ');
}

const queue = [];
let running = false;
let limitsLine = null;

function showUpload() {
  const input = h('input', { type: 'file', multiple: true, accept: info.types.join(','), hidden: true });
  const list = h('ul', { class: 'files' });
  const drop = h('div', { class: 'drop' },
    'Drop the recording here, or ',
    h('a', { href: '#', onclick: (e) => { e.preventDefault(); input.click(); } }, 'choose a file'), '.');
  const add = (files) => {
    for (const file of files) {
      const row = { file, status: h('p', { class: 'muted' }, 'Waiting…'), progress: h('progress', { max: 1, value: 0 }) };
      list.append(h('li', {}, h('p', {}, h('strong', {}, file.name), ' · ', formatSize(file.size)), row.progress, row.status));
      queue.push(row);
    }
    run();
  };
  input.addEventListener('change', () => { add([...input.files]); input.value = ''; });
  drop.addEventListener('dragover', (e) => { e.preventDefault(); drop.classList.add('over'); });
  drop.addEventListener('dragleave', () => drop.classList.remove('over'));
  drop.addEventListener('drop', (e) => {
    e.preventDefault();
    drop.classList.remove('over');
    add([...e.dataTransfer.files]);
  });
  const other = h('a', { href: '#', onclick: (e) => {
    e.preventDefault();
    if (running) return;
    sessionStorage.removeItem('shareCode');
    code = '';
    showCodeForm();
  } }, 'Use another code');
  view.replaceChildren(
    h('h1', {}, info.label),
    limitsLine = h('p', { class: 'muted' }, `For ${info.church}. ${limitsText()}.`),
    info.state ? h('p', { class: 'error' }, 'This code cannot be used any more.') : drop,
    input, list,
    h('p', { class: 'muted' }, 'Keep this page open until every file is received. ', other));
}

async function run() {
  if (running) return;
  running = true;
  while (queue.length) {
    const row = queue.shift();
    row.status.className = 'muted';
    row.status.textContent = 'Uploading…';
    try {
      await sendFile(row.file, (p) => { row.progress.value = p; });
      row.progress.value = 1;
      row.status.className = 'ok';
      row.status.textContent = '✓ Received. Thank you!';
    } catch (e) {
      row.progress.hidden = true;
      row.status.className = 'error';
      row.status.textContent = e.message;
    }
  }
  running = false;
  const r = await call('GET', 'api/share').catch(() => null);
  if (r && r.resp.ok) {
    info = r.data;
    limitsLine.textContent = `For ${info.church}. ${limitsText()}.`;
  }
}

// sendFile uploads in chunks; an upload interrupted by a reload continues
// (its id is remembered by the file's name, size and date).
async function sendFile(file, onProgress) {
  const ext = file.name.slice(file.name.lastIndexOf('.')).toLowerCase();
  if (!info.types.includes(ext)) {
    throw new Error(info.allTypes ? 'This type of file cannot be sent.' : `Only ${info.types.join(' and ')} files can be sent with this code.`);
  }
  if (file.size > info.maxFileSize) throw new Error(`The file is larger than the ${formatSize(info.maxFileSize)} this code allows.`);
  const key = `shareUpload:${file.name}:${file.size}:${file.lastModified}`;
  let id = localStorage.getItem(key);
  let offset = 0;
  if (id) {
    const r = await call('GET', `api/share/uploads/${encodeURIComponent(id)}`).catch(() => null);
    if (r && r.resp.ok) offset = r.data.offset;
    else id = null;
  }
  if (!id) {
    const r = await call('POST', 'api/share/uploads', { name: file.name, size: file.size, modified: file.lastModified });
    if (!r.resp.ok) throw new Error(errorText(r.resp, r.data));
    id = r.data.id;
    localStorage.setItem(key, id);
  }
  const chunk = info.chunk || 16 << 20;
  let failures = 0;
  for (;;) {
    onProgress(offset / file.size);
    let r;
    try {
      r = await call('PUT', `api/share/uploads/${encodeURIComponent(id)}?offset=${offset}`, file.slice(offset, offset + chunk));
    } catch (e) {
      if (++failures > 8) throw new Error('The connection keeps failing: ' + e.message + '. Choose the file again to continue.');
      await sleep(1000 * Math.min(60, 2 ** failures));
      continue;
    }
    const { resp, data } = r;
    if (resp.status === 201) {
      localStorage.removeItem(key);
      return;
    }
    if (resp.ok) {
      offset = data.offset;
      failures = 0;
      continue;
    }
    if (resp.status === 409 && typeof data.offset === 'number') {
      offset = data.offset; // continue from what the server has
      continue;
    }
    if (resp.status === 409 || resp.status === 429 || resp.status >= 500) {
      if (++failures > 8) throw new Error(errorText(resp, data));
      await sleep(1000 * Math.max(Number(resp.headers.get('Retry-After')) || 0, Math.min(60, 2 ** failures)));
      continue;
    }
    // Refused for good (wrong type, revoked...): free its place in the code's limits.
    localStorage.removeItem(key);
    if (resp.status !== 404) await call('DELETE', `api/share/uploads/${encodeURIComponent(id)}`).catch(() => {});
    throw new Error(errorText(resp, data));
  }
}

code = codeFromLink() || sessionStorage.getItem('shareCode') || '';
if (code) tryCode(code);
else showCodeForm();

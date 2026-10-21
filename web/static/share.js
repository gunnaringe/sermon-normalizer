// The public share page: enter a code (or come with #code=... in the link),
// then drop recordings, or, with the code of a download share, download the
// sermon it gives (sharedownload.go). Every request carries the code in the Share-Code
// header; the code is kept in sessionStorage (this tab only), never in the
// address bar once read. Files are sent in chunks, so they pass Cloudflare's
// limits and continue after a dropped connection or a reload.
import { h, formatSize, formatDateTime } from './util.js';
import { t, tn, tr, lang, LANGS, setLang } from './i18n.js';

// The language, for those whose browser is set to another one.
const langSelect = document.getElementById('lang');
langSelect.replaceChildren(...LANGS.map(([code, name]) => h('option', { value: code, selected: code === lang }, name)));
langSelect.addEventListener('change', () => { setLang(langSelect.value); location.reload(); });

const view = document.getElementById('view');

// setHeading names the page, in the header and the tab, for what the code is for.
function setHeading(text) {
  document.getElementById('heading').textContent = text;
  document.title = text;
}
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
  return secs > 90 ? t('{n} minutes', { n: Math.ceil(secs / 60) }) : t('{n} seconds', { n: secs });
}

function errorText(resp, data) {
  if (resp.status === 429) return t('{error} (wait {time}).', { error: tr(data.error || 'Too many attempts'), time: waitText(Number(resp.headers.get('Retry-After')) || 60) });
  return tr(data.error || `${resp.status} ${resp.statusText}`);
}

// codeFromLink takes #code=... from the address and removes it.
function codeFromLink() {
  const m = location.hash.match(/code=([A-Za-z0-9 -]{1,64})/);
  if (location.hash) history.replaceState(null, '', location.pathname + location.search);
  return m ? m[1] : '';
}

function showCodeForm(message = '') {
  const input = h('input', { type: 'text', name: 'code', autocomplete: 'off', spellcheck: 'false', maxlength: 64,
    placeholder: 'XXXX-XXXX-XXXX-XXXX', 'aria-label': t('Code'), value: code });
  const button = h('button', { class: 'primary', type: 'submit' }, t('Continue'));
  const form = h('form', { class: 'code-form', onsubmit: (e) => {
    e.preventDefault();
    if (input.value.trim()) { button.disabled = true; tryCode(input.value.trim()); }
  } }, input, button);
  setHeading('Sermon Studio');
  view.replaceChildren(
    h('h1', {}, t('Enter your code')),
    h('p', {}, t('Enter the code you were given, to send a recording or download a sermon.')),
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
    showCodeForm(t('Could not reach the server: {error}', { error: e.message }));
    return;
  }
  if (!r.resp.ok) {
    sessionStorage.removeItem('shareCode');
    showCodeForm(errorText(r.resp, r.data));
    return;
  }
  sessionStorage.setItem('shareCode', code);
  info = r.data;
  if (info.kind === 'download') showDownload();
  else showUpload();
}

function limitsText() {
  const parts = [info.allTypes
    ? t('Audio or video files up to {size}', { size: formatSize(info.maxFileSize) })
    : t('{types} files up to {size}', { types: info.types.join(` ${t('or')} `), size: formatSize(info.maxFileSize) })];
  if (info.uploadsLeft !== undefined) parts.push(tn(info.uploadsLeft, '{n} more file', '{n} more files'));
  if (info.bytesLeft !== undefined) parts.push(t('{size} left', { size: formatSize(info.bytesLeft) }));
  if (info.expires) parts.push(t('until {time}', { time: formatDateTime(info.expires) }));
  return parts.join(' · ');
}

const queue = [];
let running = false;
let limitsLine = null;

// showLogo puts the church's own logo in the header, in place of the app's. The code goes in a header, so
// the image is fetched with it and shown from the bytes.
let logoShown = false;
async function showLogo() {
  if (!info.logo || logoShown) return;
  logoShown = true;
  try {
    const resp = await fetch('api/share/logo', { headers: { 'Share-Code': code } });
    if (!resp.ok) return;
    const img = document.querySelector('header .brand img');
    img.src = URL.createObjectURL(await resp.blob());
    img.alt = info.church;
  } catch { /* the app's logo stays */ }
}

function otherCode() {
  return h('a', { href: '#', onclick: (e) => {
    e.preventDefault();
    if (running) return;
    sessionStorage.removeItem('shareCode');
    code = '';
    showCodeForm();
  } }, t('Use another code'));
}

function showUpload() {
  setHeading(t('Send a recording'));
  showLogo();
  const input = h('input', { type: 'file', multiple: true, accept: info.types.join(','), hidden: true });
  const list = h('ul', { class: 'files' });
  const drop = h('div', { class: 'drop' },
    t('Drop the recording here, or'), ' ',
    h('a', { href: '#', onclick: (e) => { e.preventDefault(); input.click(); } }, t('choose a file')), '.');
  const add = (files) => {
    for (const file of files) {
      const row = { file, status: h('p', { class: 'muted' }, t('Waiting…')), progress: h('progress', { max: 1, value: 0 }) };
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
  view.replaceChildren(
    h('h1', {}, info.label),
    limitsLine = h('p', { class: 'muted' }, t('For {church}. {limits}.', { church: info.church, limits: limitsText() })),
    info.state ? h('p', { class: 'error' }, t('This code cannot be used any more.')) : drop,
    input, list,
    h('p', { class: 'muted' }, t('Keep this page open until every file is received.'), ' ', otherCode()));
}

const FORMAT_NAMES = { flac: 'FLAC', mp3: 'MP3', opus: 'Opus' };

function downloadLimits() {
  const parts = [];
  if (info.downloadsLeft !== undefined) parts.push(tn(info.downloadsLeft, '{n} more download', '{n} more downloads'));
  if (info.expires) parts.push(t('until {time}', { time: formatDateTime(info.expires) }));
  return t('From {church}.', { church: info.church }) + (parts.length ? ' ' + parts.join(' · ') + '.' : '');
}

// showDownload is the page of a download share: the sermon, and a button per file. A press counts one
// download and starts it from an address that works for some hours without the code, so the browser
// can download it, and continue a download that broke; pressing again for it counts nothing.
function showDownload() {
  setHeading(t('Download a sermon'));
  showLogo();
  const status = h('p', { class: 'muted', role: 'status' });
  const limits = h('p', { class: 'muted' }, downloadLimits());
  const press = async (f, button) => {
    button.disabled = true;
    try {
      const r = await call('POST', 'api/share/downloads', { format: f.format });
      if (!r.resp.ok) throw new Error(errorText(r.resp, r.data));
      location.assign(r.data.url); // sent as an attachment: the page stays
      status.className = 'ok';
      status.textContent = t('The download has started. If it stops, press the button again: it continues, and is not counted again.');
      const again = await call('GET', 'api/share').catch(() => null);
      if (again && again.resp.ok) {
        info = again.data;
        limits.textContent = downloadLimits();
      }
    } catch (e) {
      status.className = 'error';
      status.textContent = e.message;
    } finally {
      button.disabled = false;
    }
  };
  const buttons = info.files.map((f) => {
    const b = h('button', { type: 'button', class: 'primary', name: 'download-' + f.format },
      t('Download {format}', { format: FORMAT_NAMES[f.format] || f.format }), ' · ', formatSize(f.size));
    b.addEventListener('click', () => press(f, b));
    return b;
  });
  // the artist and date, unless the church's name or the title says it already
  const what = [info.artist !== info.church ? info.artist : '', info.date && !(info.title || '').includes(info.date) ? info.date : '']
    .filter(Boolean).join(' · ');
  const ended = info.state === 'missing' ? t('This sermon is no longer available.')
    : info.state ? t('This code cannot be used any more.')
      : !buttons.length ? t('This sermon is no longer available.') : '';
  view.replaceChildren(
    h('h1', {}, info.title || info.label),
    what ? h('p', {}, what) : '',
    limits,
    ended ? h('p', { class: 'error' }, ended) : h('p', { class: 'actions' }, buttons),
    status,
    h('p', { class: 'muted' }, otherCode()));
}

async function run() {
  if (running) return;
  running = true;
  while (queue.length) {
    const row = queue.shift();
    row.status.className = 'muted';
    row.status.textContent = t('Uploading…');
    try {
      await sendFile(row.file, (p) => { row.progress.value = p; });
      row.progress.value = 1;
      row.status.className = 'ok';
      row.status.textContent = t('✓ Received. Thank you!');
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
    limitsLine.textContent = t('For {church}. {limits}.', { church: info.church, limits: limitsText() });
  }
}

// sendFile uploads in chunks; an upload interrupted by a reload continues
// (its id is remembered by the file's name, size and date).
async function sendFile(file, onProgress) {
  const ext = file.name.slice(file.name.lastIndexOf('.')).toLowerCase();
  if (!info.types.includes(ext)) {
    throw new Error(info.allTypes ? t('This type of file cannot be sent.') : t('Only {types} files can be sent with this code.', { types: info.types.join(` ${t('and')} `) }));
  }
  if (file.size > info.maxFileSize) throw new Error(t('The file is larger than the {size} this code allows.', { size: formatSize(info.maxFileSize) }));
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
      if (++failures > 8) throw new Error(t('The connection keeps failing: {error}. Choose the file again to continue.', { error: e.message }));
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
    // Refused (wrong type, paused...): free its place in the code's limits.
    localStorage.removeItem(key);
    if (resp.status !== 404) await call('DELETE', `api/share/uploads/${encodeURIComponent(id)}`).catch(() => {});
    throw new Error(errorText(resp, data));
  }
}

code = codeFromLink() || sessionStorage.getItem('shareCode') || '';
if (code) tryCode(code);
else showCodeForm();

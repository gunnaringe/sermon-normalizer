// The app: a hash router over three views (folders with the upload, a folder's editor, settings with the shares).
import { api, q, h, formatSize, formatDate, formatDateTime, dateInput, crumbs, forTenant, currentTenant, setTenant, debounce, suggestNames, wheelZoomNeedsKey, setWheelZoomNeedsKey } from './util.js';
import { editorView } from './editor.js';
import { alertDialog, promptDialog } from './dialog.js';
import { sharesView } from './shares.js';
import { sermonDetail } from './sermons.js';

const view = document.getElementById('view');
let config = { inbox: false, defaults: {} };
let teardown = null; // cleanup of the current view (player, event stream, keys)

async function route() {
  if (teardown) teardown();
  teardown = null;
  const hash = decodeURIComponent(location.hash.slice(1)) || '/';
  const [, section, ...rest] = hash.split('/');
  const path = rest.join('/');
  // Old addresses: the upload is on the folder list now, the shares in the settings.
  if (section === 'upload') return location.replace('#/');
  if (section === 'shares') return location.replace('#/settings/shares');
  const nav = section === 'settings' ? section : 'folders';
  for (const a of document.querySelectorAll('header [data-nav]')) a.classList.toggle('active', a.dataset.nav === nav);
  try {
    if (section === 'folder') teardown = await editorView(view, path, config);
    else if (section === 'settings') teardown = await settingsView(path);
    else await foldersView(section === 'folders' ? path : '');
  } catch (err) {
    view.replaceChildren(h('p', { class: 'error' }, err.message));
  }
}

function badges(e) {
  const out = [];
  if (e.job) out.push(h('span', { class: 'badge busy' }, 'processing'));
  if (e.converting) out.push(h('span', { class: 'badge busy' }, 'converting'));
  if (e.preparing) out.push(h('span', { class: 'badge busy' }, 'preparing'));
  if (e.raw && e.outputs.length === 0) out.push(h('span', { class: 'badge warn', title: 'Only the full, unedited recording of the service exists' }, 'not processed'));
  if (e.outputs.length) out.push(h('span', { class: 'badge on', title: 'Ready to play and download' }, 'ready'));
  return out;
}

// arrived says when a recording came: today/yesterday with the time, else the date.
function arrived(t) {
  const d = new Date(t);
  const day = (x) => formatDate(x);
  const time = formatDateTime(d).slice(11);
  if (day(d) === day(Date.now())) return 'arrived today ' + time;
  if (day(d) === day(Date.now() - 864e5)) return 'arrived yesterday ' + time;
  return 'arrived ' + day(d);
}

// folderTable lists folders. A date folder opens its details in the list,
// anywhere on its row; a recording that is not processed only offers the
// editor there. With when, a row also says when its recording arrived.
function folderTable(entries, when = false) {
  const body = h('tbody');
  for (const e of entries) {
    const href = (e.date ? '#/folder/' : '#/folders/') + e.path;
    let detail = null;
    const toggle = h('button', { class: 'twisty', 'aria-expanded': 'false', title: 'Show details', 'aria-label': 'Show details of ' + e.name }, '▸');
    const flip = () => {
      if (detail) {
        detail.remove();
        detail = null;
      } else {
        detail = h('tr', { class: 'detail-row' }, h('td', { colspan: 2 }, sermonDetail(e)));
        row.after(detail);
      }
      toggle.setAttribute('aria-expanded', String(!!detail));
      toggle.textContent = detail ? '▾' : '▸';
    };
    toggle.addEventListener('click', (ev) => { ev.stopPropagation(); flip(); });
    const name = e.date
      ? h('button', { class: 'sermon-name' }, e.name) // its click reaches the row
      : h('a', { href }, e.name + '/');
    const row = h('tr', { class: 'row', onclick: e.date ? flip : () => { location.hash = href; } },
      h('td', {}, e.date ? toggle : '', name),
      h('td', {}, badges(e), when && e.imported ? h('span', { class: 'muted arrived' }, arrived(e.imported)) : ''));
    body.append(row);
  }
  return h('table', { class: 'list' }, body);
}

// foldersView: the upload on top of every list (a recording goes into its
// date folder anyway); on the first page, then the recordings that arrived
// in the last days, before the years.
async function foldersView(path) {
  const data = await api('GET', 'api/folders?path=' + q(path));
  const recent = data.recent || [];
  view.replaceChildren(
    data.path ? crumbs(data.path) : '',
    h('h1', {}, data.path ? data.path.split('/').pop() : 'Recordings'),
    uploadBox(),
    recent.length ? h('section', { class: 'recent' }, h('h2', {}, 'Newly imported'), folderTable(recent, true)) : '',
    recent.length && data.entries.length ? h('h2', {}, 'All') : '',
    data.entries.length
      ? folderTable(data.entries)
      : h('p', { class: 'muted' }, 'No recordings here yet. Upload one above',
        config.inbox ? ', or put one in the inbox folder.' : '.'));
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// suggestDate guesses the recording date from the file name, else the
// file's own date.
function suggestDate(file) {
  for (const m of file.name.matchAll(/(20\d\d)[-_.]?(\d\d)[-_.]?(\d\d)/g)) {
    const d = `${m[1]}-${m[2]}-${m[3]}`;
    if (!Number.isNaN(Date.parse(d))) return { date: d, source: 'from the name' };
  }
  return { date: formatDate(file.lastModified), source: "from the file's date" };
}

// sendFile uploads file into the date folder in chunks: each request stays
// small (proxies limit request bodies), and after a network error or a
// server restart it continues where the server has it.
async function sendFile(file, date, sermon, onProgress) {
  const chunk = Number(localStorage.getItem('uploadChunk')) || Math.min(32 << 20, config.chunk);
  let offset = 0;
  let failures = 0;
  let sent = false; // a chunk may have arrived even if its answer did not
  for (;;) {
    const url = forTenant(`api/upload?date=${q(date)}&sermon=${q(sermon)}&name=${q(file.name)}&size=${file.size}&offset=${offset}&modified=${file.lastModified}`);
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

// uploadBox is a one-line drop zone; a chosen file opens the form for its
// date and name below it.
function uploadBox() {
  const input = h('input', { type: 'file', accept: '.wav,.flac,audio/wav,audio/flac', hidden: true });
  const chosen = h('div', {});
  const status = h('p', { class: 'muted' });
  const progress = h('progress', { max: 1, value: 0, hidden: true });
  const drop = h('div', { class: 'drop' },
    h('strong', {}, 'Upload a recording: '), 'drop the whole service (.wav or .flac) here, or ',
    h('button', { class: 'link', type: 'button', onclick: () => input.click() }, 'choose a file'), '.');

  function choose(file) {
    if (!file) return;
    if (!/\.(wav|flac)$/i.test(file.name)) {
      status.className = 'error';
      status.textContent = `${file.name}: only .wav and .flac recordings are accepted.`;
      return;
    }
    const guess = suggestDate(file);
    const date = dateInput({ name: 'date', value: guess.date, required: true, 'aria-label': 'Date' });
    const sermon = h('input', { type: 'text', maxlength: 60, placeholder: 'optional, e.g. Evening', 'aria-label': 'Name' });
    const button = h('button', { class: 'primary', onclick: async () => {
      if (!date.value) return;
      button.disabled = date.disabled = sermon.disabled = true;
      progress.hidden = false;
      progress.value = 0;
      status.className = 'muted';
      status.textContent = `Uploading ${file.name} (${formatSize(file.size)})…`;
      try {
        const path = await sendFile(file, date.value, sermon.value.trim(), (p) => { progress.value = p; });
        location.hash = '#/folder/' + path;
      } catch (e) {
        status.className = 'error';
        status.textContent = `${file.name}: ${e.message}`;
        button.disabled = date.disabled = sermon.disabled = false;
        progress.hidden = true;
      }
    } }, 'Upload');
    status.textContent = '';
    chosen.replaceChildren(h('div', { class: 'panel' },
      h('p', {}, h('strong', {}, file.name), ' · ', formatSize(file.size), ' ',
        h('button', { class: 'link', type: 'button', onclick: () => { chosen.replaceChildren(); status.textContent = ''; } }, 'cancel')),
      h('p', {}, h('label', {}, 'Date ', date), ' ', h('span', { class: 'muted' }, guess.source)),
      h('p', {}, h('label', {}, 'Name ', sermon), ' ', h('span', { class: 'muted' }, 'Only needed for a second sermon the same day.')),
      h('p', {}, button)));
    suggestNames(sermon);
  }

  input.addEventListener('change', () => choose(input.files[0]));
  drop.addEventListener('dragover', (e) => { e.preventDefault(); drop.classList.add('over'); });
  drop.addEventListener('dragleave', () => drop.classList.remove('over'));
  drop.addEventListener('drop', (e) => {
    e.preventDefault();
    drop.classList.remove('over');
    choose(e.dataTransfer.files[0]);
  });
  return h('div', { class: 'upload' }, drop, input, chosen, status, progress);
}

// settingsView edits the church: its name, default tags (templates with
// placeholders, shown filled in for today, with and without a sermon name),
// sermon names, and its shares when the public upload page is on.
// #/settings/shares scrolls to the shares.
async function settingsView(part) {
  const data = await api('GET', 'api/settings');
  const fields = [['title', 'Title'], ['artist', 'Artist'], ['album', 'Album']];
  const inputs = {};
  const previews = {};
  const status = h('span', { class: 'muted' });
  const save = h('button', { class: 'primary', name: 'save-tags' }, 'Save');
  const rows = fields.map(([key, label]) => {
    inputs[key] = h('input', { type: 'text', name: key, value: data.meta[key], maxlength: 200, oninput: () => { status.textContent = ''; preview(); } });
    previews[key] = h('span', { class: 'muted' });
    return h('label', {}, h('span', { class: 'name' }, label), inputs[key], previews[key]);
  });
  const values = () => Object.fromEntries(fields.map(([key]) => [key, inputs[key].value]));
  const preview = debounce(async () => {
    try {
      const plain = await api('POST', 'api/meta/preview', values());
      const named = await api('POST', 'api/meta/preview', { ...values(), name: 'Evening' });
      for (const [key] of fields) {
        previews[key].classList.remove('error');
        previews[key].textContent = plain[key] || named[key] ? '→ ' + plain[key] + (named[key] !== plain[key] ? '   (with a name: ' + named[key] + ')' : '') : 'empty: no tag';
      }
      save.disabled = false;
    } catch (err) {
      for (const [key] of fields) previews[key].textContent = '';
      previews.title.textContent = err.message;
      previews.title.classList.add('error');
      save.disabled = true;
    }
  }, 250);
  save.addEventListener('click', async () => {
    try {
      await api('PUT', 'api/settings', { meta: values() });
      status.textContent = 'Saved.';
    } catch (err) { status.textContent = err.message; }
  });
  // Remembered sermon names: suggested when naming a sermon; delete one-offs here.
  const names = await api('GET', 'api/names');
  const namesBox = h('div', { class: 'panel' });
  const saveNames = async () => {
    try {
      Object.assign(names, await api('PUT', 'api/names', names));
    } catch (err) { alertDialog({ body: err.message }); }
    showNames();
  };
  function showNames() {
    namesBox.replaceChildren(
      h('label', { class: 'check' }, h('input', {
        type: 'checkbox', name: 'autocomplete', checked: names.enabled,
        onchange: (e) => { names.enabled = e.target.checked; saveNames(); },
      }), 'Suggest names when naming a sermon'),
      names.list.length
        ? h('ul', { class: 'names' }, names.list.map((n) => h('li', {}, n, ' ',
          h('button', { class: 'link', title: 'Remove ' + n, 'aria-label': 'Remove ' + n,
            onclick: () => { names.list = names.list.filter((x) => x !== n); saveNames(); } }, '✕'))))
        : h('p', { class: 'muted' }, names.enabled
          ? 'No names yet: a name is remembered when you give a sermon one.' : 'Names are not being remembered.'));
  }
  showNames();

  // The church's name (kept in church.json in its folder).
  const tenants = await api('GET', 'api/tenants');
  const nameInput = h('input', { type: 'text', name: 'church', maxlength: 80, value: tenants.tenants.find((t) => t.id === currentTenant())?.name || '' });
  const nameStatus = h('span', { class: 'muted' });
  const saveName = h('button', { class: 'primary', name: 'save-church', onclick: async () => {
    try {
      await api('PUT', 'api/church', { name: nameInput.value });
      nameStatus.textContent = 'Saved.';
      window.dispatchEvent(new Event('churches-changed'));
    } catch (err) { nameStatus.textContent = err.message; }
  } }, 'Save');

  // The church's own logo, shown in the header instead of the app's.
  const logoBox = h('div', { class: 'logo-row' });
  const logoStatus = h('span', { class: 'muted' });
  const logoInput = h('input', { type: 'file', name: 'logo', accept: 'image/png,image/jpeg,image/webp,image/svg+xml', hidden: true });
  const showLogo = (logo) => {
    logoBox.replaceChildren(
      logo ? h('img', { src: forTenant('api/logo') + '&v=' + logo, alt: 'Logo' }) : h('span', { class: 'muted' }, 'The Sermon Studio logo is used.'),
      h('button', { type: 'button', name: 'upload-logo', onclick: () => logoInput.click() }, logo ? 'Change logo…' : 'Upload logo…'),
      logo ? h('button', { type: 'button', name: 'remove-logo', onclick: async () => {
        try {
          await api('DELETE', 'api/logo');
          showLogo('');
          window.dispatchEvent(new Event('churches-changed'));
        } catch (err) { logoStatus.textContent = err.message; }
      } }, 'Remove') : '',
      logoInput, logoStatus);
  };
  logoInput.addEventListener('change', async () => {
    const file = logoInput.files[0];
    logoInput.value = '';
    if (!file) return;
    logoStatus.className = 'muted';
    logoStatus.textContent = '';
    const resp = await fetch(forTenant('api/logo'), { method: 'PUT', body: file }).catch((e) => ({ ok: false, json: async () => ({ error: e.message }) }));
    const data = await resp.json().catch(() => ({}));
    if (!resp.ok) {
      logoStatus.className = 'error';
      logoStatus.textContent = data.error || `${resp.status} ${resp.statusText}`;
      return;
    }
    showLogo(data.logo);
    logoStatus.textContent = 'Saved.';
    window.dispatchEvent(new Event('churches-changed'));
  });
  showLogo(tenants.tenants.find((t) => t.id === currentTenant())?.logo || '');

  const shares = h('section', { id: 'shares' });
  if (tenants.shares) await sharesView(shares);

  const view_ = h('div', {},
    h('h1', {}, 'Settings'),
    h('h2', {}, 'Church'),
    h('div', { class: 'panel tags' }, h('label', {}, h('span', { class: 'name' }, 'Name'), nameInput), h('div', {}, saveName, ' ', nameStatus),
      h('p', { class: 'muted' }, 'Logo, shown in the header (PNG, JPEG, WebP or SVG, up to 1 MB). Make it as wide as it is tall, or wider.'), logoBox),
    h('h2', {}, 'Default tags'),
    h('p', { class: 'muted' }, 'Written into the FLAC, MP3 and Opus of every sermon of this church. You can change them for a single run on the sermon\'s page. Placeholders: ',
      data.placeholders.flatMap((p, i) => [h('code', {}, '{' + p.name + '}'), ' ' + p.help + (i < data.placeholders.length - 1 ? ' · ' : '.')])),
    h('div', { class: 'panel tags' }, rows, h('div', {}, save, ' ', status)),
    h('h2', {}, 'Sermon names'),
    h('p', { class: 'muted' }, 'A second sermon on a day gets a name. The names used are remembered and suggested next time, ' +
      'so the same one is not typed in different ways. Remove one-off names from the list to tidy it.'),
    namesBox,
    h('h2', {}, 'Waveform'),
    h('p', { class: 'muted' }, 'Kept in this browser only, for every church.'),
    h('div', { class: 'panel' }, h('label', { class: 'check' }, h('input', {
      type: 'checkbox', name: 'wheel-zoom-key', checked: wheelZoomNeedsKey(),
      onchange: (e) => setWheelZoomNeedsKey(e.target.checked),
    }), 'Hold Ctrl (Cmd on a Mac) to zoom with the scroll wheel; without it, scrolling moves the page')),
    tenants.shares ? shares : '');
  view.replaceChildren(view_);
  if (part === 'shares') shares.scrollIntoView();
  preview();
  return () => {};
}

// churchPicker fills the dropdown in the header and switches church.
async function churchPicker() {
  const select = document.getElementById('church');
  let data = await api('GET', 'api/tenants');
  if (!data.tenants.some((t) => t.id === currentTenant())) setTenant(data.default);
  const brand = document.querySelector('header .brand img');
  const nameText = document.getElementById('church-name');
  const fill = () => {
    const mine = data.tenants.find((t) => t.id === currentTenant());
    brand.src = mine?.logo ? forTenant('api/logo') + '&v=' + mine.logo : 'icons/logo.svg';
    // One church and no adding: just its name, no dropdown.
    const single = data.tenants.length === 1 && !data.canAdd;
    select.hidden = single;
    nameText.hidden = !single;
    nameText.textContent = mine?.name || '';
    select.replaceChildren(...data.tenants.map((t) => h('option', { value: t.id, selected: t.id === currentTenant() }, t.name)),
      data.canAdd ? h('option', { value: '+' }, '+ Add church…') : '');
    select.value = currentTenant();
  };
  fill();
  window.addEventListener('churches-changed', async () => { data = await api('GET', 'api/tenants'); fill(); });
  select.addEventListener('change', async () => {
    if (select.value === '+') {
      const name = await promptDialog({ title: 'Add a church', label: 'Name of the new church', confirm: 'Add church' });
      if (name && name.trim()) {
        try {
          const t = await api('POST', 'api/tenants', { name });
          data.tenants.push(t);
          setTenant(t.id);
          if (t.group) alertDialog({ title: 'Church added', body: `Give people access by adding them to the group ${t.group} in Pocket ID.` });
        } catch (err) { alertDialog({ body: err.message }); }
      }
    } else {
      setTenant(select.value);
    }
    fill();
    config = await api('GET', 'api/config');
    if (location.hash && location.hash !== '#/') location.hash = '#/';
    else route();
  });
}

// logoutButton shows Log out when the server knows the login proxy's logout.
// The icon asks first: a click opens "Log out?", which logs out when clicked;
// a click elsewhere, or a few seconds, closes it again.
async function logoutButton() {
  const { logoutUrl } = await api('GET', 'api/session');
  if (!logoutUrl) return;
  const form = document.getElementById('logout');
  const button = form.querySelector('button');
  form.action = logoutUrl;
  form.hidden = false;
  let timer = null;
  const close = () => {
    clearTimeout(timer);
    button.classList.remove('asking');
    button.setAttribute('aria-label', 'Log out');
  };
  button.addEventListener('click', (e) => {
    e.stopPropagation();
    if (button.classList.contains('asking')) {
      form.submit();
      return;
    }
    button.classList.add('asking');
    button.setAttribute('aria-label', 'Log out? Click again to log out');
    timer = setTimeout(close, 5000);
  });
  document.addEventListener('click', close);
}

async function start() {
  await logoutButton();
  await churchPicker();
  config = await api('GET', 'api/config');
  window.addEventListener('hashchange', route);
  route();
}

start().catch((err) => view.replaceChildren(h('p', { class: 'error' }, err.message)));

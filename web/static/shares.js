// Shares: codes that let people without a login send recordings to this
// church's inbox from the public share page, or download one sermon (made on
// the recording, sermons.js). A code is shown once, when the share is made.
// They have their own overlay (the icon in the header); sharesView fills its box.
import { api, h, href, formatSize, formatDate, formatDateTime } from './util.js';
import { alertDialog, confirmDialog } from './dialog.js';
import { t, tn } from './i18n.js';

const GB = 1 << 30;

// What a share's state is called; the server's words are the keys of this table.
const STATES = { paused: 'paused', expired: 'expired', 'used up': 'used up', full: 'full' };

const FORMAT_NAMES = { flac: 'FLAC', mp3: 'MP3', opus: 'Opus' };
const baseName = (path) => path.slice(path.lastIndexOf('/') + 1);

function stateText(s) {
  if (s.state) return h('span', { class: 'badge' }, t(STATES[s.state] || s.state));
  if (s.missing) return h('span', { class: 'badge' }, t('recording not found'));
  if (s.downloading) return h('span', { class: 'badge busy' }, t('{n} downloading', { n: s.downloading }));
  if (s.uploading) return h('span', { class: 'badge busy' }, t('{n} uploading', { n: s.uploading }));
  return h('span', { class: 'badge on' }, t('open'));
}

function limits(s) {
  if (s.kind === 'download') {
    return [s.formats.map((f) => FORMAT_NAMES[f] || f).join(' '),
      s.maxDownloads ? tn(s.maxDownloads, 'up to {n} download', 'up to {n} downloads') : t('no download limit'),
      s.expires ? t('until {time}', { time: formatDateTime(s.expires) }) : t('no expiry')].join(' · ');
  }
  const out = [t('up to {size} per file', { size: formatSize(s.maxFileSize) }), s.allTypes ? t('all audio') : s.types.join(' ')];
  if (s.maxUploads) out.push(tn(s.maxUploads, '{n} file', '{n} files'));
  if (s.maxTotal) out.push(t('{size} in all', { size: formatSize(s.maxTotal) }));
  out.push(s.expires ? t('until {time}', { time: formatDateTime(s.expires) }) : t('no expiry'));
  return out.join(' · ');
}

// parseExpiry reads yyyy-mm-dd or yyyy-mm-dd hh:mm in local time; a day
// alone lasts to its end. Empty = never.
export function parseExpiry(s) {
  s = s.trim();
  if (!s) return null;
  const m = s.match(/^(\d{4})-(\d{2})-(\d{2})(?:[ T](\d{1,2}):(\d{2}))?$/);
  const d = m && (m[4] === undefined
    ? new Date(+m[1], m[2] - 1, +m[3], 23, 59, 59)
    : new Date(+m[1], m[2] - 1, +m[3], +m[4], +m[5]));
  if (!d || d.getMonth() !== m[2] - 1 || d.getDate() !== +m[3] || (m[4] !== undefined && (+m[4] > 23 || +m[5] > 59))) {
    throw new Error(t('Write the expiry as yyyy-mm-dd or yyyy-mm-dd hh:mm.'));
  }
  return d.toISOString();
}

function linkFor(url, code) {
  return url ? url.replace(/\/?$/, '/') + '#code=' + code : '';
}

// holdToCopy copies text when el is held down half a second (a long press on
// a phone). The copy happens as it is let go, inside the gesture, which is
// what browsers allow; the phone's own selection menu is kept away.
function holdToCopy(el, text, done) {
  let since = 0;
  el.addEventListener('pointerdown', () => { since = Date.now(); });
  el.addEventListener('pointerup', () => {
    if (since && Date.now() - since >= 500) navigator.clipboard.writeText(text).then(done);
    since = 0;
  });
  el.addEventListener('pointerleave', () => { since = 0; });
  el.addEventListener('contextmenu', (e) => e.preventDefault());
}

// showCode shows a new share's code and link, once. title is what the phone's share menu calls it.
export async function showCode(url, code, title = t('Send recordings')) {
  const link = linkFor(url, code);
  const note = h('span', { class: 'muted', role: 'status' });
  const codeEl = h('span', { class: 'share-code', title: t('Hold to copy') }, code);
  holdToCopy(codeEl, code, () => { note.textContent = t('Code copied.'); });
  const share = h('button', { type: 'button', name: 'share', onclick: async () => {
    const what = link ? { url: link } : { text: code };
    if (navigator.share) {
      try { await navigator.share({ title, ...what }); } catch { /* closed */ }
      return;
    }
    await navigator.clipboard.writeText(link || code);
    note.textContent = link ? t('Link copied.') : t('Code copied.');
  } }, t('Share'));
  await alertDialog({ title: t('Share made'), body: [
    t('The code is shown only once. Share it before you close this.'), h('br'), h('br'),
    codeEl, h('br'), h('span', { class: 'muted' }, t('Hold the code to copy it.')),
    ...(link ? [h('br'), h('br'), h('span', { class: 'share-link' }, link)] : []),
    h('br'), h('br'), share, ' ', note,
  ] });
}

export async function sharesView(box) {
  const data = await api('GET', 'api/shares');
  const refresh = () => sharesView(box);
  const num = (name, attrs = {}) => h('input', { type: 'number', name, min: 0, step: 'any', ...attrs });
  const label = h('input', { type: 'text', name: 'label', maxlength: 80, placeholder: t('e.g. Recordings from the sound desk') });
  const types = data.types.map((t) => h('input', { type: 'checkbox', name: 'type', value: t, checked: true }));
  const typeList = h('span', { hidden: true }, types.map((c) => h('label', { class: 'check' }, c, ' ' + c.value + ' ')));
  const allTypes = h('input', { type: 'checkbox', name: 'allTypes', checked: true,
    onchange: () => { typeList.hidden = allTypes.checked; } });
  const fileSize = num('maxFileSize', { value: data.defaultFileSize / GB, min: 0.01 });
  const uploads = num('maxUploads', { step: 1, placeholder: t('no limit') });
  const total = num('maxTotal', { placeholder: t('no limit') });
  const expires = h('input', { type: 'text', name: 'expires', class: 'date', maxlength: 16, autocomplete: 'off',
    placeholder: t('yyyy-mm-dd [hh:mm]') });
  const status = h('span', { class: 'error' });
  const create = h('button', { class: 'primary', name: 'create-share', onclick: async () => {
    status.textContent = '';
    try {
      const made = await api('POST', 'api/shares', {
        label: label.value,
        allTypes: allTypes.checked,
        types: allTypes.checked ? [] : types.filter((c) => c.checked).map((c) => c.value),
        maxFileSize: Math.round(Number(fileSize.value) * GB),
        maxUploads: Number(uploads.value) || 0,
        maxTotal: Math.round(Number(total.value) * GB) || 0,
        expires: parseExpiry(expires.value),
      });
      await showCode(data.url, made.code);
      await refresh();
    } catch (err) { status.textContent = err.message; }
  } }, t('Make share'));

  box.replaceChildren(
    h('p', { class: 'muted' }, t('A share is a code that lets someone without a login send recordings on the upload page'), data.url ? [' (', h('a', { href: data.url, target: '_blank', rel: 'noopener' }, data.url), ')'] : '',
      '. ' + t('The recordings arrive in this church like any other. Give people the code or the link.') + ' ' +
      t('The code is shown only once, when you make the share.') + ' ' +
      t('To let someone download a finished sermon, use Share for download… on the recording.')),
    h('h2', {}, t('New upload share')),
    data.inbox ? h('div', { class: 'panel share-form' },
      h('label', { for: 'share-label' }, t('Name')), Object.assign(label, { id: 'share-label' }),
      h('span', {}, t('File types')), h('span', {}, h('label', { class: 'check' }, allTypes, ' ' + t('All supported audio')), h('br'), typeList),
      h('label', {}, t('Max file size (GB)')), fileSize,
      h('label', {}, t('Max number of files')), uploads,
      h('label', {}, t('Max total size (GB)')), total,
      h('label', {}, t('Expires')), expires,
      h('div', { class: 'wide' }, create, ' ', status))
      : h('p', { class: 'error' }, t('Shares need an inbox folder, and this church has none.')),
    h('h2', {}, t('Made')),
    keepEnded(data, refresh),
    ...shareGroups(data, refresh));
}

// keepEnded is the church's choice of how long a share that has ended is kept before it is removed by itself.
function keepEnded(data, refresh) {
  const select = h('select', { name: 'ended-days', id: 'ended-days', onchange: async () => {
    try {
      await api('PUT', 'api/shares/ended-days', { days: Number(select.value) });
      await refresh();
    } catch (err) { alertDialog({ body: err.message }); }
  } }, data.endedDaysChoices.map((n) => h('option', { value: n, selected: n === data.endedDays },
    n ? tn(n, '{n} day', '{n} days') : t('never'))));
  return h('p', {}, h('label', { for: 'ended-days' }, t('Remove ended shares after')), ' ', select);
}

// recordingShares fills box with a recording's own download shares, in its row on the recordings page;
// nothing when it has none. It is called again after a share was made there.
export async function recordingShares(path, box) {
  const data = await api('GET', 'api/shares?recording=' + encodeURIComponent(path));
  const refresh = () => recordingShares(path, box);
  box.replaceChildren(...(data.shares.length ? [h('h4', {}, t('Shared for download'), ' ', h('a', { class: 'all-shares', href: href('shares') }, t('All shares'))),
    ...shareGroups(data, refresh, { compact: true })] : []));
}

// shareGroups are the shares in two folds: the open and paused ones, and the ended ones (expired, used
// up, full), folded, which are removed by themselves some days after they ended. refresh draws them again
// after a change. compact: a recording's own, without what is the same for all of them.
function shareGroups(data, refresh, { compact = false } = {}) {
  const act = (fn) => async () => {
    try {
      if (await fn() === false) return;
      await refresh();
    } catch (err) { alertDialog({ body: err.message }); }
  };
  // Pausing asks first only when it stops someone's upload or download.
  const pause = (s) => act(async () => {
    if (s.uploading && !await confirmDialog({ title: t('Pause share'), confirm: t('Pause'), danger: true,
      body: t('Uploads in progress with the code of "{label}" stop. It takes uploads again when you resume it.', { label: s.label }) })) return false;
    if (s.resumable && !await confirmDialog({ title: t('Pause share'), confirm: t('Pause'), danger: true,
      body: t('Downloads that have started with the code of "{label}" stop. It gives downloads again when you resume it.', { label: s.label }) })) return false;
    await api('POST', `api/shares/${encodeURIComponent(s.id)}/pause`);
  });
  const resume = (s) => act(() => api('POST', `api/shares/${encodeURIComponent(s.id)}/resume`));
  const remove = (s) => act(async () => {
    const ok = await confirmDialog({ title: t('Remove share'), confirm: t('Remove'), danger: true,
      body: s.kind === 'download'
        ? t('"{label}" and its list of downloads are removed, and its code no longer works. The recording stays.', { label: s.label })
        : t('"{label}" and its list of uploads are removed, and its code no longer works. Files already sent stay.', { label: s.label }) });
    if (!ok) return false;
    await api('POST', `api/shares/${encodeURIComponent(s.id)}/remove`);
  });
  const open = data.shares.filter((s) => !s.ended);
  const ended = data.shares.filter((s) => s.ended);
  const removeEnded = act(async () => {
    const ok = await confirmDialog({ title: t('Remove ended shares'), confirm: t('Remove'), danger: true,
      body: tn(ended.length, '{n} share that has ended (expired, used up or full) is removed with its list. Recordings stay; paused shares too.', '{n} shares that have ended (expired, used up or full) are removed with their lists. Recordings stay; paused shares too.') });
    if (!ok) return false;
    await api('POST', 'api/shares/remove-ended');
  });

  const used = (s) => s.kind === 'download'
    ? (s.count ? tn(s.count, '{n} download', '{n} downloads') : t('Not downloaded yet'))
    : (s.count ? t('{files} received ({size})', { files: tn(s.count, '{n} file', '{n} files'), size: formatSize(s.bytes) }) : t('Nothing received yet'));
  const row = (s) => h('div', { class: 'panel share' + (compact ? ' compact' : '') },
    h('p', {}, h('strong', {}, s.label), ' ', stateText(s), ' ',
      compact ? '' : h('span', { class: 'badge kind' }, s.kind === 'download' ? t('Download') : t('Upload')), ' ',
      h('span', { class: 'muted mono' }, '…' + s.hint)),
    s.kind === 'download' && !compact ? h('p', {}, t('Download of {name}', { name: baseName(s.recording) })) : '',
    h('p', { class: 'muted' }, limits(s)),
    h('p', {}, `${used(s)} · ${t('made {date}', { date: formatDate(s.created) })}`),
    s.removeAt ? h('p', { class: 'muted' }, t('Removed automatically on {date}.', { date: formatDate(s.removeAt) })) : '',
    s.log.length ? h('details', {}, h('summary', {}, s.kind === 'download' ? t('Downloads') : t('Uploads')),
      h('table', { class: 'list' }, s.log.map((u) => h('tr', {},
        h('td', {}, formatDateTime(u.time)), h('td', {}, u.name), h('td', {}, formatSize(u.size)), h('td', { class: 'muted' }, u.ip))))) : '',
    h('p', {}, !s.state ? h('button', { name: 'pause', onclick: pause(s) }, t('Pause'))
      : s.state === 'paused' ? [h('button', { class: 'primary', name: 'resume', onclick: resume(s) }, t('Resume')), ' ',
        h('button', { name: 'remove', onclick: remove(s) }, t('Remove'))]
      : h('button', { name: 'remove', onclick: remove(s) }, t('Remove'))));

  if (!data.shares.length) return [h('p', { class: 'muted' }, t('No shares yet.'))];
  return [
    open.length || !compact ? h('details', { class: 'share-group', open: true },
      h('summary', {}, t('Open ({n})', { n: open.length })),
      ...(open.length ? open.map(row) : [h('p', { class: 'muted' }, t('None open.'))])) : '',
    ended.length ? h('details', { class: 'share-group ended' },
      h('summary', {}, t('Ended ({n})', { n: ended.length })),
      h('p', { class: 'muted' }, data.endedDays
        ? tn(data.endedDays, 'An ended share is removed automatically {n} day after it ended.', 'An ended share is removed automatically {n} days after it ended.')
        : t('Ended shares are kept until you remove them.'),
      compact ? '' : [' ', h('button', { name: 'remove-ended', onclick: removeEnded }, t('Remove {n} ended', { n: ended.length }))]),
      ...ended.map(row)) : '',
  ];
}

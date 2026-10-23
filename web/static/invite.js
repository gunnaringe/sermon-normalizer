// An invite link (invite.html#<token>, made with `sermon-studio admin invite`):
// after the login, it says which church it is for and makes the user a
// member when they accept (api/invite).
import { h, toLogin } from './util.js';
import { t, tr, lang, LANGS, setLang } from './i18n.js';

const langSelect = document.getElementById('lang');
langSelect.replaceChildren(...LANGS.map(([code, name]) => h('option', { value: code, selected: code === lang }, name)));
langSelect.addEventListener('change', () => { setLang(langSelect.value); location.reload(); });

const view = document.getElementById('view');
const token = decodeURIComponent(location.hash.slice(1));

async function call(method, url, body) {
  const opts = { method, headers: {} };
  if (body !== undefined) {
    opts.headers['Content-Type'] = 'application/json';
    opts.body = JSON.stringify(body);
  }
  const resp = await fetch(url, opts);
  if (resp.status === 401) {
    toLogin();
    return new Promise(() => {}); // the page is leaving
  }
  const data = await resp.json().catch(() => ({}));
  if (!resp.ok) throw new Error(tr(data.error || `${resp.status} ${resp.statusText}`));
  return data;
}

const studio = (church) => './#/' + encodeURIComponent(church.id);

function show(...children) {
  view.replaceChildren(h('h1', {}, t('Invitation')), ...children);
}

async function start() {
  if (!token) {
    show(h('p', { class: 'error' }, t('This link has no invite in it. Copy the whole link, and try again.')));
    return;
  }
  const inv = await call('GET', 'api/invite?token=' + encodeURIComponent(token));
  const name = inv.church.name;
  if (inv.member && inv.role !== 'admin') {
    show(h('p', {}, t('You are already a member of {church}.', { church: name })),
      h('p', {}, h('a', { class: 'button primary', href: studio(inv.church) }, t('Open the studio'))));
    return;
  }
  const accept = h('button', { class: 'primary', type: 'button' }, t('Accept'));
  const error = h('p', { class: 'error', hidden: true });
  accept.addEventListener('click', async () => {
    accept.disabled = true;
    try {
      const done = await call('POST', 'api/invite', { token });
      history.replaceState(null, '', location.pathname); // the link is used up
      location.href = studio(done.church);
    } catch (err) {
      error.textContent = err.message;
      error.hidden = false;
      accept.disabled = false;
    }
  });
  show(h('p', {}, inv.role === 'admin'
    ? t('You are invited to {church} as an admin.', { church: name })
    : t('You are invited to {church} as a member.', { church: name })),
  h('p', {}, accept), error);
}

start().catch((err) => show(h('p', { class: 'error' }, err.message)));

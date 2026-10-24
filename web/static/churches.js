// The church picker, on the login host when every church has its own address
// (church hosts): with one church it goes straight there; with several it
// lists them; with none it says how to get in.
import { h, toLogin } from './util.js';
import { t, tr, lang, LANGS, setLang } from './i18n.js';

const langSelect = document.getElementById('lang');
langSelect.replaceChildren(...LANGS.map(([code, name]) => h('option', { value: code, selected: code === lang }, name)));
langSelect.addEventListener('change', () => { setLang(langSelect.value); location.reload(); });

const view = document.getElementById('view');
const address = (c) => (c.url || './') + '#/' + encodeURIComponent(c.id);

async function start() {
  const resp = await fetch('api/tenants');
  const data = await resp.json().catch(() => ({}));
  if (resp.status === 401 && data.login) {
    toLogin();
    return;
  }
  if (!resp.ok) {
    view.replaceChildren(h('h1', {}, t('Your churches')), h('p', {}, tr(data.error || `${resp.status} ${resp.statusText}`)));
    return;
  }
  if (data.tenants.length === 1) {
    location.replace(address(data.tenants[0]));
    return;
  }
  view.replaceChildren(h('h1', {}, t('Your churches')), h('p', { class: 'muted' }, t('Choose a church.')),
    h('ul', { class: 'church-list' }, data.tenants.map((c) => h('li', {}, h('a', { class: 'button', href: address(c) }, c.name)))));
}

start().catch((err) => view.replaceChildren(h('p', { class: 'error' }, err.message)));

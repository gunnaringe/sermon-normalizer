// The page after logging out of the studio (auth/logout), or after a login
// that did not work (?failed): a way to log in again.
import { h } from './util.js';
import { t, lang, LANGS, setLang } from './i18n.js';

const langSelect = document.getElementById('lang');
langSelect.replaceChildren(...LANGS.map(([code, name]) => h('option', { value: code, selected: code === lang }, name)));
langSelect.addEventListener('change', () => { setLang(langSelect.value); location.reload(); });

if (location.search === '?failed') {
  document.getElementById('title').textContent = t('Log in');
  document.getElementById('failed').hidden = false;
}

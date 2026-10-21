// Dialogs in the look of the app, instead of the browser's own confirm/prompt/alert.
// They use <dialog>.showModal(): focus stays inside, Esc and a click outside cancel.
import { h } from './util.js';
import { t } from './i18n.js';

// ask shows a dialog with a title, a body and buttons, and resolves with the
// dialog's result: what close() was given, or null when it was dismissed.
function ask({ title, body, buttons, focus, result }) {
  return new Promise((resolve) => {
    const dialog = h('dialog', { class: 'dialog', 'aria-label': title });
    const close = (value) => dialog.close(value);
    const made = buttons.map((b) => h('button', {
      type: 'button', class: b.class || '', 'data-result': b.value,
      onclick: () => close(b.value),
    }, b.label));
    dialog.append(h('div', { class: 'dialog-body' },
      h('h2', {}, title), ...[body].flat(), h('div', { class: 'dialog-buttons' }, made)));
    dialog.addEventListener('click', (e) => { if (e.target === dialog) close('cancel'); }); // the backdrop
    dialog.addEventListener('close', () => {
      const value = dialog.returnValue;
      const out = result ? result(value) : value;
      dialog.remove();
      resolve(out);
    });
    document.body.append(dialog);
    dialog.showModal();
    const first = focus ? dialog.querySelector(focus) : made[made.length - 1];
    if (first) first.focus();
  });
}

// confirmDialog resolves true when confirmed. danger makes the confirm button
// red and puts the focus on Cancel, for things that replace or delete.
export function confirmDialog({ title, body, confirm = t('OK'), cancel = t('Cancel'), danger = false }) {
  return ask({
    title, body,
    buttons: [{ label: cancel, value: 'cancel' }, { label: confirm, value: 'ok', class: danger ? 'danger' : 'primary' }],
    focus: danger ? '[data-result=cancel]' : '[data-result=ok]',
    result: (v) => v === 'ok',
  });
}

// alertDialog shows a message with one button.
export function alertDialog({ title = t('Something went wrong'), body }) {
  return ask({ title, body: h('p', {}, body), buttons: [{ label: t('OK'), value: 'ok', class: 'primary' }] });
}

// infoDialog shows any content (a table, several paragraphs) with one button.
export function infoDialog({ title, body }) {
  return ask({ title, body, buttons: [{ label: t('OK'), value: 'ok', class: 'primary' }] });
}

// promptDialog asks for a line of text and resolves with it, or null if cancelled.
export function promptDialog({ title, label, value = '', placeholder = '', confirm = t('OK'), maxlength = 200 }) {
  const input = h('input', { type: 'text', value, placeholder, maxlength, 'aria-label': label || title });
  let dialogEl = null;
  input.addEventListener('keydown', (e) => {
    if (e.key === 'Enter') { e.preventDefault(); dialogEl = dialogEl || input.closest('dialog'); dialogEl.close('ok'); }
  });
  return ask({
    title,
    body: h('p', {}, label ? h('label', {}, label, h('br'), input) : input),
    buttons: [{ label: t('Cancel'), value: 'cancel' }, { label: confirm, value: 'ok', class: 'primary' }],
    focus: 'input',
    result: (v) => (v === 'ok' ? input.value : null),
  });
}

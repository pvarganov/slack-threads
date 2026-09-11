// Разметка приложения. Функции возвращают HTML-строки и ничего не знают
// о DOM и о бэкенде — так их можно проверять в node.

import {authorHue, authorInitial, escapeHtml, formatTime, renderBlocks, renderReactions} from './format.js';
import {canSend, sendBlockedReason, visibleThreads} from './state.js';

/** Весь экран целиком. */
export function renderApp(state) {
    return `
      <div class="layout">
        ${renderHeader(state)}
        <div class="body">
          ${renderSidebar(state)}
          ${renderMain(state)}
        </div>
        ${renderTokenDialog(state)}
        ${renderAddDialog(state)}
        ${renderConfirmDialog(state)}
      </div>
    `;
}

/** Шапка: кто подключён, кнопки добавления и общего обновления. */
export function renderHeader(state) {
    const account = state.token.ok
        ? `<span class="account">${escapeHtml(state.token.user)} · ${escapeHtml(state.token.team)}</span>`
        : '<span class="account account-bad">нет доступа к Slack</span>';

    const busy = Boolean(state.busy);

    return `
      <header class="header">
        <h1>Slack Threads</h1>
        ${account}
        <div class="header-actions">
          <button data-action="add-thread" ${disabled(busy || !state.token.ok)}>Добавить по ссылке</button>
          <button data-action="refresh-all" ${disabled(busy || !state.token.ok)}>Обновить все</button>
          <button data-action="open-token" class="secondary">Токен</button>
        </div>
      </header>
      ${renderStatusBar(state)}
    `;
}

/** Полоса состояния: прогресс, ошибка или короткое уведомление. */
export function renderStatusBar(state) {
    if (state.error) {
        return `<div class="status status-error" role="alert">${escapeHtml(state.error)}</div>`;
    }

    if (state.busy || state.progress) {
        const text = state.progress || state.busy;

        return `<div class="status status-busy"><span class="spinner"></span>${escapeHtml(text)}</div>`;
    }

    if (!state.token.ok) {
        const hint = state.token.fixable ? ' Нажмите «Токен» и введите новый.' : '';

        return `<div class="status status-error">${escapeHtml(state.token.message || 'Токен Slack не задан.')}${hint}</div>`;
    }

    if (state.notice) {
        return `<div class="status status-notice">${escapeHtml(state.notice)}</div>`;
    }

    return '<div class="status status-idle"></div>';
}

/** Левая колонка: фильтр архива и список тредов. */
export function renderSidebar(state) {
    const threads = visibleThreads(state.threads, state.showArchived);

    const items = threads.length
        ? threads.map((t) => renderThreadRow(t, t.id === state.selectedId)).join('')
        : '<li class="empty">Пока ни одного треда. Добавьте его по ссылке.</li>';

    return `
      <aside class="sidebar">
        <label class="archive-toggle">
          <input type="checkbox" data-action="toggle-archived" ${state.showArchived ? 'checked' : ''}/>
          Показывать архив
        </label>
        <ul class="thread-list">${items}</ul>
      </aside>
    `;
}

function renderThreadRow(thread, selected) {
    const classes = ['thread-row'];

    if (selected) {
        classes.push('selected');
    }

    if (thread.archived) {
        classes.push('archived');
    }

    const mark = thread.needsRefresh ? '<span class="dot" title="Есть изменения"></span>' : '';
    const title = thread.title || 'Без названия';

    return `
      <li class="${classes.join(' ')}" data-action="select-thread" data-id="${thread.id}">
        <div class="thread-title">${mark}${escapeHtml(title)}</div>
        <div class="thread-meta">
          ${escapeHtml(formatTime(thread.lastFetchedAt || thread.addedAt))}
          ${thread.archived ? '· архив' : ''}
        </div>
      </li>
    `;
}

/** Правая колонка: лента треда и панель ответа. */
export function renderMain(state) {
    if (!state.view) {
        return '<main class="main"><div class="empty-feed">Выберите тред слева или добавьте новый.</div></main>';
    }

    return `
      <main class="main">
        ${renderThreadHeader(state)}
        <div class="feed">
          ${renderSummary(state.view)}
          ${state.view.messages.map((m) => renderMessage(m, state.showOriginal)).join('')}
        </div>
        ${renderReply(state)}
      </main>
    `;
}

function renderThreadHeader(state) {
    const thread = state.view.thread;
    const busy = Boolean(state.busy);
    const link = thread.permalink
        ? `<a href="${escapeHtml(thread.permalink)}" target="_blank" rel="noreferrer">Открыть в Slack</a>`
        : '';

    return `
      <div class="thread-header">
        <h2>${escapeHtml(thread.title || 'Без названия')}</h2>
        <div class="thread-header-actions">
          ${link}
          <label class="original-toggle">
            <input type="checkbox" data-action="toggle-original" ${state.showOriginal ? 'checked' : ''}/>
            Показать оригинал
          </label>
          <button data-action="refresh-thread" ${disabled(busy || !state.token.ok)}>Обновить</button>
          <button data-action="archive-thread" class="secondary">
            ${thread.archived ? 'Вернуть из архива' : 'В архив'}
          </button>
          <button data-action="delete-thread" class="danger">Удалить</button>
        </div>
      </div>
    `;
}

function renderSummary(view) {
    const body = renderBlocks(view.summaryBlocks) || (view.summary ? `<p>${escapeHtml(view.summary)}</p>` : '');

    if (!body) {
        return '<section class="summary summary-empty">«Суть» ещё не собрана.</section>';
    }

    return `<section class="summary"><h3>Суть</h3>${body}</section>`;
}

/** Одно сообщение: перевод, при желании — оригинал под ним. */
export function renderMessage(message, showOriginal) {
    const translated = renderBlocks(message.blocksRu);
    const original = renderBlocks(message.blocks) || `<p>${escapeHtml(message.text)}</p>`;
    const body = translated || '<p class="untranslated">Перевод ещё не готов.</p>';

    const flags = [
        message.isBot ? '<span class="badge">бот</span>' : '',
        message.edited ? '<span class="badge">изменено</span>' : '',
        message.deleted ? '<span class="badge badge-deleted">удалено</span>' : '',
    ].join('');

    const originalBlock = showOriginal
        ? `<div class="original"><div class="original-label">Оригинал</div>${original}</div>`
        : '';

    // Оттенок автора живёт на карточке: его берут и кружок, и имя, и полоса слева.
    return `
      <article class="message${message.deleted ? ' deleted' : ''}" data-id="${message.id}"
               style="--hue: ${authorHue(message.author)}">
        <div class="message-head">
          <span class="avatar">${escapeHtml(authorInitial(message.author))}</span>
          <span class="author">${escapeHtml(message.author)}</span>
          <span class="time">${escapeHtml(formatTime(message.time))}</span>
          ${flags}
        </div>
        <div class="translated">${body}</div>
        ${originalBlock}
        ${renderReactions(message.reactions)}
      </article>
    `;
}

/** Панель ответа: русский → перевод → проверка → отправка. */
export function renderReply(state) {
    const draft = state.draft;
    const busy = Boolean(state.busy);
    const blocked = sendBlockedReason(draft);

    const back = draft.backRu
        ? `<div class="back-translation"><div class="field-label">Обратный перевод</div><div class="back-text">${escapeHtml(draft.backRu)}</div></div>`
        : '';

    return `
      <section class="reply">
        <div class="field-label">Ответ по-русски</div>
        <textarea data-field="ru" rows="3" placeholder="Напишите ответ по-русски">${escapeHtml(draft.textRu)}</textarea>
        <div class="reply-actions">
          <button data-action="translate-reply" ${disabled(busy || !draft.textRu.trim() || !state.token.ok)}>Перевести</button>
          <button data-action="send-reply" class="primary" ${disabled(busy || !canSend(draft) || !state.token.ok)}>Отправить</button>
          ${blocked ? `<span class="hint">${escapeHtml(blocked)}</span>` : ''}
        </div>
        <div class="field-label">Английский текст</div>
        <textarea data-field="en" rows="3" placeholder="Появится после перевода">${escapeHtml(draft.textEn)}</textarea>
        ${back}
      </section>
    `;
}

/**
 * Диалог добавления треда. Системный window.prompt в WKWebView не работает
 * (Wails не реализует делегаты JS-диалогов), поэтому все диалоги — свои.
 */
export function renderAddDialog(state) {
    if (!state.addPrompt) {
        return '';
    }

    return `
      <div class="overlay">
        <div class="dialog">
          <h3>Добавить тред</h3>
          <p class="dialog-hint">Ссылка на сообщение или тред: в Slack меню сообщения → «Copy link».</p>
          <input type="text" data-field="url" placeholder="https://…slack.com/archives/…" autocomplete="off"
                 value="${escapeHtml(state.addUrl)}"/>
          <div class="dialog-actions">
            <button data-action="close-add" class="secondary">Отмена</button>
            <button data-action="submit-add" class="primary" ${disabled(!state.addUrl.trim())}>Добавить</button>
          </div>
        </div>
      </div>
    `;
}

/** Вопрос «да/нет» вместо window.confirm. */
export function renderConfirmDialog(state) {
    if (!state.confirm) {
        return '';
    }

    const {text, okLabel, danger} = state.confirm;

    return `
      <div class="overlay">
        <div class="dialog">
          <p class="dialog-question">${escapeHtml(text)}</p>
          <div class="dialog-actions">
            <button data-action="confirm-cancel" class="secondary">Отмена</button>
            <button data-action="confirm-ok" class="${danger ? 'danger' : 'primary'}">${escapeHtml(okLabel)}</button>
          </div>
        </div>
      </div>
    `;
}

/** Диалог ввода токена. */
export function renderTokenDialog(state) {
    if (!state.tokenPrompt) {
        return '';
    }

    const problem = state.token.ok ? '' : `<p class="dialog-problem">${escapeHtml(state.token.message)}</p>`;

    return `
      <div class="overlay">
        <div class="dialog">
          <h3>Токен Slack</h3>
          ${problem}
          <p class="dialog-hint">Пользовательский токен приложения, начинается с xoxp-. Хранится в связке ключей macOS.</p>
          <input type="password" data-field="token" placeholder="xoxp-…" autocomplete="off"/>
          <div class="dialog-actions">
            <button data-action="close-token" class="secondary">Отмена</button>
            <button data-action="save-token" class="primary">Сохранить</button>
          </div>
        </div>
      </div>
    `;
}

function disabled(condition) {
    return condition ? 'disabled' : '';
}

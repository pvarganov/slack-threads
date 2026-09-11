import './style.css';
import './app.css';

import {api, errorText, onProgress, openExternal} from './api.js';
import {progressText} from './format.js';
import {
    canSend,
    draftFromView,
    emptyDraft,
    initialState,
    withEnglish,
    withRussian,
    withTranslation,
} from './state.js';
import {renderApp} from './view.js';

const root = document.querySelector('#app');
const state = initialState();

/**
 * Перерисовка всего экрана. Ввод в текстовых полях живёт в state, поэтому
 * после замены разметки достаточно вернуть фокус и каретку туда, где они
 * были: иначе набор ответа сбрасывался бы на каждое событие прогресса.
 */
function render() {
    const active = document.activeElement;
    const field = active?.dataset?.field;
    const caret = field ? active.selectionStart : 0;

    root.innerHTML = renderApp(state);

    if (!field) {
        // Открытый диалог сразу готов к вводу: печатать можно, не целясь мышью.
        root.querySelector('.dialog [data-field]')?.focus();

        return;
    }

    const restored = root.querySelector(`[data-field="${field}"]`);

    if (restored) {
        restored.focus();

        if (restored.setSelectionRange) {
            restored.setSelectionRange(caret, caret);
        }
    }
}

/** Обёртка вокруг вызова бэкенда: показывает занятость и ошибку. */
async function run(busyText, fn) {
    if (state.busy) {
        return undefined;
    }

    state.busy = busyText;
    state.error = '';
    state.notice = '';
    render();

    try {
        return await fn();
    } catch (err) {
        state.error = errorText(err);

        return undefined;
    } finally {
        state.busy = '';
        state.progress = '';
        render();
    }
}

async function reloadThreads() {
    state.threads = (await api.listThreads()) ?? [];
}

async function openThread(id) {
    state.selectedId = id;
    state.view = await api.getThread(id);
    state.draft = draftFromView(state.view.draft);
}

/** Готов ли черновик к отправке — та же проверка, что блокирует кнопку. */
function canSendDraft() {
    return canSend(state.draft);
}

/** Открыть вопрос «да/нет»; ответ выполнит confirmed[action]. */
function askConfirm(text, okLabel, action, danger = false) {
    state.confirm = {text, okLabel, action, danger};
    render();
}

const actions = {
    'add-thread'() {
        state.addPrompt = true;
        state.addUrl = '';
        render();
    },

    'close-add'() {
        state.addPrompt = false;
        render();
    },

    async 'submit-add'() {
        const url = state.addUrl.trim();

        if (!url) {
            return;
        }

        state.addPrompt = false;
        state.addUrl = '';

        await run('Добавляю тред…', async () => {
            const view = await api.addThread(url);

            await reloadThreads();
            state.selectedId = view.thread.id;
            state.view = view;
            state.draft = draftFromView(view.draft);
            state.notice = 'Тред добавлен.';
        });
    },

    async 'refresh-all'() {
        await run('Обновляю все треды…', async () => {
            const outcomes = (await api.refreshAll()) ?? [];

            await reloadThreads();

            if (state.selectedId) {
                await openThread(state.selectedId);
            }

            const failed = outcomes.filter((o) => o.error);
            const changed = outcomes.reduce((sum, o) => sum + (o.changed ?? 0), 0);

            state.notice = failed.length
                ? `Обновлено ${outcomes.length - failed.length} из ${outcomes.length}, изменений: ${changed}.`
                : `Обновлено тредов: ${outcomes.length}, изменений: ${changed}.`;

            if (failed.length) {
                state.error = failed.map((o) => `${o.title || o.threadId}: ${o.error}`).join('\n');
            }
        });
    },

    async 'refresh-thread'() {
        if (!state.selectedId) {
            return;
        }

        await run('Обновляю тред…', async () => {
            state.view = await api.refreshThread(state.selectedId);
            state.draft = draftFromView(state.view.draft);
            await reloadThreads();
        });
    },

    async 'select-thread'(element) {
        const id = Number(element.dataset.id);

        await run('Открываю тред…', () => openThread(id));
    },

    async 'translate-reply'() {
        if (!state.selectedId || !state.draft.textRu.trim()) {
            return;
        }

        await run('Перевожу ответ…', async () => {
            const draft = await api.draftReply(state.selectedId, state.draft.textRu);

            state.draft = withTranslation(state.draft, draft);
        });
    },

    'delete-thread'() {
        if (!state.selectedId) {
            return;
        }

        askConfirm('Удалить тред вместе с переводами? Это необратимо.', 'Удалить', 'delete-thread', true);
    },

    async 'archive-thread'() {
        if (!state.view) {
            return;
        }

        const archived = !state.view.thread.archived;

        askConfirm(
            archived
                ? 'Убрать тред в архив? Он перестанет обновляться, перевод сохранится.'
                : 'Вернуть тред из архива?',
            archived ? 'В архив' : 'Вернуть',
            'archive-thread',
        );
    },

    'send-reply'() {
        if (!state.selectedId || !canSendDraft()) {
            return;
        }

        askConfirm('Отправить ответ в тред?', 'Отправить', 'send-reply');
    },

    'confirm-cancel'() {
        state.confirm = null;
        render();
    },

    async 'confirm-ok'() {
        const pending = state.confirm;

        state.confirm = null;
        render();

        if (pending) {
            await confirmed[pending.action]?.();
        }
    },

    'open-token'() {
        state.tokenPrompt = true;
        render();
    },

    'close-token'() {
        state.tokenPrompt = false;
        render();
    },

    async 'save-token'() {
        const token = root.querySelector('[data-field="token"]')?.value?.trim();

        if (!token) {
            return;
        }

        await run('Проверяю токен…', async () => {
            state.token = await api.saveToken(token);

            if (!state.token.ok) {
                return;
            }

            state.tokenPrompt = false;
            await reloadThreads();
            state.notice = 'Токен сохранён.';
        });
    },
};

// Действия, выполняемые после подтверждения в диалоге.
const confirmed = {
    async 'delete-thread'() {
        await run('Удаляю тред…', async () => {
            await api.deleteThread(state.selectedId);
            state.selectedId = 0;
            state.view = null;
            state.draft = emptyDraft(0);
            await reloadThreads();
            state.notice = 'Тред удалён.';
        });
    },

    async 'archive-thread'() {
        if (!state.view) {
            return;
        }

        const archived = !state.view.thread.archived;

        await run(archived ? 'Убираю в архив…' : 'Возвращаю из архива…', async () => {
            await api.archiveThread(state.selectedId, archived);
            await reloadThreads();
            await openThread(state.selectedId);
        });
    },

    async 'send-reply'() {
        if (!state.selectedId) {
            return;
        }

        await run('Отправляю ответ…', async () => {
            await api.sendReply(state.selectedId, state.draft.textEn);
            state.draft = emptyDraft(state.selectedId);
            state.view = await api.getThread(state.selectedId);
            state.notice = 'Ответ отправлен.';
        });
    },

};

root.addEventListener('click', (event) => {
    // Ссылки в сообщениях уходят в браузер, а не в окно приложения.
    const link = event.target.closest('a[href]');

    if (link) {
        event.preventDefault();
        openExternal(link.href);

        return;
    }

    const element = event.target.closest('[data-action]');

    if (!element || element.disabled) {
        return;
    }

    const handler = actions[element.dataset.action];

    if (handler) {
        void handler(element);
    }
});

root.addEventListener('change', (event) => {
    const action = event.target.dataset.action;

    if (action === 'toggle-archived') {
        state.showArchived = event.target.checked;
        render();
    }

    if (action === 'toggle-original') {
        state.showOriginal = event.target.checked;
        render();
    }
});

// Ввод в текстовые поля только запоминается: перерисовка на каждую букву
// не нужна, кнопки пересчитываются по blur и по действиям.
root.addEventListener('input', (event) => {
    const field = event.target.dataset.field;

    if (field === 'ru') {
        state.draft = withRussian(state.draft, event.target.value);
    }

    if (field === 'en') {
        state.draft = withEnglish(state.draft, event.target.value);
    }

    // Кнопка «Добавить» включается по мере ввода, поэтому здесь нужна перерисовка.
    if (field === 'url') {
        const wasEmpty = !state.addUrl.trim();

        state.addUrl = event.target.value;

        if (wasEmpty !== !state.addUrl.trim()) {
            render();
        }
    }
});

// Enter в однострочных полях диалогов подтверждает, Escape закрывает.
root.addEventListener('keydown', (event) => {
    if (event.key === 'Enter' && event.target.dataset?.field === 'url') {
        void actions['submit-add']();
    }

    if (event.key === 'Enter' && event.target.dataset?.field === 'token') {
        void actions['save-token']();
    }

    if (event.key === 'Escape') {
        if (state.confirm) {
            actions['confirm-cancel']();
        } else if (state.addPrompt) {
            actions['close-add']();
        } else if (state.tokenPrompt) {
            actions['close-token']();
        }
    }
});

root.addEventListener('blur', (event) => {
    if (event.target.dataset?.field === 'ru' || event.target.dataset?.field === 'en') {
        render();
    }
}, true);

onProgress((progress) => {
    state.progress = progressText(progress);
    render();
});

async function start() {
    render();

    try {
        state.token = await api.tokenStatus();
    } catch (err) {
        state.error = errorText(err);
    }

    if (state.token.ok) {
        try {
            await reloadThreads();
        } catch (err) {
            state.error = errorText(err);
        }
    } else {
        state.tokenPrompt = state.token.fixable;
    }

    render();
}

void start();

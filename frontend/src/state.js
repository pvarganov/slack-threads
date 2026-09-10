// Состояние интерфейса и переходы между его состояниями. Модуль чистый:
// ничего не рисует и никуда не ходит, поэтому тестируется в node.

/** Пустое состояние до первой загрузки данных. */
export function initialState() {
    return {
        // token — результат последней проверки токена (TokenStatus).
        token: {ok: false, message: '', fixable: false},
        // tokenPrompt — открыт ли диалог ввода токена.
        tokenPrompt: false,
        threads: [],
        showArchived: false,
        selectedId: 0,
        view: null,
        draft: emptyDraft(0),
        showOriginal: false,
        busy: '',
        progress: '',
        error: '',
        notice: '',
    };
}

/** Пустой черновик ответа для треда. */
export function emptyDraft(threadId) {
    return {threadId, textRu: '', textEn: '', backRu: '', translatedFrom: ''};
}

/** Треды слева: архивные показываются только по запросу. */
export function visibleThreads(threads, showArchived) {
    const list = (threads ?? []).filter((t) => showArchived || !t.archived);

    // Свежие сверху: список читается как лента, а не как история.
    return list.slice().sort((a, b) => String(b.addedAt).localeCompare(String(a.addedAt)));
}

/**
 * Черновик из ответа бэкенда. Сохранённый перевод считается сделанным
 * ровно для того русского текста, который вместе с ним лежит в базе.
 */
export function draftFromView(view) {
    if (!view) {
        return emptyDraft(0);
    }

    return {
        threadId: view.threadId ?? 0,
        textRu: view.textRu ?? '',
        textEn: view.textEn ?? '',
        backRu: view.backRu ?? '',
        translatedFrom: view.textEn ? (view.textRu ?? '') : '',
    };
}

/** Правка русского текста: перевод остаётся, но перестаёт быть актуальным. */
export function withRussian(draft, textRu) {
    return {...draft, textRu};
}

/** Правка английского текста руками — обратный перевод больше не про него. */
export function withEnglish(draft, textEn) {
    return {...draft, textEn, backRu: textEn.trim() === draft.textEn.trim() ? draft.backRu : ''};
}

/** Результат «Перевести»: английский и обратный перевод для контроля. */
export function withTranslation(draft, view) {
    return {
        threadId: view.threadId ?? draft.threadId,
        textRu: view.textRu ?? draft.textRu,
        textEn: view.textEn ?? '',
        backRu: view.backRu ?? '',
        translatedFrom: view.textRu ?? draft.textRu,
    };
}

/**
 * Отправка разрешена только после перевода: английский текст есть и
 * русский с тех пор не менялся. Иначе в тред уедет не то, что проверили.
 */
export function canSend(draft) {
    if (!draft || !draft.textEn.trim()) {
        return false;
    }

    return draft.textRu.trim() === (draft.translatedFrom ?? '').trim();
}

/** Причина, по которой кнопка «Отправить» заблокирована. */
export function sendBlockedReason(draft) {
    if (canSend(draft)) {
        return '';
    }

    if (!draft || !draft.textEn.trim()) {
        return 'Сначала переведите ответ на английский.';
    }

    return 'Русский текст изменился после перевода — переведите заново.';
}

/** Тред, открытый справа, в списке слева. */
export function selectedThread(state) {
    return (state.threads ?? []).find((t) => t.id === state.selectedId) ?? null;
}

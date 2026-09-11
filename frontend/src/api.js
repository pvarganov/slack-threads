// Мост к Go-биндингам. Wails кладёт методы в window.go.app.App, а события —
// в window.runtime. Обращение ленивое: до готовности рантайма модуль
// импортируется, но не вызывается.

function bindings() {
    const app = globalThis.go?.app?.App;

    if (!app) {
        throw new Error('Приложение ещё запускается, повторите через секунду.');
    }

    return app;
}

function call(name, ...args) {
    return bindings()[name](...args);
}

export const api = {
    tokenStatus: () => call('TokenStatus'),
    checkToken: () => call('CheckToken'),
    saveToken: (token) => call('SaveToken', token),
    listThreads: () => call('ListThreads'),
    getThread: (id) => call('GetThread', id),
    addThread: (url) => call('AddThread', url),
    refreshThread: (id) => call('RefreshThread', id),
    refreshAll: () => call('RefreshAll'),
    deleteThread: (id) => call('DeleteThread', id),
    archiveThread: (id, archived) => call('ArchiveThread', id, archived),
    draftReply: (id, ru) => call('DraftReply', id, ru),
    sendReply: (id, en) => call('SendReply', id, en),
};

/**
 * Открыть ссылку в системном браузере. Внутри WKWebView обычная навигация
 * увела бы само приложение на страницу Slack, а target="_blank" там просто
 * не работает: окно должен открывать рантайм Wails.
 */
export function openExternal(url) {
    globalThis.runtime?.BrowserOpenURL?.(url);
}

/** Подписка на события прогресса синхронизации. */
export function onProgress(handler) {
    globalThis.runtime?.EventsOn?.('sync:progress', handler);
}

/** Ошибка из Go приходит строкой — она и есть текст для пользователя. */
export function errorText(err) {
    if (!err) {
        return 'Неизвестная ошибка';
    }

    return typeof err === 'string' ? err : (err.message ?? String(err));
}

import assert from 'node:assert/strict';
import {test} from 'node:test';

import {draftFromView, emptyDraft, initialState, withRussian, withTranslation} from '../src/state.js';
import {
    renderAddDialog,
    renderApp,
    renderConfirmDialog,
    renderMessage,
    renderReply,
    renderStatusBar,
    renderTokenDialog,
} from '../src/view.js';

function stateWithThread(overrides = {}) {
    const view = {
        thread: {
            id: 1,
            title: 'Деплой упал',
            channelId: 'C1',
            permalink: 'https://acme.slack.com/archives/C1/p1717171717000200',
            archived: false,
        },
        summary: 'Просят починить деплой',
        summaryBlocks: [{kind: 'paragraph', spans: [{kind: 'text', text: 'Просят починить деплой'}]}],
        messages: [
            {
                id: 10,
                time: '2024-03-01T10:00:00Z',
                author: 'alice',
                text: 'deploy failed',
                textRu: 'деплой упал',
                blocks: [{kind: 'paragraph', spans: [{kind: 'text', text: 'deploy failed'}]}],
                blocksRu: [{kind: 'paragraph', spans: [{kind: 'text', text: 'деплой упал'}]}],
                reactions: [{name: 'eyes', count: 1}],
            },
        ],
        draft: {threadId: 1, textRu: '', textEn: '', backRu: ''},
    };

    return {
        ...initialState(),
        token: {ok: true, user: 'pavel', team: 'acme'},
        threads: [view.thread],
        selectedId: 1,
        view,
        draft: draftFromView(view.draft),
        ...overrides,
    };
}

test('макет из двух колонок с кнопками добавления и обновления всех', () => {
    const html = renderApp(stateWithThread());

    assert.match(html, /class="sidebar"/);
    assert.match(html, /class="main"/);
    assert.match(html, /data-action="add-thread"[^>]*>Добавить по ссылке/);
    assert.match(html, /data-action="refresh-all"[^>]*>Обновить все/);
    assert.match(html, /data-action="select-thread" data-id="1"/);
});

test('лента: «Суть» вверху, затем сообщения с автором, временем и реакциями', () => {
    const html = renderApp(stateWithThread());

    assert.ok(html.indexOf('<h3>Суть</h3>') < html.indexOf('class="message"'));
    assert.match(html, /деплой упал/);
    assert.match(html, /:eyes: 1/);
    assert.match(html, /alice/);
});

test('оригинал показывается только по тумблеру', () => {
    const message = stateWithThread().view.messages[0];

    assert.ok(!renderMessage(message, false).includes('deploy failed'));
    assert.match(renderMessage(message, true), /class="original"/);
});

test('непереведённое сообщение помечено, а не пусто', () => {
    const html = renderMessage({id: 1, author: 'bot', time: '', text: 'raw', blocks: [], blocksRu: []}, false);

    assert.match(html, /Перевод ещё не готов/);
});

test('кнопка отправки заблокирована до перевода и разблокируется после', () => {
    const before = renderReply(stateWithThread({draft: withRussian(emptyDraft(1), 'привет')}));

    assert.match(before, /data-action="send-reply"[^>]*disabled/);
    assert.match(before, /Сначала переведите ответ/);

    const translated = withTranslation(emptyDraft(1), {
        threadId: 1,
        textRu: 'привет',
        textEn: 'hi',
        backRu: 'привет',
    });
    const after = renderReply(stateWithThread({draft: translated}));

    assert.ok(!/data-action="send-reply"[^>]*disabled/.test(after));
    assert.match(after, /Обратный перевод/);
});

test('прогресс, ошибка и отсутствие токена показываются в строке состояния', () => {
    const busy = renderStatusBar({...stateWithThread(), busy: 'Добавляю тред…', progress: 'Перевожу сообщения: 1 из 3'});

    assert.match(busy, /Перевожу сообщения: 1 из 3/);
    assert.match(busy, /class="spinner"/);

    const failed = renderStatusBar({...stateWithThread(), error: 'Slack ответил ошибкой'});

    assert.match(failed, /status-error/);
    assert.match(failed, /Slack ответил ошибкой/);

    const noToken = renderStatusBar({
        ...initialState(),
        token: {ok: false, message: 'Токен Slack не найден.', fixable: true},
    });

    assert.match(noToken, /Токен Slack не найден\./);
    assert.match(noToken, /Нажмите «Токен»/);
});

test('удаление и архивирование доступны из шапки треда', () => {
    const html = renderApp(stateWithThread());

    assert.match(html, /data-action="delete-thread"/);
    assert.match(html, /data-action="archive-thread"[^>]*>\s*В архив/);

    const archived = stateWithThread();

    archived.view.thread.archived = true;

    assert.match(renderApp(archived), /data-action="archive-thread"[^>]*>\s*Вернуть из архива/);
});

test('без токена действия со Slack заблокированы', () => {
    const html = renderApp({...stateWithThread(), token: {ok: false, message: 'нет токена', fixable: true}});

    assert.match(html, /data-action="refresh-all"[^>]*disabled/);
    assert.match(html, /data-action="add-thread"[^>]*disabled/);
});

test('диалог токена открывается только по запросу', () => {
    assert.equal(renderTokenDialog(initialState()), '');
    assert.match(renderTokenDialog({...initialState(), tokenPrompt: true}), /data-field="token"/);
});

test('пустой список тредов подсказывает, что делать', () => {
    const html = renderApp(initialState());

    assert.match(html, /Пока ни одного треда/);
    assert.match(html, /Выберите тред слева/);
});

test('диалог добавления треда заменяет window.prompt', () => {
    assert.equal(renderAddDialog(initialState()), '');

    const empty = renderAddDialog({...initialState(), addPrompt: true});

    assert.match(empty, /data-field="url"/);
    assert.match(empty, /data-action="submit-add"[^>]*disabled/);

    const filled = renderAddDialog({...initialState(), addPrompt: true, addUrl: 'https://x.slack.com/archives/C1/p1'});

    assert.match(filled, /value="https:\/\/x\.slack\.com\/archives\/C1\/p1"/);
    assert.doesNotMatch(filled, /data-action="submit-add"[^>]*disabled/);
});

test('вопрос «да/нет» заменяет window.confirm', () => {
    assert.equal(renderConfirmDialog(initialState()), '');

    const state = {
        ...initialState(),
        confirm: {text: 'Удалить тред?', okLabel: 'Удалить', action: 'delete-thread', danger: true},
    };

    const html = renderConfirmDialog(state);

    assert.match(html, /Удалить тред\?/);
    assert.match(html, /data-action="confirm-ok"[^>]*class="danger"[^>]*>\s*Удалить/);
    assert.match(html, /data-action="confirm-cancel"/);
});

test('открытый диалог попадает в разметку экрана', () => {
    assert.match(renderApp({...initialState(), addPrompt: true}), /data-action="submit-add"/);
    assert.match(
        renderApp({...initialState(), confirm: {text: 'Отправить?', okLabel: 'Отправить', action: 'send-reply'}}),
        /data-action="confirm-ok"/,
    );
});

test('каждое сообщение — отдельная карточка с подсвеченным автором', () => {
    const html = renderMessage(
        {id: 1, author: 'Ann Lee', time: '2026-09-10T10:00:00Z', text: 'hi', blocksRu: [], blocks: [], reactions: []},
        false,
    );

    assert.match(html, /<article class="message"[^>]*style="--hue: \d+"/);
    assert.match(html, /<span class="avatar">A<\/span>/);
    assert.match(html, /<span class="author">Ann Lee<\/span>/);
});

test('строка списка — это тема треда', () => {
    const html = renderApp({
        ...initialState(),
        token: {ok: true, user: 'pavel', team: 'Overgear'},
        threads: [{id: 7, title: 'Ошибка CVV в Ecommpay', channelId: 'C1', addedAt: '2026-09-10T10:00:00Z'}],
    });

    assert.match(html, /Ошибка CVV в Ecommpay/);
    assert.doesNotMatch(html, /thread-preview/);
});

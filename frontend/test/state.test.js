import assert from 'node:assert/strict';
import {test} from 'node:test';

import {
    canSend,
    draftFromView,
    emptyDraft,
    initialState,
    selectedThread,
    sendBlockedReason,
    visibleThreads,
    withEnglish,
    withRussian,
    withTranslation,
} from '../src/state.js';

const threads = [
    {id: 1, title: 'старый', addedAt: '2024-01-01T00:00:00Z', archived: false},
    {id: 2, title: 'архивный', addedAt: '2024-02-01T00:00:00Z', archived: true},
    {id: 3, title: 'новый', addedAt: '2024-03-01T00:00:00Z', archived: false},
];

test('архивные треды скрыты, пока их не попросили', () => {
    assert.deepEqual(visibleThreads(threads, false).map((t) => t.id), [3, 1]);
    assert.deepEqual(visibleThreads(threads, true).map((t) => t.id), [3, 2, 1]);
});

test('исходный список не мутируется сортировкой', () => {
    const copy = threads.slice();

    visibleThreads(threads, true);

    assert.deepEqual(threads, copy);
});

test('отправка заблокирована, пока ответ не переведён', () => {
    const draft = withRussian(emptyDraft(1), 'привет');

    assert.equal(canSend(draft), false);
    assert.equal(sendBlockedReason(draft), 'Сначала переведите ответ на английский.');
});

test('после перевода отправка разрешена', () => {
    const draft = withTranslation(withRussian(emptyDraft(1), 'привет'), {
        threadId: 1,
        textRu: 'привет',
        textEn: 'hi',
        backRu: 'привет',
    });

    assert.equal(canSend(draft), true);
    assert.equal(sendBlockedReason(draft), '');
});

test('правка русского текста снова блокирует отправку', () => {
    const translated = withTranslation(emptyDraft(1), {
        threadId: 1,
        textRu: 'привет',
        textEn: 'hi',
        backRu: 'привет',
    });
    const edited = withRussian(translated, 'привет, коллеги');

    assert.equal(canSend(edited), false);
    assert.match(sendBlockedReason(edited), /переведите заново/);
});

test('правка английского руками убирает обратный перевод, но не блокирует отправку', () => {
    const translated = withTranslation(emptyDraft(1), {
        threadId: 1,
        textRu: 'привет',
        textEn: 'hi',
        backRu: 'привет',
    });
    const edited = withEnglish(translated, 'hi there');

    assert.equal(edited.backRu, '');
    assert.equal(canSend(edited), true);
});

test('сохранённый черновик без перевода не считается готовым к отправке', () => {
    const draft = draftFromView({threadId: 5, textRu: 'привет', textEn: '', backRu: ''});

    assert.equal(draft.translatedFrom, '');
    assert.equal(canSend(draft), false);

    const withEn = draftFromView({threadId: 5, textRu: 'привет', textEn: 'hi', backRu: 'привет'});

    assert.equal(canSend(withEn), true);
});

test('выбранный тред находится в списке', () => {
    const state = {...initialState(), threads, selectedId: 2};

    assert.equal(selectedThread(state).title, 'архивный');
    assert.equal(selectedThread({...state, selectedId: 99}), null);
});

import assert from 'node:assert/strict';
import {test} from 'node:test';

import {
    authorHue,
    authorInitial,
    escapeHtml,
    formatTime,
    progressText,
    renderBlocks,
    renderReactions,
    renderSpan,
} from '../src/format.js';

test('escapeHtml обезвреживает разметку из Slack', () => {
    assert.equal(escapeHtml('<img src=x onerror="a">'), '&lt;img src=x onerror=&quot;a&quot;&gt;');
    assert.equal(escapeHtml(null), '');
});

test('код-блок рендерится моноширинным блоком с языком', () => {
    const html = renderBlocks([{kind: 'code', lang: 'go', text: 'x := "<a>"'}]);

    assert.match(html, /<pre class="code" data-lang="go"><code>/);
    assert.match(html, /x := &quot;&lt;a&gt;&quot;/);
});

test('абзацы, цитаты и переносы строк', () => {
    const html = renderBlocks([
        {kind: 'paragraph', spans: [{kind: 'text', text: 'первая\nвторая'}]},
        {kind: 'quote', spans: [{kind: 'text', text: 'цитата'}]},
    ]);

    assert.match(html, /<p>первая<br\/>вторая<\/p>/);
    assert.match(html, /<blockquote>цитата<\/blockquote>/);
});

test('пустой список блоков не даёт разметки', () => {
    assert.equal(renderBlocks([]), '');
    assert.equal(renderBlocks(undefined), '');
});

test('ссылка с небезопасной схемой остаётся текстом', () => {
    const unsafe = renderSpan({kind: 'link', url: 'javascript:alert(1)', text: 'клик'});
    const safe = renderSpan({kind: 'link', url: 'https://slack.com', text: 'клик'});

    assert.equal(unsafe, 'клик');
    assert.match(safe, /^<a href="https:\/\/slack.com" target="_blank" rel="noreferrer">клик<\/a>$/);
});

test('упоминания выделяются, инлайн-код не теряет спецсимволы', () => {
    assert.equal(renderSpan({kind: 'user', text: '@alice'}), '<span class="mention">@alice</span>');
    assert.equal(renderSpan({kind: 'code', text: 'a<b'}), '<code class="inline-code">a&lt;b</code>');
});

test('реакции показываются как есть', () => {
    const html = renderReactions([{name: 'tada', count: 2}]);

    assert.match(html, /:tada: 2/);
    assert.equal(renderReactions([]), '');
});

test('время без значения не рисуется, битое время не ломает ленту', () => {
    assert.equal(formatTime(''), '');
    assert.equal(formatTime('не время'), '');
    assert.match(formatTime('2023-11-14T22:13:20Z'), /2023/);
});

test('прогресс переводится в человеческий текст', () => {
    assert.equal(progressText({stage: 'fetching'}), 'Читаю тред в Slack…');
    assert.equal(progressText({stage: 'translating', done: 3, total: 10}), 'Перевожу сообщения: 3 из 10');
    assert.equal(progressText({stage: 'translating'}), 'Перевожу сообщения…');
    assert.equal(progressText({stage: 'summarizing'}), 'Собираю «Суть» треда…');
    assert.equal(progressText(null), '');
});

test('цвет автора устойчив и различает людей', () => {
    assert.equal(authorHue('Иван Петров'), authorHue('Иван Петров'));
    assert.notEqual(authorHue('Иван Петров'), authorHue('Ann Lee'));

    const hue = authorHue('Ann Lee');

    assert.ok(Number.isInteger(hue) && hue >= 0 && hue < 360, `оттенок вне круга: ${hue}`);
    assert.ok(Number.isInteger(authorHue(undefined)));
});

test('буква на кружке автора', () => {
    assert.equal(authorInitial('ann lee'), 'A');
    assert.equal(authorInitial('  Иван'), 'И');
    assert.equal(authorInitial(''), '?');
    assert.equal(authorInitial(undefined), '?');
});

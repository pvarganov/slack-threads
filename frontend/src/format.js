// Рендер данных бэкенда в HTML. Здесь нет DOM и нет обращений к Wails —
// только чистые функции, поэтому модуль тестируется в node.

const ENTITIES = {
    '&': '&amp;',
    '<': '&lt;',
    '>': '&gt;',
    '"': '&quot;',
    "'": '&#39;',
};

/** Экранирует текст, пришедший из Slack или от переводчика. */
export function escapeHtml(text) {
    return String(text ?? '').replace(/[&<>"']/g, (ch) => ENTITIES[ch]);
}

/**
 * Устойчивый оттенок автора: один и тот же человек всегда одного цвета,
 * поэтому в длинном треде видно, кто говорит, ещё до чтения имени.
 */
export function authorHue(author) {
    const name = String(author ?? '');
    let hash = 0;

    for (const ch of name) {
        hash = (hash * 31 + ch.codePointAt(0)) % 360;
    }

    return hash;
}

/** Буква на кружке автора. */
export function authorInitial(author) {
    const name = String(author ?? '').trim();

    return name ? [...name][0].toUpperCase() : '?';
}

/** Переносы строк внутри абзаца сохраняются: в Slack они значимы. */
function withBreaks(html) {
    return html.replace(/\n/g, '<br/>');
}

// Ссылка вставляется в href только с безопасной схемой: остальное
// показывается текстом, чтобы javascript: не попал в разметку.
const SAFE_URL = /^(https?:|mailto:|slack:)/i;

/** Рендерит один инлайн-фрагмент абзаца или цитаты. */
export function renderSpan(span) {
    const text = withBreaks(escapeHtml(span.text));

    switch (span.kind) {
        case 'code':
            return `<code class="inline-code">${escapeHtml(span.text)}</code>`;
        case 'link': {
            const url = String(span.url ?? '');

            if (!SAFE_URL.test(url)) {
                return text;
            }

            return `<a href="${escapeHtml(url)}" target="_blank" rel="noreferrer">${text || escapeHtml(url)}</a>`;
        }
        case 'user':
        case 'channel':
        case 'broadcast':
            return `<span class="mention">${text}</span>`;
        default:
            return text;
    }
}

/** Рендерит блоки сообщения: абзацы, цитаты и код-блоки. */
export function renderBlocks(blocks) {
    if (!Array.isArray(blocks) || blocks.length === 0) {
        return '';
    }

    return blocks.map(renderBlock).join('');
}

function renderBlock(block) {
    const spans = (block.spans ?? []).map(renderSpan).join('');

    switch (block.kind) {
        case 'code': {
            const lang = block.lang ? ` data-lang="${escapeHtml(block.lang)}"` : '';

            return `<pre class="code"${lang}><code>${escapeHtml(block.text)}</code></pre>`;
        }
        case 'quote':
            return `<blockquote>${spans}</blockquote>`;
        default:
            return `<p>${spans}</p>`;
    }
}

/** Реакции показываются как есть: :name: и счётчик. */
export function renderReactions(reactions) {
    if (!Array.isArray(reactions) || reactions.length === 0) {
        return '';
    }

    const items = reactions
        .map((r) => `<span class="reaction">:${escapeHtml(r.name)}: ${escapeHtml(r.count)}</span>`)
        .join('');

    return `<div class="reactions">${items}</div>`;
}

const TIME_FORMAT = new Intl.DateTimeFormat('ru-RU', {
    day: '2-digit',
    month: '2-digit',
    year: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
});

/** Время бэкенд отдаёт в RFC 3339; показываем в локальной зоне. */
export function formatTime(iso) {
    if (!iso) {
        return '';
    }

    const date = new Date(iso);

    if (Number.isNaN(date.getTime())) {
        return '';
    }

    return TIME_FORMAT.format(date);
}

/** Текст индикатора прогресса для события sync:progress. */
export function progressText(progress) {
    if (!progress || !progress.stage) {
        return '';
    }

    switch (progress.stage) {
        case 'fetching':
            return 'Читаю тред в Slack…';
        case 'translating': {
            const total = progress.total ?? 0;

            if (!total) {
                return 'Перевожу сообщения…';
            }

            return `Перевожу сообщения: ${progress.done ?? 0} из ${total}`;
        }
        case 'summarizing':
            return 'Собираю «Суть» треда…';
        case 'done':
            return 'Готово';
        default:
            return '';
    }
}

# Контракт `claude` CLI (проверено эмпирически)

Версия бинарника: **2.1.267 (Claude Code)**, macOS darwin/arm64, дата проверки — 2026-09-10.

Всё ниже снято с живого бинарника (`claude --help` и реальные прогоны), а не по памяти.
При обновлении CLI документ нужно перепроверять — формы событий не гарантированы стабильными.

## 1. Флаги, используемые приложением

| Флаг | Значение для нас |
|---|---|
| `-p`, `--print` | неинтерактивный режим; обязателен для `--input-format`/`--output-format` |
| `--input-format <text\|stream-json>` | `stream-json` — построчный JSON на stdin, работает только с `--print` |
| `--output-format <text\|json\|stream-json>` | `stream-json` — построчный JSON на stdout |
| `--verbose` | обязателен для `stream-json` вывода, иначе поток урезан |
| `--append-system-prompt <prompt>` | сюда уходят правила перевода и глоссарий |
| `--system-prompt <prompt>` | полная замена системного промпта (не используем) |
| `--model <model>` | алиас (`opus`, `sonnet`, `fable`) или полное имя (`claude-opus-5`) |
| `-r`, `--resume <session-id>` | продолжение сессии треда после перезапуска приложения |
| `--fork-session` | при `--resume` создаёт новый session id; нам НЕ нужен — сессия должна оставаться той же |
| `--session-id <uuid>` | задать id сессии заранее (валидный UUID) |
| `--tools <list>` | ограничение набора встроенных инструментов; `""` (пустая строка) отключает все |
| `--strict-mcp-config` | игнорировать все MCP-серверы пользователя (без него MCP-инструменты подтягиваются даже при `--tools ""`) |
| `--permission-mode <mode>` | `acceptEdits\|auto\|bypassPermissions\|manual\|dontAsk\|plan`; используем `dontAsk` |
| `--permission-prompts <host\|none>` | `none` — всё, что запросило бы разрешение, отклоняется автоматически |
| `--no-session-persistence` | не сохранять сессию на диск; нам НЕ нужен, `--resume` требует персистентности |
| `--replay-user-messages` | ре-эмит пользовательских сообщений на stdout для подтверждения; опционально |
| `--json-schema <schema>` | валидация структурированного ответа по JSON Schema — кандидат для перевода батчей |
| `--max-budget-usd <amount>` | предохранитель по стоимости (только с `--print`) |

⚠️ Важно: `--tools ""` отключает только встроенные инструменты. MCP-серверы пользователя
всё равно подключаются (в пробном прогоне в `system/init` приехал список
`mcp__claude_ai_Google_Drive__*`). Для изолированного переводчика нужны **оба** флага:
`--tools "" --strict-mcp-config`.

## 2. Команда, которую запускает приложение

```
claude -p \
  --input-format stream-json \
  --output-format stream-json \
  --verbose \
  --strict-mcp-config \
  --tools "" \
  --permission-mode dontAsk \
  --permission-prompts none \
  --model opus \
  --append-system-prompt "<правила перевода + глоссарий>"
```

Для восстановления сессии треда добавляется `--resume <threads.claude_session_id>`
(без `--fork-session`, чтобы id остался прежним).

## 3. Формат запроса (stdin)

Одна строка JSON на сообщение, поток остаётся открытым — процесс живёт, пока открыт stdin:

```json
{"type":"user","message":{"role":"user","content":[{"type":"text","text":"…"}]}}
```

Невалидная строка не роняет процесс: в stderr печатается
`Error parsing streaming input line (type=unknown, N chars): SyntaxError`,
процесс продолжает работу и завершается с кодом 0.

## 4. Формат ответа (stdout)

Построчный JSON. За один пользовательский ход приходят события в таком порядке:

### `system` / `init` — в начале **каждого** хода (не только первого)

```json
{"type":"system","subtype":"init","cwd":"…","session_id":"a0663cdc-…",
 "tools":[],"mcp_servers":[…],"model":"claude-sonnet-5","permissionMode":"dontAsk"}
```

### `rate_limit_event` — может прийти в любой момент, игнорируем

```json
{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","resetsAt":1789040400,
 "rateLimitType":"five_hour","unifiedWindows":{…}},"uuid":"…","session_id":"…"}
```

Поле `rate_limit_info.status` стоит логировать: при `status != "allowed"` перевод
длинного треда упрётся в лимит подписки.

### `assistant` — собственно ответ модели

```json
{"type":"assistant",
 "message":{"model":"claude-sonnet-5","id":"msg_…","type":"message","role":"assistant",
            "content":[{"type":"text","text":"Сборка завершилась неудачно на CI."}],
            "stop_reason":null,"usage":{…}},
 "parent_tool_use_id":null,"session_id":"a0663cdc-…","uuid":"…",
 "timestamp":"2026-09-10T08:51:42.298Z","request_id":"req_…"}
```

Текст ответа собирается из блоков `message.content[]` с `type == "text"`.

### `result` — конец хода, сигнал «можно слать следующее сообщение»

```json
{"type":"result","subtype":"success","is_error":false,"num_turns":1,
 "result":"Сборка завершилась неудачно на CI.","session_id":"a0663cdc-…",
 "duration_ms":…,"duration_api_ms":2812,"total_cost_usd":0.0258,"stop_reason":"end_turn",
 "terminal_reason":"completed","permission_denials":[],"usage":{…},"modelUsage":{…},
 "uuid":"…"}
```

Полный набор ключей события `result`:
`api_error_status`, `duration_api_ms`, `duration_ms`, `fast_mode_disabled_reason`,
`fast_mode_state`, `first_content_frame_ms`, `is_error`, `modelUsage`, `num_turns`,
`permission_denials`, `queued_turn_count`, `result`, `session_id`, `stop_reason`,
`subagent_stats`, `subtype`, `terminal_reason`, `time_to_request_ms`, `total_cost_usd`,
`ttft_ms`, `ttft_stream_ms`, `type`, `usage`, `uuid`.

Готовый текст ответа дублируется в `result.result` — читать его проще, чем склеивать
`assistant`-блоки, но полагаться стоит на оба (при пустом `result` fallback на `assistant`).

## 5. Правила чтения потока для `internal/translate`

1. Читать stdout построчно, каждую строку разбирать как JSON; неизвестные `type` игнорировать.
2. Ход считается завершённым по событию `type == "result"`. Только после него слать следующий запрос.
3. Ошибка хода: `is_error == true` либо `subtype != "success"`. Текст ошибки — в `result`,
   код API-ошибки — в `api_error_status`.
4. `session_id` берётся из первого события с этим полем (обычно `system/init`) и сохраняется
   в `threads.claude_session_id`. В рамках одного процесса он не меняется; при `--resume`
   без `--fork-session` он тоже остаётся прежним (проверено).
5. Контекст сохраняется между ходами одного процесса: второй запрос видит первый
   (проверено — модель воспроизвела текст из предыдущего хода).
6. Закрытие stdin завершает процесс с кодом 0.
7. stderr читать отдельно и логировать — туда идут ошибки разбора входа, не прерывающие процесс.

## 6. Что проверено прогонами

- одиночный ход через pipe: события `rate_limit_event`, `system/init`, `assistant`, `result`;
- долгоживущий процесс с двумя ходами: `session_id` один и тот же, `system/init` приходит
  на каждый ход, контекст первого хода доступен во втором;
- `--resume <id>` в новом процессе: сессия продолжена, `session_id` не изменился, модель
  помнит содержимое предыдущего разговора;
- невалидная строка на stdin: ошибка в stderr, процесс жив, exit code 0;
- `--tools ""` без `--strict-mcp-config`: MCP-инструменты остаются доступны.

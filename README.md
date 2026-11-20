# SOS Allert Backend

Proximity-based emergency dispatch backend written in Go with Fiber. A client reports its position in the background; when someone triggers an SOS, the service stores the incident, finds active users nearby, and fans the alert out to push, SMS, and live-tracking clients while the incident moves through a small state machine.

- **MySQL** keeps the durable data: incidents, breadcrumbs, media metadata, emergency contacts, and push subscriptions.
- **Redis** is the operational store: geospatial index, sessions, rate limits, idempotency keys, and the pending audio classification.
- **Kafka is optional.** When enabled, `cmd/worker` consumes events and becomes the only notifier.

> This file documents behaviour and configuration **names**, never values. Copy `config.json.example` to `config.json` and fill it in locally. `config.json` and `ca.pem` are gitignored; never commit them.

## How an alert flows

1. The client sends `PUT /api/location` at least every two minutes.
2. The client sends `POST /api/sos`, optionally with a `description` or a recent audio classification.
3. The alert is committed to MySQL, then recipient search runs on Redis GEO with an adaptive radius (1, 2, or 5 km).
4. A 10-second grace period lets the sender cancel with a safe PIN before anything is sent.
5. The dispatcher sends Web Push, FCM, APNs, SMS to the sender's emergency contacts, and publishes to Kafka when enabled.
6. Responders answer with an ETA, stream breadcrumbs over WebSocket, and the alert closes as `RESOLVED`, `CANCELLED`, or `EXPIRED`.

```mermaid
sequenceDiagram
    participant App
    participant API
    participant Redis
    participant DB
    participant Notifier
    App->>API: PUT /api/location
    API->>Redis: GEOADD users:locations + HSET last_seen
    API->>DB: upsert locations
    App->>API: POST /api/sos
    API->>DB: INSERT alerts (TRIGGERED)
    API->>Redis: GEOSEARCH (adaptive radius)
    API-->>App: 201 {alert_id, recipients_notified}
    Note over API: 10 s grace period
    API->>Notifier: push / SMS / Kafka
```

## Implemented features

Legend: ✅ done and unit-tested · 🟡 implemented but not exercised with a real provider · 🔵 planned.

### Accounts and sessions

- ✅ **Register, login, logout** — `POST /api/users`, `POST /api/users/_login`, `DELETE /api/users`.
- ✅ **Current profile** — `GET` and `PATCH /api/users/_current` (profile, safe PIN, push tokens).
- ✅ **Sessions** — raw token in `Authorization` (no `Bearer` needed), stored only in Redis with a TTL (default 168 h); a new login replaces the old token.
- ✅ **Safe PIN** — 4 digits, bcrypt hash, required to cancel an alert. A wrong PIN is `403`, never `401`.
- ✅ **Auth rate limit** — 5 attempts per minute per IP, then a 15-minute lockout (`429` + `Retry-After`); fails open when Redis is down.

### Location

- ✅ **Heartbeat** — `PUT /api/location`; Redis first, MySQL snapshot second.
- ✅ **Read own position** — `GET /api/location` returns the last heartbeat plus a `stale` flag.
- ✅ **Nearby responders** — `GET /api/location/nearby`; radius 100–5000 m (default 1000), max 50, masked coordinates, stale users dropped.

### SOS lifecycle

- ✅ **Trigger** — `POST /api/sos`; body coordinates optional (falls back to the last heartbeat), idempotency key optional.
- ✅ **Idempotency** — `X-Idempotency-Key` with a 5-minute replay window; a finished replay is answered before the rate limiter, so retries do not burn quota.
- ✅ **Rate limit** — 3 triggers per hour per user by default; can be disabled.
- ✅ **Adaptive radius** — starts at 1/2/5 km depending on severity, widens until at least 3 fresh users, capped at 50.
- ✅ **Grace period** — 10 s default, stored as `alerts.dispatch_due_at` and dispatched by a durable outbox loop (at-least-once).
- ✅ **State machine** — `TRIGGERED → ACKNOWLEDGED → RESOLVED`, plus `CANCELLED` and `EXPIRED`; acknowledge and resolve rules are enforced in the use case.
- ✅ **Cancel** — safe PIN plus reason (`FALSE_ALARM`, `ACCIDENTAL`, `SAFE_NOW`, `TEST`).
- ✅ **Active, inbox, history, summary** — history is paged across all statuses with `role=sent|received`.
- ✅ **Breadcrumbs** — REST and WebSocket; a client timestamp older than 24 h is rejected.
- ✅ **Media** — multipart upload up to 50 MB, local storage, authenticated download.
- ✅ **Housekeeping** — expires `TRIGGERED` alerts after 24 h and purges breadcrumbs after 30 days.

### Responders and live tracking

- ✅ **Respond with ETA** — `POST /api/sos/:alertId/respond` returns distance and ETA; a duplicate answer is `409`.
- ✅ **WebSocket tracking** — `/ws/sos/:alertId/track` broadcasts `LOCATION_UPDATE`, `ALERT_STATUS`, `ALERT_TRIAGE`, and `RESPONDER_ADDED`.
- ✅ **Socket budgets** — 500 global, 10 per IP, 3 per user; ping/pong drops half-open connections.
- ✅ **Participant-only data** — responder identities and precise coordinates only for the sender and recorded responders.

### Notifications

- ✅ **Alert dispatcher** — FCM v1, APNs, Twilio SMS, and Web Push; a channel without credentials logs instead of failing the alert.
- ✅ **Web Push (VAPID)** — no Firebase needed; subscriptions live in `push_subscriptions`, endpoints are allowlisted, dead ones are deleted.
- ✅ **Kafka worker** — `sos.dispatched` plus user/contact/address mirrors, each with its own consumer group.
- ✅ **Retry and DLQ** — same-message retry with backoff, then publish to `sos.dispatched.dlq` with the original payload and source headers.
- ✅ **Emergency contacts** — max 10 per user, E.164 only, deduplicated, so the SMS target list is bounded.
- ✅ **Push tokens** — `fcm_token` / `apns_token` via profile; without one, FCM falls back to topic `user_<id>`.
- 🟡 **Live provider credentials** — the FCM, APNs, and Twilio clients are tested against stub servers, not with real credentials in this repo.

### AI and operations

- ✅ **Text triage** — `description` → emergency type, severity, and summary; fail-open.
- ✅ **Audio transcription** — OpenAI-compatible Whisper provider, audio ≤ 25 MB, silence detection.
- ✅ **Pre-trigger classification** — `POST /api/sos/classify` stores nothing; the result is handed to the next trigger through Redis.
- ✅ **Post-audio escalation** — a higher severity widens the fan-out and notifies only the newly reached responders.
- 🟡 **Live LLM providers** — Jev, OpenAI, and Ollama clients exist; only the offline classifier path is unit-tested end to end.
- ✅ **Metrics** — `/metrics` is registered only when `metrics.token` is set, and scraping requires `Authorization`.
- ✅ **Graceful shutdown** — SIGINT/SIGTERM drains HTTP for 10 s and stops background loops.
- ✅ **CORS and masking** — production plus local dev origins only; non-participants get coordinates rounded to about 100 m.
- 🔵 **TLS** — not implemented at the application layer (see production notes).

## AI triage

Triage turns a short incident description or audio clip into a classification. It is optional and never blocks an alert.

| Output | Meaning |
|---|---|
| `emergency_type` | `DANGER`, `MEDICAL`, or `ACCIDENT` |
| `severity` | score from 0 to 1, used to choose the first fan-out radius |
| `summary` | short Indonesian summary shown in notifications and detail responses |

### Where the classification comes from

1. **During the trigger** — the `description` field (up to 1000 characters) is classified inline.
2. **Before the trigger** — `POST /api/sos/classify` accepts a short audio clip, transcribes it, and classifies the transcript. Nothing is persisted; the server keeps the result in Redis for 2 minutes, one key per user, single use. The next trigger consumes it, so the first fan-out already matches the clip and a client cannot inflate the severity by editing the request.
3. **After the trigger** — uploading audio to `/api/sos/:alertId/media` transcribes it, updates the alert, and broadcasts `ALERT_TRIAGE` over the tracking socket.

Priority rules:

- A pending audio classification wins over description triage.
- An explicit `emergency_type` in the trigger body wins for the type; severity still comes from triage.
- `client_severity` and `client_summary` are the sender's claim. They are echoed and labelled "Pelapor menyebut: …" in the notification, but never drive triage or the radius.

### Provider chain

| `ai.provider` | Order tried |
|---|---|
| `offline` (default) | offline keyword classifier |
| `jev` | Jev → OpenAI (only if its key is configured) → offline |
| `ollama` | Ollama → offline |
| `openai` | OpenAI → offline |

A stage that errors, times out, or returns no emergency type is logged and the next stage is tried. If every stage fails, the alert still triggers with `DANGER` and a 1 km start radius.

### Offline classifier

The default classifier is deterministic and needs no credentials. It matches Indonesian and English keywords (strong weight 2, weak weight 1), resolves ties by life-safety priority `MEDICAL > DANGER > ACCIDENT`, starts severity at 0.6, adds 0.2 per urgency keyword, and caps at 1.0.

### Severity picks the first radius

| Severity | Start radius |
|---|---|
| 0.00 – 0.33 | 1 km |
| 0.34 – 0.66 | 2 km |
| 0.67 – 1.00 | 5 km |

The search then widens on its own until it has at least 3 fresh recipients, never more than 50, nearest first. A severity of `0` is a real score and starts at 1 km; `alerts.severity = NULL` means the incident was never classified.

### Audio and silence handling

Audio evidence is transcribed by an OpenAI-compatible provider (default: Groq `whisper-large-v3-turbo`, Indonesian). Only audio up to 25 MB is sent. Transcription status is `disabled`, `too_large`, `completed`, `empty`, or `failed`.

A silent clip is not a transcript: high `no_speech_prob` across segments, a repetition loop, a very low log probability on short text, a clip under 0.5 s, or a known Whisper hallucination such as "terima kasih" produces `empty` with no triage and no `transcribed_at`.

### Escalation after audio

When post-trigger audio raises the severity enough to move to a wider radius, the backend notifies only responders not already recorded for that alert, waits out the remaining grace period, and re-checks the alert status so a cancelled alert never spreads.

It publishes a second `sos.dispatched` event with `push_only: true` so the sender's contacts are not texted twice, and the media response reports `escalated` and `recipients_added`.

### Failure behaviour

AI is advisory only. A disabled engine, missing credentials, a provider error, or an empty description all fall back to request defaults. There is no inline retry on provider rate limits, because a retry would delay the SOS itself.

Notification payloads carry `severity` and `summary` only when a classification exists. Configuration names live under `ai.*` and `ai.stt.*`; values belong in the local `config.json`.

## How locations are stored and used

Two systems hold location data, with different jobs.

| Store | Contents | Role |
|---|---|---|
| Redis GEO `users:locations` | member = user id, value = `(longitude, latitude)` | operational index for radius search |
| Redis Hash `users:last_seen` | user id → epoch milliseconds of the last heartbeat | freshness filter |
| MySQL `locations` | one row per user (`UNIQUE user_id`) | audit copy of the latest position, not a track |

### Write path

`PUT /api/location` requires both `latitude` and `longitude`. It writes `GEOADD users:locations`, then `HSET users:last_seen`. If either Redis write fails, the heartbeat fails. After Redis succeeds, MySQL upserts the `locations` row; a MySQL failure is only logged and the heartbeat still succeeds.

Neither Redis structure has a TTL. Old positions stay, but users whose `last_seen` is older than two minutes are filtered out by every reader, so they stop receiving alerts and disappear from the nearby list without being deleted.

### Read paths

- `GET /api/location` reads the caller's position and timestamp and reports `stale`.
- `GET /api/location/nearby` runs `GEOSEARCH` with distance, drops the caller, reads `last_seen` for all candidates in one `HMGET`, drops stale users, masks coordinates to about 100 m, rounds distances to 100 m buckets, and caps the list at 50.
- The SOS trigger reads body coordinates first and falls back to this Redis position when the body omits them. With no stored heartbeat either, the request is `422`.

### What the data is used for

1. **Recipient selection for SOS** — `GEOSEARCH` inside 1/2/5 km, bounded candidate count, nearest first, capped at 50.
2. **Coordinate fallback** — a trigger without body coordinates still knows where the sender is.
3. **Responder map** — the nearby endpoint the app shows before or during an incident.
4. **Freshness filtering** — a user must heartbeat at least every two minutes to stay reachable.
5. **Privacy** — nearby and non-participant views are always masked; only the sender and recorded responders see the precise point in an incident detail.

### Boundaries to keep in mind

- MySQL `locations` is a single latest snapshot per user, not movement history. The trail lives in `alert_breadcrumbs`.
- Redis is the source of truth for fan-out and the nearby map; MySQL is only the audit copy.
- Location data is not encrypted by the application, because TLS is not wired into the MySQL or Redis clients.

## API overview

All responses use `{ "data": ... }`, with `paging` on list endpoints and `{ "errors": ... }` on failure. Timestamps are epoch milliseconds. Full schemas are in [`api/api.md`](api/api.md) and [`api/api-spec.json`](api/api-spec.json); recent contract changes are in [`docs/api-changes.md`](docs/api-changes.md).

| Method | Path | Auth | Notes |
|---|---|:---:|---|
| `POST` | `/api/users`, `/api/users/_login` | — | Register or log in; login returns the raw session token |
| `GET/PATCH` | `/api/users/_current` | ✅ | Profile, safe PIN, push tokens |
| `DELETE` | `/api/users` | ✅ | Logout and revoke the session |
| `GET/POST` | `/api/contacts` | ✅ | Legacy contact CRUD |
| `PUT/GET/DELETE` | `/api/contacts/:contactId` | ✅ | Legacy contact CRUD |
| `GET/POST` | `/api/contacts/:contactId/addresses` | ✅ | Legacy address CRUD |
| `PUT/GET/DELETE` | `/api/contacts/:contactId/addresses/:addressId` | ✅ | Legacy address CRUD |
| `PUT/GET` | `/api/location` | ✅ | Send or read the heartbeat |
| `GET` | `/api/location/nearby` | ✅ | Active users around the caller |
| `POST` | `/api/sos` | ✅ | Trigger an alert; body and `X-Idempotency-Key` optional |
| `POST` | `/api/sos/classify` | ✅ | Pre-trigger audio classification (nothing persisted) |
| `GET` | `/api/sos/active`, `/api/sos/inbox` | ✅ | Current incidents, or incidents the caller was notified about |
| `GET` | `/api/sos/history`, `/api/sos/summary` | ✅ | Paged history and aggregate counters |
| `GET` | `/api/sos/:alertId` | ✅ | Detail; masked unless the caller is a participant |
| `POST` | `/api/sos/:alertId/cancel` | ✅ | Safe PIN and reason |
| `POST` | `/api/sos/:alertId/acknowledge`, `.../resolve` | ✅ | Lifecycle transitions |
| `POST` | `/api/sos/:alertId/respond` | ✅ | "I am coming"; returns ETA and distance |
| `GET/POST` | `/api/sos/:alertId/breadcrumbs` | ✅ | Trail history and live points |
| `POST` | `/api/sos/:alertId/media` | ✅ | Upload media; audio is transcribed and may escalate |
| `GET` | `/api/sos/:alertId/media`, `.../media/:mediaId` | ✅ | List or download media |
| `GET/POST` | `/api/emergency-contacts` | ✅ | Tier-1 SMS contacts |
| `GET` | `/api/push/vapid-public-key` | ✅ | Browser push public key |
| `GET/POST/DELETE` | `/api/push/subscriptions` | ✅ | Browser push subscriptions |
| `WS` | `/ws/sos/:alertId/track` | in-band | First frame `{"type":"auth","token":"..."}`, then `LOCATION_UPDATE` broadcasts |
| `GET` | `/metrics` | token | Prometheus metrics, only when `metrics.token` is configured |

## Running locally

Requirements: Go 1.22+, MySQL, Redis 7+. Kafka is optional.

```bash
cp config.json.example config.json   # fill in local values
migrate -path db/migrations -database "mysql://DB_USER:DB_PASSWORD@tcp(DB_HOST:DB_PORT)/DB_NAME" up

go run cmd/web/main.go      # HTTP API on :3000
go run cmd/worker/main.go   # Kafka consumers, only needed with Kafka enabled
go build ./... && go vet ./...
```

The web process refuses to start when MySQL or Redis is unreachable. An unreachable Kafka broker does not: producers and consumers retry a few times, then disable themselves, and the web process notifies directly.

## Production notes

- **Load test baseline**: a read-only k6 run against production (741 requests, 2 → 10 req/s, 0% failures, p95 ≈ 1.6 s) is documented in [`perf/k6/`](perf/k6/README.md) with the dashboard and the raw metric stream.
- **Behind a proxy**, set `web.proxy_header` (for example `X-Forwarded-For`) and `web.trusted_proxies`. Without it every client appears to come from the proxy, so the per-IP limiter becomes one global bucket.
- **TLS is not implemented.** The MySQL DSN has no TLS parameter and Redis has no TLS config; terminate TLS at the provider or proxy.
- **Kafka producer is off by default.** With it on, the worker sends notifications; with it off, the web process does. Both read the same MySQL data, so notifications are not sent twice.
- **Web Push needs a secure context** (`https://` or `localhost`) and an allowed push-service host.
- **CORS is locked** to the production origin and the local dev origin in `internal/config/fiber.go`.
- **Shutdown drains** for 10 seconds. Undispatched grace-period alerts are safe because their due time is in MySQL, not in a timer.
- **Metrics** are not public by default; the route only exists when a token is configured.

## Not implemented

- PSAP / 911 / E911 dispatch integration, an emergency medical ID card, and geofenced safe zones.
- Anonymous in-incident chat or WebRTC voice.
- S3/GCS media storage (uploads are local) and OpenTelemetry tracing.
- Fall/crash sensor flags and network-quality telemetry.
- Automatic DLQ replay; replaying `sos.dispatched.dlq` is manual.
- A durable escalation timer: post-audio escalation still uses an in-process timer for the remaining grace period, unlike the trigger outbox.

## Tests

```bash
go test ./internal/... -v -count=1   # no live infrastructure needed
go test ./... -v -count=1            # also needs config.json and a reachable Redis
go test ./test/ -run '^TestCalculateDistance' -v -count=1
```

The `test/` package is stateful and mutates a live Redis (it deletes a fixture user), so point it at an isolated instance. Coverage focuses on the trigger flow, idempotency, rate limits, adaptive radius, lifecycle, AI triage, dispatch clients via fake servers, Kafka consumer retry/DLQ, WebSocket tracking, and the location helpers.

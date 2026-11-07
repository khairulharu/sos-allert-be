# AGENTS.md

SOS Allert backend: Go service derived from the `golang-clean-architecture` template. The Go module is still named `golang-clean-architecture`; all imports depend on that name.

## Commands

```bash
go run cmd/web/main.go        # HTTP API on :3000
go run cmd/worker/main.go     # Kafka consumers (topic: users, contacts, addresses)
go build ./... && go vet ./...
go test ./test/ -v -count=1   # all tests (see caveats below)
go test ./test/ -run '^TestCalculateDistance$' -v -count=1
```

- No Makefile, CI, linter, formatter config, or codegen. `gofmt` is the only convention.
- README's `go test ./test/init.go ./test/location_test.go` compiles only those two files and silently skips `geolocation_test.go`; prefer the package command.
- Migrations use the `migrate` CLI against `db/migrations`; live-DB command and TLS setup are in `Archi.Md`.

## Configuration / runtime

- `config.json` and `ca.pem` are gitignored but required. `config.NewViper` searches `./../` then `./` and panics if absent; keep them at repo root. Never commit the live credentials in them. `config.json.example` is the committed template holding the full key set; keep it in sync when config keys change. Behind a proxy/tunnel, `web.proxy_header` (e.g. `X-Forwarded-For`) is required or the per-IP auth limiter degrades into one global bucket. Set `web.trusted_proxies` (IP/CIDR) so `ctx.IP()` honors `X-Forwarded-For` only from those proxies (blocks direct-access spoofing; without it the header is honored unconditionally, warned at startup). CORS is locked to `https://allert.my.id` + `http://localhost:5173` in `internal/config/fiber.go`.
- Web startup hard-fails if MySQL or Redis is unreachable. Worker startup needs Kafka (`kafka.bootstrap.servers`, default `localhost:9092`).
- Kafka producer is disabled by default (`kafka.producer.enabled: false`) and producers are nil-checked in use cases, so the web app runs without Kafka.
- Persistence is MySQL (Aiven), despite README listing Supabase/PostgreSQL. Timestamps are millisecond `bigint` via GORM `autoCreateTime:milli`.
- `README.md` and `Archi.Md` are partly stale notes, and `Archi.Md` is gitignored. Trust code/config; e.g. README's `location_repostory.go` does not exist.

## Layout

- `cmd/web` = Fiber app; `cmd/worker` = Kafka consumers.
- `internal/delivery/http` = controllers, routes, auth middleware; `internal/delivery/messaging` = consumers.
- `internal/usecase` = business logic and transactions; `internal/repository` = GORM/Redis access; `internal/gateway/messaging` = producers.
- `internal/entity` = GORM entities; `internal/model` = API models/events plus `converter/`.
- `internal/config/app.go` is the dependency graph. New repositories/use cases/controllers must be wired there and in `route.go`.
- Alert is spelled both ways: `AllertUseCase`, `NewAllertRepository`, `allert_entity.go` vs `model.Alert`, `alert_usecase.go`. Grep both spellings.

## Auth

- Guest routes: `POST /api/users`, `POST /api/users/_login`. All other routes go through `AuthMiddleware`.
- Guest auth routes are rate-limited per client IP by `middleware.NewAuthRateLimit`: 5 attempts/minute, then a 15-minute lockout (`429` + `Retry-After`). Register and login share the same bucket. The limiter fails open on Redis errors so auth stays available; config is `auth.rate_limit_per_minute` / `auth.lockout_minutes`. Behind a reverse proxy set `web.proxy_header` (e.g. `X-Forwarded-For`) so `ctx.IP()` is the real client IP.
- `Authorization` is the raw token returned by login, not `Bearer <token>`.
- Middleware loads the user ID into `ctx.Locals("auth")`; controllers get it with `middleware.GetUser(ctx)` and set `UserId` fields marked `json:"-"`.

## Location / SOS status

- Two distinct stores: `entity.Location` is the MySQL `locations` table; `entity.LocationCache` is Redis. Redis GEO is keyed by `users:locations`; longitude comes before latitude (Redis API order).
- `LocationCacheRepository` implements Save/Get/Delete/GetAll plus radius search (`GeoSearch`, meters) via `GeoAdd`, `GeoPos`, `ZRange`, `ZRem`, `GEOSEARCH`.
- `TriggerSOSUseCase.ExecuteWithRequest(ctx, senderID, req, idempotencyKey)` — request body coords (fallback: Redis), idempotency, rate limit, adaptive radius (1/2/5 km, cap 50), grace-period dispatch, Kafka publish when enabled. `Execute(ctx, senderID)` is the legacy Redis-only wrapper. The per-user trigger limit (default 3/hour) is skipped when `sos.rate_limit_disabled: true` (default false); `X-Idempotency-Key` and the grace period are unaffected. Lifecycle methods on the same struct: `GetAlert`, `ListActive`, `Cancel` (bcrypt safe PIN), `Acknowledge`, `Resolve`, `Respond` (ETA), `AddBreadcrumb`/`ListBreadcrumbs`, emergency contacts, `UploadMedia`.
- `TriggerSOSUseCase` depends on interfaces: `LocationRepository` (Redis GEO + `Save`), `AlertStore`, `Notifier` (satisfied by `*AlertDispatcher`, FCM/APNs/Twilio, log-only when unconfigured), `IdempotencyStore`, `RateLimiter`, `EventPublisher`, `TokenReader`/`UserReaderRepository`; all are faked in unit tests without live infra.
- Redis locations have no TTL. `users:last_seen` is written by `LocationCacheRepository.Save` and used for the >2 min stale filter in `findRecipients` and `GET /api/location/nearby`. Both readers resolve it with `GetLastSeenBatch` (one `HMGET` per radius/candidate set) instead of `GetLastSeen` per candidate: the per-candidate form cost a Redis round trip per nearby user (~53 round trips, ~300 ms at 5 ms RTT, for a 50-responder payload; 3 round trips after batching). A batch failure drops the candidates whose freshness is unknown, matching the previous per-user skip.
- `PUT /api/location` also upserts one historical row per user into MySQL `locations` (unique `user_id`, migration `20261107000000_locations_user_unique`); MySQL failures are logged but do not fail the heartbeat. `GET /api/location` reads Redis and returns `last_seen`/`stale`. `GET /api/location/nearby` uses Redis `GEOSEARCH` with distance, excludes the requester and stale users, caps at 50, and masks coordinates (~100 m).
- Wired endpoints: `GET/PUT /api/location`, `GET /api/location/nearby`, `POST /api/sos`, `POST /api/sos/classify` (pre-trigger audio classification, nothing persisted), `GET /api/sos/active`, `GET /api/sos/inbox`, `GET /api/sos/:alertId`, cancel/acknowledge/resolve/respond, breadcrumbs, media (`POST /api/sos/:alertId/media`, `GET …/media`, `GET …/media/:mediaId`), `GET/POST /api/emergency-contacts`, browser push (`GET /api/push/vapid-public-key`, `GET/POST/DELETE /api/push/subscriptions`), `WS /ws/sos/:alertId/track?token=`, `GET /metrics`. The WS route is registered before `AuthMiddleware` in `route.go`; it broadcasts `LOCATION_UPDATE`, `ALERT_STATUS` (cancel/acknowledge/resolve), and `ALERT_TRIAGE` (post-upload audio triage).
- Browser Web Push (VAPID, no Firebase credentials): `webpush.enabled|vapid_public_key|vapid_private_key|subscriber|ttl_seconds` gates `usecase.WebPushNotifier`, wired as `AlertDispatcher.Web`; subscriptions live in MySQL `push_subscriptions` (`endpoint` unique → re-subscribe upserts, a 404/410 answer deletes the row). Code: `entity/push_subscription_entity.go`, `repository/push_subscription_repository.go`, `usecase/webpush_notifier.go` + `usecase/push_usecase.go`, `delivery/http/push_controller.go`, `db/migrations/20261109000000_push_subscriptions.*`; the browser side is `frontend/public/sw.js` + `frontend/src/lib/push.js`. `subscriber` is a bare email (the library prefixes `mailto:`). Web push only fires in the web process, so it needs `kafka.producer.enabled: false`; the worker passes `nil` for the new dispatcher argument (it has no DB).
- AI triage (optional): `TriggerSOSDeps.Triage` (`usecase.EmergencyTriage`) classifies `POST /api/sos` `description` → `emergency_type` + `severity` (0–1) + `summary`; fail-open (nil/error → request defaults, alert still triggers). `severity` picks the adaptive-radius start via `radiusStartIndex` (more severe = broader fan-out); explicit request `emergency_type` wins over AI. Providers in `internal/usecase/ai_triage.go` (composed engine), `ai_offline.go` (keyword classifier, default fallback, zero-dependency), `ai_ollama.go`/`ai_openai.go` (HTTP clients); config in `internal/config/ai.go` (`ai.enabled|provider|timeout_seconds|ollama.*|openai.*`). Explicit request `severity`/`summary` (typically from `POST /api/sos/classify`) also override the description triage, so an audio-classified incident fans out at the right radius on the first wave. Triage shows on the response as `triage`; `model.Alert.Severity`/`Summary` thread into the FCM data payload and notification body, and `model.SOSEvent` carries both so the Kafka worker sends identical content.
- Audio STT: `POST /api/sos/:alertId/media` stores the upload, then for audio ≤ 25 MB calls `TriggerSOSDeps.Transcriber` (`usecase.Transcriber`, `WhisperTranscriber` is OpenAI-compatible and defaults to Groq `whisper-large-v3-turbo`), feeds the transcript into the same `EmergencyTriage`, updates `alerts` via `AlertStore.UpdateTriage`, stores `alert_media.transcript/transcription_status/transcribed_at`, and broadcasts `ALERT_TRIAGE` on the tracking hub. STT/triage failures are fail-open; the media row is still created. Config: `internal/config/stt.go` (`ai.stt.enabled|provider|base_url|api_key|model|language|timeout_seconds`); the Groq key lives only in `config.json` → `ai.stt.api_key`. Frontend `tracking.js` must add an `ALERT_TRIAGE` handler; it currently handles `LOCATION_UPDATE`/`ALERT_STATUS`. Migration `20261108000000_alert_media_transcription` adds the media columns.
- Post-trigger escalation: `UploadMedia` compares the pre-audio `alert.Severity` with the transcript triage and, when `radiusStartIndex` grows, calls `TriggerSOSUseCase.escalateFanout`. It lists the alert's recorded recipients (`AlertStore.ListRecipients`), re-runs `findRecipients` at the wider radius, and records + notifies **only the new** responders (`RecipientNotifier.NotifyRecipients` is push-only so the sender's emergency contacts are not texted twice; the Kafka path republishes `sos.dispatched` with just the added recipients, whose worker dispatcher has no contact resolver anyway). The notification still waits out `GracePeriod` and re-checks the alert status, so a cancelled false alarm never fans out. The media response carries `escalated`/`recipients_added`. `POST /api/sos/classify` (`TriggerSOSUseCase.ClassifyAudio`) is the pre-trigger counterpart: it transcribes and classifies a clip without persisting anything and returns `model.ClassifyResponse`; the client feeds `emergency_type`/`severity`/`summary` into `POST /api/sos`, which then fans out at the audio radius on the first wave (no escalation needed). `400` for non-audio/oversize, `503` when `ai.stt` is off, `502` on provider failure.
- `AlertDispatcher` in `internal/usecase/` supersedes the log-only `AlertNotifier` (kept, unwired). Safe PIN is set via `PATCH /api/users/_current` (`safe_pin`, bcrypt hash in `users.safe_pin_hash`).
- Migrations `20261027000000_create_table_alerts` and `20261031000000_alerts_v2` add the `alerts` table and the v2 columns/tables (`alert_breadcrumbs`, `emergency_contacts`, `alert_responses`, `alert_media`, PIN/device-token columns); `20261101000000_alerts_triage` adds `alerts.severity/summary`, `20261102000000_alert_recipients` backs the responder inbox, `20261107000000_locations_user_unique` adds the unique `locations.user_id` index used by the heartbeat upsert, and `20261108000000_alert_media_transcription` adds the media transcript columns. Run them before the SOS/location endpoints work.

## Runtime Cloud

- `deploy/runtime/` runs the project on Runtime Cloud with the `withruntime` SDK. `npm run verify` builds and unit-tests in a sandbox; `npm run deploy` builds, starts the API and web app, and prints private preview URLs. See `deploy/runtime/README.md`.
- The API needs MySQL (port 18770) and Redis (15164). A sandbox without credit reaches only ports 80 and 443 outbound, so starting the API needs paid credit; the build/test job does not.
- `config.json` and `ca.pem` are uploaded to the sandbox from the working tree, never committed.

## Tests

- Test package-specific tetap berada dekat kode yang diuji agar dapat memakai
  fake/helper internal; test Redis live lintas package berada di `test/`.
- `go test ./internal/... -v -count=1` adalah suite unit/integration tanpa
  infrastruktur live dan tidak memerlukan `config.json`.
- `go test ./... -v -count=1` juga menjalankan package `test`; package tersebut
  membutuhkan `config.json` dan Redis live/network.
- `test/init.go` membuat koneksi Redis saat package diinisialisasi. Jika
  konfigurasi atau Redis tidak tersedia, package `test` gagal sebelum test
  berjalan.
- `TestGet`, `TestGetAll`, dan `TestDelete` mengasumsikan fixture `user-B`
  tersedia di `users:locations`; `TestDelete` menghapus `user-B`. Test ini
  stateful dan memutasi Redis remote, jadi gunakan Redis/database isolasi.
- `TestCalculateDistance` dan `TestCalculateDistanceSimetris` menguji helper
  Haversine dengan batas kurang dari 1 km.
- `internal/entity/alert_entity_test.go` menguji status aktif dan transisi
  state machine alert.
- Test internal lainnya mencakup TriggerSOS, lifecycle, AI triage, dispatch
  client via `httptest`, HTTP SOS, Kafka consumer, tracking hub, dan metrics.
- Jalankan suite tanpa live infra dengan `go test ./internal/... -v -count=1`.
- Build dan static check: `go build ./... && go vet ./...`.

## Code conventions

- Use cases own transactions: `tx := DB.WithContext(ctx).Begin()`, `defer tx.Rollback()`, then `tx.Commit()`. Repositories accept the `*gorm.DB`/`tx`; Redis repos use `context.Context`.
- Publish Kafka events only after commit and only when the producer is non-nil.
- Validate requests with `validate.Struct` and struct tags; return `fiber.Err*` errors so the global `ErrorHandler` renders `{"errors": ...}`.
- Generic CRUD lives in `repository.Repository[T]`; add entity-specific queries to the concrete repository.

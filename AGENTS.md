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

- `config.json` and `ca.pem` are gitignored but required. `config.NewViper` searches `./../` then `./` and panics if absent; keep them at repo root. Never commit the live credentials in them.
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
- `Authorization` is the raw token returned by login, not `Bearer <token>`.
- Middleware loads the user ID into `ctx.Locals("auth")`; controllers get it with `middleware.GetUser(ctx)` and set `UserId` fields marked `json:"-"`.

## Location / SOS status

- Two distinct stores: `entity.Location` is the MySQL `locations` table; `entity.LocationCache` is Redis. Redis GEO is keyed by `users:locations`; longitude comes before latitude (Redis API order).
- `LocationCacheRepository` implements Save/Get/Delete/GetAll plus radius search (`GeoSearch`, meters) via `GeoAdd`, `GeoPos`, `ZRange`, `ZRem`, `GEOSEARCH`.
- `TriggerSOSUseCase.ExecuteWithRequest(ctx, senderID, req, idempotencyKey)` — request body coords (fallback: Redis), idempotency, rate limit, adaptive radius (1/2/5 km, cap 50), grace-period dispatch, Kafka publish when enabled. `Execute(ctx, senderID)` is the legacy Redis-only wrapper. Lifecycle methods on the same struct: `GetAlert`, `ListActive`, `Cancel` (bcrypt safe PIN), `Acknowledge`, `Resolve`, `Respond` (ETA), `AddBreadcrumb`/`ListBreadcrumbs`, emergency contacts, `UploadMedia`.
- `TriggerSOSUseCase` depends on interfaces: `LocationRepository` (Redis GEO + `Save`), `AlertStore`, `Notifier` (satisfied by `*AlertDispatcher`, FCM/APNs/Twilio, log-only when unconfigured), `IdempotencyStore`, `RateLimiter`, `EventPublisher`, `TokenReader`/`UserReaderRepository`; all are faked in unit tests without live infra.
- Redis locations have no TTL. `users:last_seen` is written by `LocationCacheRepository.Save` and used for the >2 min stale filter in `findRecipients`.
- Wired endpoints: `PUT /api/location`, `POST /api/sos`, `GET /api/sos/active`, `GET /api/sos/:alertId`, cancel/acknowledge/resolve/respond, breadcrumbs, media, `GET/POST /api/emergency-contacts`, `WS /ws/sos/:alertId/track?token=`, `GET /metrics`. The WS route is registered before `AuthMiddleware` in `route.go`.
- `AlertDispatcher` in `internal/usecase/` supersedes the log-only `AlertNotifier` (kept, unwired). Safe PIN is set via `PATCH /api/users/_current` (`safe_pin`, bcrypt hash in `users.safe_pin_hash`).
- Migrations `20261027000000_create_table_alerts` and `20261031000000_alerts_v2` add the `alerts` table and the v2 columns/tables (`alert_breadcrumbs`, `emergency_contacts`, `alert_responses`, `alert_media`, PIN/device-token columns); run both before the SOS endpoints work.

## Runtime Cloud

- `deploy/runtime/` runs the project on Runtime Cloud with the `withruntime` SDK. `npm run verify` builds and unit-tests in a sandbox; `npm run deploy` builds, starts the API and web app, and prints private preview URLs. See `deploy/runtime/README.md`.
- The API needs MySQL (port 18770) and Redis (15164). A sandbox without credit reaches only ports 80 and 443 outbound, so starting the API needs paid credit; the build/test job does not.
- `config.json` and `ca.pem` are uploaded to the sandbox from the working tree, never committed.

## Tests

- `test/init.go` connects to the real Redis from `config.json` in package `init` (and `log.Fatalf`s if unreachable), so every test in `./test` needs live Redis/network, even `TestCalculateDistance`.
- `TestGet`, `TestGetAll`, and `TestDelete` assume data already exists in `users:locations` (including `user-B`), and `TestDelete` removes `user-B`. They are stateful/order-dependent and mutate a shared remote Redis; a full-suite failure may be missing fixture data, not a code regression.
- `TestCalculateDistance`/`TestCalculateDistanceSimetris` cover the 1 km Haversine helper (`< 1` km, boundary excluded).
- `internal/usecase/trigger_sos_usecase_test.go` and `sos_lifecycle_test.go` — unit tests for `TriggerSOSUseCase` and the lifecycle; no live infra needed (fakes for Redis, MySQL, idempotency, rate limit, PIN, Kafka, hub). Run with `go test ./internal/usecase/ ./internal/tracking/ ./internal/metrics/ -v -count=1`. Dispatch clients (FCM/APNs/Twilio) are tested with `httptest` in `dispatch_test.go`.

## Code conventions

- Use cases own transactions: `tx := DB.WithContext(ctx).Begin()`, `defer tx.Rollback()`, then `tx.Commit()`. Repositories accept the `*gorm.DB`/`tx`; Redis repos use `context.Context`.
- Publish Kafka events only after commit and only when the producer is non-nil.
- Validate requests with `validate.Struct` and struct tags; return `fiber.Err*` errors so the global `ErrorHandler` renders `{"errors": ...}`.
- Generic CRUD lives in `repository.Repository[T]`; add entity-specific queries to the concrete repository.

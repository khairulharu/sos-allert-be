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
- `TriggerSOSUseCase.Execute(ctx, senderID)` — gets sender location from Redis, inserts an `entity.Alert` row to MySQL (`alerts` table), runs `GeoSearch` 1 km, returns `*model.TriggerSOSResult{Alert, Recipients}` excluding the sender.
- `TriggerSOSUseCase` depends on two interfaces: `LocationRepository` (satisfied by `*LocationCacheRepository`) and `AlertSaver` (satisfied by `*AlertRepository`); both are faked in unit tests without live infra.
- Redis locations have no TTL. `users:last_seen` is reserved for future stale-user filtering (<2 min) but unused.
- Wired endpoints: `PUT /api/location` (update Redis GEO), `POST /api/sos` (trigger alert — auth required). Both missing from `api/api-spec.json`.
- `AllertUseCase` and `AlertNotifier` in `internal/usecase/` are legacy stubs — not wired, not used. `alert_usecase.go` and `alert_notifier_usecase.go` can be removed when FCM is implemented.
- Migration `20261027000000_create_table_alerts.up.sql` adds the `alerts` table; must be run before `POST /api/sos` works.

## Runtime Cloud

- `deploy/runtime/` runs the project on Runtime Cloud with the `withruntime` SDK. `npm run verify` builds and unit-tests in a sandbox; `npm run deploy` builds, starts the API and web app, and prints private preview URLs. See `deploy/runtime/README.md`.
- The API needs MySQL (port 18770) and Redis (15164). A sandbox without credit reaches only ports 80 and 443 outbound, so starting the API needs paid credit; the build/test job does not.
- `config.json` and `ca.pem` are uploaded to the sandbox from the working tree, never committed.

## Tests

- `test/init.go` connects to the real Redis from `config.json` in package `init` (and `log.Fatalf`s if unreachable), so every test in `./test` needs live Redis/network, even `TestCalculateDistance`.
- `TestGet`, `TestGetAll`, and `TestDelete` assume data already exists in `users:locations` (including `user-B`), and `TestDelete` removes `user-B`. They are stateful/order-dependent and mutate a shared remote Redis; a full-suite failure may be missing fixture data, not a code regression.
- `TestCalculateDistance`/`TestCalculateDistanceSimetris` cover the 1 km Haversine helper (`< 1` km, boundary excluded).
- `internal/usecase/trigger_sos_usecase_test.go` — unit tests for `TriggerSOSUseCase`; no live infra needed (fakes for Redis and MySQL). Run with `go test ./internal/usecase/ -run '^TestTriggerSOS' -v -count=1`.

## Code conventions

- Use cases own transactions: `tx := DB.WithContext(ctx).Begin()`, `defer tx.Rollback()`, then `tx.Commit()`. Repositories accept the `*gorm.DB`/`tx`; Redis repos use `context.Context`.
- Publish Kafka events only after commit and only when the producer is non-nil.
- Validate requests with `validate.Struct` and struct tags; return `fiber.Err*` errors so the global `ErrorHandler` renders `{"errors": ...}`.
- Generic CRUD lives in `repository.Repository[T]`; add entity-specific queries to the concrete repository.

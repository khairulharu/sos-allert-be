# Running SOS-Allert on Runtime Cloud

This folder moves the project off a local machine and onto
[Runtime Cloud](https://withruntime.com): a Linux microVM created by API, with
the Go API and the Svelte web app running inside it.

The project had no sandbox provider before this change, so there was no SDK to
swap. These scripts use Runtime's JavaScript SDK (`withruntime`) directly.

## Setup

Node 22.12 or later. Then, once:

```bash
cd backend/deploy/runtime
npm install
npx withruntime login --no-browser   # only if this machine is not connected
```

`RUNTIME_API_KEY` takes precedence over the saved connection. Use that on a
server or in CI; never commit it.

## Run the project's job

```bash
npm run verify
```

Creates a 2 vCPU / 4 GiB sandbox, uploads `backend/` and `frontend/`, installs
Go 1.25 and Node if needed, and checks:

- `go build ./...`
- `go vet ./...`
- `go test ./internal/usecase/ -count=1`
- `npm ci && npm run build`
- `frontend/dist/index.html` exists

It prints PASS/FAIL for each and deletes the sandbox afterwards. `--keep` keeps
it; `--full` also runs `go test ./test/`, which needs the live Redis.

## Run the app

```bash
npm run deploy
```

Builds both parts in the sandbox, starts the API on port 3000 and the web app
on port 4173, and prints a private preview URL for each. The links carry a
token (`urlWithToken`), so they work in a browser without extra headers.
Re-running rebuilds the same named sandbox.

```bash
npm run delete   # remove the sandbox for good
```

## Credit and network

A sandbox without credit can reach only ports 80 and 443 outbound, and its
previews are private. The API's `config.json` points at MySQL on port 18770 and
Redis on port 15164, so the API can only start once the account has credit.
The build-and-test job above needs no such ports.

`backend/config.json` and `backend/ca.pem` are gitignored and are uploaded to
the sandbox from the working tree. They are never committed. For production,
put those values in Runtime secrets or the sandbox environment instead.

## Files

- `lib.mjs` — staging, toolchain install, command helpers.
- `verify.mjs` — the project's build and test job.
- `deploy.mjs` — build, run and share the API and web app.

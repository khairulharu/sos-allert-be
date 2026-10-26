# SOS-Allert Backend

Go HTTP service untuk fitur SOS berbasis radius lokasi.

## Konsep

User klik tombol SOS → sistem cari semua user dalam radius 1 km → kirim notifikasi realtime → penerima bisa lihat lokasi pelapor.

---

## Status Implementasi

| Fitur | Status |
|---|---|
| `CalculateDistance` — Haversine 1 km helper | ✅ Done |
| `UpdateLocation` — simpan lokasi user ke Redis GEO | ✅ Done |
| `TriggerSOS` — save alert ke MySQL + GeoSearch 1 km | ✅ Done |
| Alert entity + repository (MySQL) | ✅ Done |
| `POST /api/sos` controller + route | ✅ Done |
| Migration tabel `alerts` | ✅ Done |
| AlertNotifier — log-only (sebelum FCM) | ✅ Done |
| Stale user filtering (`last_seen` < 2 menit) | ✅ Done |
| Hapus stubs `AllertUseCase` + `NearbyFinder` | ✅ Done |
| Notifikasi realtime (FCM/APNs) | ❌ Belum |
| Tampilan peta (Google Maps) | ❌ Belum |

---

## Endpoints

| Method | Path | Auth | Keterangan |
|---|---|---|---|
| `POST` | `/api/users` | ❌ | Register |
| `POST` | `/api/users/_login` | ❌ | Login, return raw token |
| `PUT` | `/api/location` | ✅ | Update lokasi user ke Redis GEO |
| `POST` | `/api/sos` | ✅ | Trigger SOS — simpan alert ke MySQL, return recipients 1 km |

> `Authorization` header = raw token dari login, bukan `Bearer <token>`.

---

## Alur TriggerSOS (`POST /api/sos`)

```
1. Get lokasi sender dari Redis GEO
2. INSERT entity.Alert ke MySQL (id, sender_id, longitude, latitude, triggered_at)
3. GeoSearch Redis radius 1000m
4. Filter sender dari hasil
5. Filter user stale (last_seen < 2 menit via Redis HGET `users:last_seen`)
6. AlertNotifier.Notify() — log recipients (FCM belakangan)
7. Return TriggerSOSResult { Alert, Recipients }
```

---

## Desain Penyimpanan Lokasi

- **Redis GEO** (`users:locations`) — lokasi aktif semua user, diupdate tiap `PUT /api/location`. Tidak ada TTL → difilter via `users:last_seen`.
- **Redis HASH** (`users:last_seen`) — timestamp last seen per user (ms), diset tiap `PUT /api/location`. User tidak aktif > 2 menit dibuang dari hasil GeoSearch.
- **MySQL** (`locations` table) — tidak dipakai untuk proximity, hanya ada sebagai tabel historis.
- **MySQL** (`alerts` table) — hanya ditulis saat user trigger SOS. Schema: `id, sender_id, longitude, latitude, triggered_at bigint`.

---

## Menjalankan Migration

```bash
migrate -path db/migrations -database "mysql://user:pass@host/dbname" up
```

Tabel `alerts` (migration `20261027000000`) harus ada sebelum `POST /api/sos` bisa dipakai.

---

## Menjalankan Service

```bash
go run cmd/web/main.go        # HTTP API on :3000
go run cmd/worker/main.go     # Kafka consumers
go build ./... && go vet ./...
```

`config.json` dan `ca.pem` wajib ada di root repo (gitignored). Web startup gagal kalau MySQL atau Redis tidak bisa dijangkau.

---

## Menjalankan Test

```bash
# Unit test TriggerSOS — tidak butuh live infra
go test ./internal/usecase/ -run '^TestTriggerSOS' -v -count=1

# Semua test di ./test/ — butuh Redis live dari config.json
go test ./test/ -v -count=1
```

> `./test/` bersifat stateful dan order-dependent (lihat AGENTS.md). `TestGet`, `TestGetAll`, `TestDelete` butuh data fixture di Redis.

---

## Tech Stack

- **Go** + Fiber
- **MySQL** (Aiven) — persistence
- **Redis** — GEO lokasi aktif
- **Kafka** — event user/contact/address (opsional, disabled by default)
- **Firebase** — notifikasi (belum diimplementasi)
- **Svelte** — frontend (repo terpisah)

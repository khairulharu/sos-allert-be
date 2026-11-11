# API changes — handoff (backend → frontend)

Catatan perubahan kontrak yang dikerjakan di backend. Mirror `../frontend/api/`
sudah disalin ulang; lihat "Status copy" di bawah.

## Alert history + summary

Ditambahkan:

| Method | Path | Ringkas |
|---|---|---|
| `GET` | `/api/sos/history` | Riwayat paged semua status, `role=sent\|received` |
| `GET` | `/api/sos/summary` | Agregat kartu: `sent`/`received`/`responded` |

### `GET /api/sos/history`

Query: `role` (`sent` default, `received`), `status` (kosong = semua,
`active`, `closed`, atau salah satu enum status), `page` (1), `size` (20, max
100), `from_ms`/`to_ms` (opsional, epoch ms, `to_ms >= from_ms`).

Response `{ "data": AlertDetailResponse[], "paging": PageMetadata }`, urut
`triggered_at DESC, id DESC`.

- Item **tanpa** `responders[]` dan `breadcrumbs`; `responders_en_route` tetap
  terisi.
- Field opsional **absen** bila kosong (`omitempty`), bukan `null`:
  `severity`, `summary`, `masked`, `resolved_at`, `cancelled_at`,
  `latest_location.updated_at`.
- Masking identik dengan `GET /api/sos/{alertId}`: `masked: true` + koordinat
  ~3 desimal untuk non-peserta; sender dan responder terdaftar dapat
  koordinat penuh.
- `role=received` berasal dari `alert_recipients` (penerima push fan-out) dan
  tidak pernah memuat insiden yang dikirim sendiri.
- `EXPIRED` tidak punya timestamp sendiri (pakai `triggered_at`).
- Error: `400` untuk param tidak valid (`from_ms`/`to_ms` non-numerik juga
  `400`; `page`/`size` non-numerik jatuh ke default), `401` tanpa token.

### `GET /api/sos/summary`

```json
{ "data": {
  "sent":      { "total": 12, "active": 1, "resolved": 8, "cancelled": 2, "expired": 1 },
  "received":  { "total": 6,  "active": 2, "resolved": 3, "cancelled": 1, "expired": 0 },
  "responded": { "total": 4,  "resolved": 3 }
} }
```

`active` = `TRIGGERED + ACKNOWLEDGED`; `total` = jumlah semua bucket;
`responded` = insiden berbeda yang dijawab pemanggil (dan berapa yang
`RESOLVED`). Semua nilai integer ≥ 0.

Catatan: selama job housekeeping `EXPIRED` belum ada, `expired` akan selalu 0
secara praktis.

## Migration

`db/migrations/20261110000000_alert_history_indexes.{up,down}.sql` menambah
`alerts (sender_id, triggered_at)` dan `alert_responses (responder_id,
alert_id)`. Jalankan `migrate up` sebelum memakai endpoint.

## Status copy

- `api/api.md` + `api/api-spec.json` diperbarui di backend.
- Sudah disalin ke `../frontend/api/` (hash sama per saat handoff ini).
- Baris "Last re-copy" di `../frontend/AGENTS.md` sudah diperbarui.

## Yang belum dikerjakan

- `GET /api/sos/nearby` (Fase 3) belum ada. Kartu ke-3 UI tetap memakai
  `received.active`, jangan berlabel "Nearby" dulu.
- Tidak ada perubahan pada `GET /api/sos/active`, `GET /api/sos/inbox`, atau
  `GET /api/sos/{alertId}`.

# SPEC — Alert history, summary, dan nearby alerts (BACKEND)

Status: **Fase 1–2 diimplementasi, Fase 3 (`/api/sos/nearby`) belum**
(sudah direkonsiliasi dengan kode backend per commit saat file ini ditulis)
Repo: `../backend` (Go/Fiber, module `golang-clean-architecture`)
Kontrak acuan: `api/api.md` + `api/api-spec.json` (sumber kebenaran ada di repo
ini; `frontend/api/` hanya mirror)
Spec sisi frontend: `../frontend/docs/SPEC-alerts-history.md` — jangan
menduplikasi TODO frontend di sini.

Dokumen ini adalah hasil audit ulang terhadap kode nyata. Bagian yang berbeda
dari draft lama ditandai **[berubah]**; alasannya ada di §1.1.

---

## 1. Masalah

`frontend/src/lib/components/AlertsView.svelte` butuh tiga hal yang tidak bisa
dilayani kontrak sekarang: **riwayat alert yang dikirim**, **alert yang sudah
selesai/`resolved`**, dan **angka kartu ringkasan**. Dua endpoint yang ada hanya
mengembalikan insiden **aktif**:

| Endpoint | Implementasi | Batasan |
|---|---|---|
| `GET /api/sos/active` | `AlertRepository.ListActiveBySender` (`internal/repository/alert_repository.go`) | `sender_id = ? AND status IN ('TRIGGERED','ACKNOWLEDGED')`, `LIMIT 20`, tanpa total |
| `GET /api/sos/inbox` | `AlertRepository.ListInbox` | join `alert_recipients` **dan** `status IN ('TRIGGERED','ACKNOWLEDGED')`, `LIMIT 20` (`ListInbox` di `internal/usecase/sos_lifecycle.go`) |

Konsekuensinya:

- Alert `RESOLVED`/`CANCELLED`/`EXPIRED` **tidak bisa di-query sama sekali**
  (per-alert `GET /api/sos/{alertId}` masih bisa, tapi frontend tidak punya
  daftar ID-nya).
- Tidak ada pagination → riwayat tidak mungkin ditampilkan.
- Tidak ada agregat → kartu "Alerts sent"/"Resolved" di UI selalu salah
  (resolved selalu 0 karena list sumbernya aktif-only).
- `GET /api/sos/inbox` adalah inbox push, bukan "alert di sekitar"; tidak ada
  endpoint untuk alert aktif di radius tertentu.

### 1.1 Koreksi terhadap draft lama **[berubah]**

| # | Draft lama | Kenyataan kode | Keputusan di spec ini |
|---|---|---|---|
| 1 | Migration menambah 3 index, termasuk `idx_alerts_active_triggered_at (status, triggered_at)` | `20261031000000_alerts_v2.up.sql` **sudah** punya `idx_alerts_status_triggered_at (status, triggered_at)` dan `idx_alerts_sender_status (sender_id, status)` | Hanya 2 index baru (§4). Index ketiga akan duplikat dan `ALTER TABLE` gagal. |
| 2 | `buildDetail` diberi parameter `includeResponders bool` (5 param, ubah 7 call site) | `buildDetail(ctx, alert, requesterID, includeBreadcrumbs)` dipakai di 7 tempat; `Responders`/`Breadcrumbs` sudah `omitempty` | `buildDetail` **tidak diubah**. Riwayat memakai builder batch terpisah (§5.4) yang berbagi helper mapping + masking. |
| 3 | "N+1 tersisa 2 query per alert" | `buildDetail` per item = 3–6 round trip (sender, `CountResponders`, `LatestBreadcrumb`, `IsResponder`, response+nama) → 60–120 query untuk `size=20`, sampai 600 untuk `size=100` | Riwayat memakai 4 query per halaman (batch), bukan per item. |
| 4 | `ExpireOlderThan` "membuang parameter `expiredAtMs`" | Signature sudah `ExpireOlderThan(ctx, db, beforeMs, expiredAtMs)`; parameter tetap dibuang dan `resolved_at`/`cancelled_at` di-`NULL`-kan | Benar, tidak ada kolom `expired_at`; `EXPIRED` tidak punya timestamp sendiri (§8.4). |
| 5 | `EXPIRED` bisa muncul lewat housekeeping | `TriggerSOSUseCase.ExpireOldAlerts` dan `PurgeOldBreadcrumbs` **tidak dipanggil siapa pun** (tidak ada ticker di `cmd/web` maupun `cmd/worker`; worker tidak punya DB) | `EXPIRED` **belum akan pernah ada** sampai job housekeeping di-wire. Spec ini tidak mewajibkannya, tapi `status=closed` sudah menangani `EXPIRED` supaya tidak perlu diubah nanti (§8.1). |
| 6 | `masked`/`resolved_at`/`cancelled_at` bernilai `null` di JSON | Semua bertag `omitempty` di `model.AlertDetailResponse` | Field **absen** (bukan `null`) bila kosong. Contoh JSON di §3 sudah disesuaikan; frontend sudah memakai `a.masked` truthy dan fallback `triggered_at`. |
| 7 | `page=abc` → `400` | `ctx.QueryInt` mengembalikan default saat parse gagal (pola yang sama di `ContactController.List`) | Angka non-numerik → pakai default; `400` hanya untuk nilai yang berhasil diparse tapi di luar rentang, atau `from_ms`/`to_ms` non-numerik (diparse manual). |
| 8 | `api/api.md` + `api/api-spec.json` dicopy ke `../frontend/api/` | `frontend/AGENTS.md` memang mewajibkan re-copy ("Last re-copy: …") | Copy tetap dikerjakan sebagai langkah eksplisit + update baris "Last re-copy" (§5.9). Selain itu catat handoff di `docs/api-changes.md`. |
| 9 | Repository SQL ikut teruji `go test ./...` | Tidak ada harness DB (fake in-memory hanya di usecase/HTTP). `go test ./internal/...` tidak menyentuh MySQL | §5.8 memisahkan: logika (filter/paging/masking/agregasi) diuji lewat fake; SQL diverifikasi manual §7 (opsional: test live di `test/`). |

---

## 2. Definisi (dipakai backend, frontend, dan copy)

| Istilah | Definisi |
|---|---|
| **Sent** | `alerts.sender_id = me`, semua status. |
| **Received** | `alert_recipients.user_id = me` (pernah dinotifikasi), semua status. |
| **Responded / helped** | ada baris `alert_responses` dengan `responder_id = me`. |
| **Active** | `status IN ('TRIGGERED','ACKNOWLEDGED')` — sama dengan `entity.Alert.IsActive()`. |
| **Closed** | `status IN ('RESOLVED','CANCELLED','EXPIRED')`. |
| **Nearby** | alert **aktif** yang koordinat trigger-nya dalam `radius_m` dari heartbeat pemanggil, bukan milik pemanggil. |

Catatan semantik:

- `received` = penerima fan-out (`CreateRecipients` di
  `ExecuteWithRequest`). User yang menemukan alert lewat "nearby" tapi tidak
  pernah masuk fan-out **tidak** muncul di `role=received`.
- Sender tidak pernah masuk `alert_recipients` (`findRecipients` membuang
  `senderID`), jadi `role=received` tidak memuat alert sendiri.
- Satu user bisa punya >1 baris `alert_responses` untuk alert yang sama:
  `alert_responses` PK-nya `id` (UUID), tidak ada unique `(alert_id,
  responder_id)`, dan `Respond` tidak menolak duplikat (`ErrDuplicateResponse`
  ada tapi tidak dipakai). Semua hitungan "responded" wajib
  `COUNT(DISTINCT alert_id)`; `responders_en_route` sudah memakai
  `COUNT(DISTINCT responder_id)`.

Frontend memakai: `sent.total`, `responded.resolved`, `received.active`
(kartu ke-3 hanya diganti ke `nearby.count` kalau Fase 3 dikerjakan).

---

## 3. Kontrak API (authoritative)

Dua endpoint wajib (Fase 1–2), satu opsional (Fase 3). Semua di bawah
`AuthMiddleware` (raw token, tanpa `Bearer `).

### 3.1 `GET /api/sos/history` — riwayat paged (Fase 1)

Query:

| Param | Tipe | Default | Validasi | Catatan |
|---|---|---|---|---|
| `role` | string | `sent` | `sent` \| `received` | menentukan query. |
| `status` | string | *(kosong = semua)* | `TRIGGERED` \| `ACKNOWLEDGED` \| `RESOLVED` \| `CANCELLED` \| `EXPIRED` \| `active` \| `closed` | case-sensitive; `active`/`closed` = sugar grup status. |
| `page` | int | `1` | `min=1` | non-numerik → default. |
| `size` | int | `20` | `min=1,max=100` | non-numerik → default. |
| `from_ms` | int64 | — | `>0` | opsional, `triggered_at >=`; non-numerik → `400`. |
| `to_ms` | int64 | — | `>0` dan `>= from_ms` | opsional, `triggered_at <=`; non-numerik → `400`. |

Response `200`:

```json
{
  "data": [
    {
      "alert_id": "alt_...",
      "sender": { "id": "user-a", "name": "Budi" },
      "status": "RESOLVED",
      "emergency_type": "MEDICAL",
      "severity": 0.8,
      "summary": "PRIORITAS TINGGI: Kemungkinan darurat medis",
      "initial_location": { "latitude": -6.209, "longitude": 106.846 },
      "latest_location": { "latitude": -6.209, "longitude": 106.846, "updated_at": 1730304050000 },
      "responders_en_route": 3,
      "triggered_at": 1730304000000,
      "resolved_at": 1730304075000
    }
  ],
  "paging": { "page": 1, "size": 20, "total_item": 42, "total_page": 3 }
}
```

Aturan:

- Item memakai `model.AlertDetailResponse` yang sudah ada (`internal/model/sos_model.go`),
  **tanpa** `responders[]` dan `breadcrumbs`. `responders_en_route` tetap diisi
  (batch `COUNT(DISTINCT responder_id)`).
- Field opsional absen bila kosong (semua `omitempty`): `severity` (0),
  `summary`, `masked` (false), `resolved_at`, `cancelled_at`, `updated_at`
  (bila belum ada breadcrumb). `initial_location`/`latest_location` **selalu
  ada** (struct non-pointer) dan tidak boleh berisi `0,0` — lihat §5.4.
- `masked` memakai aturan yang sudah berlaku di `buildDetail`: `true` +
  koordinat dibulatkan ~3 desimal bila pemanggil bukan sender dan bukan
  responder terdaftar. **Jangan** membuat definisi masking kedua: kedua builder
  memakai helper yang sama (§5.4).
- `role=received` tidak menyertakan alert yang dikirim sendiri. Gunakan
  `DISTINCT` per `alert_id` untuk berjaga-jaga.
- Urutan: `triggered_at DESC, id DESC` (tie-break supaya paging stabil).
- `EXPIRED` tidak punya timestamp sendiri; `resolved_at`/`cancelled_at` absen.
- `400` untuk `role`/`status` tak dikenal, `page < 1`, `size` di luar 1–100,
  `from_ms`/`to_ms` ≤ 0, `to_ms < from_ms`, atau nilai `from_ms`/`to_ms`
  non-numerik. `401` tanpa token. `500` untuk error DB.
- Error memakai envelope global: `{"errors":"Bad Request"}`.

Contoh:

```
GET /api/sos/history?role=sent&status=closed&page=1&size=20
GET /api/sos/history?role=received&page=2&size=20
GET /api/sos/history?role=sent&from_ms=1730304000000&to_ms=1730390400000
```

### 3.2 `GET /api/sos/summary` — agregat kartu (Fase 2)

Auth: raw token. Response `200`:

```json
{
  "data": {
    "sent":      { "total": 12, "active": 1, "resolved": 8, "cancelled": 2, "expired": 1 },
    "received":  { "total": 6,  "active": 2, "resolved": 3, "cancelled": 1, "expired": 0 },
    "responded": { "total": 4,  "resolved": 3 }
  }
}
```

Aturan:

- `sent.*` = hitung `alerts` dengan `sender_id = me`.
- `received.*` = hitung join `alert_recipients` untuk `user_id = me`, **semua**
  status.
- `sent.active`/`received.active` = `TRIGGERED + ACKNOWLEDGED`.
- `*` per status: `resolved`, `cancelled`, `expired` = hitungan status itu.
- `total` = jumlah seluruh bucket (termasuk status tak terduga, supaya tidak
  ada baris yang hilang dari total).
- `responded.total` = `COUNT(DISTINCT alert_id)` dari `alert_responses` dengan
  `responder_id = me`; `responded.resolved` = subset dengan
  `alerts.status = 'RESOLVED'` (juga `DISTINCT`).
- Semua nilai integer ≥ 0; `data` selalu ada (nol, bukan `null`).
- Tujuan: kartu tidak butuh 3 request `history?size=1`.
- Tidak ada rate limit khusus; biaya = 3 query agregat (§5.2).

### 3.3 `GET /api/sos/nearby` — alert aktif di sekitar (Fase 3, opsional)

Dikerjakan hanya kalau UI mempertahankan label "Nearby alerts" secara literal.

Auth: raw token. Butuh heartbeat pemanggil (Redis `users:locations`), aturan
reciprocity sama dengan `GET /api/location/nearby`.

Query: `radius_m` int, default `1000`, `min=100`, `max=5000`; non-numerik →
default; `404` bila pemanggil belum pernah heartbeat (heartbeat diambil lewat
`u.Deps.Location.Get`; `model.ErrUserLocationNotFound`/nil → `fiber.ErrNotFound`).

Response `200`:

```json
{
  "data": {
    "radius_m": 1000,
    "count": 2,
    "masked": true,
    "alerts": [
      {
        "alert_id": "alt_...",
        "sender": { "id": "user-a", "name": "Budi" },
        "status": "TRIGGERED",
        "emergency_type": "DANGER",
        "severity": 0.6,
        "summary": "PRIORITAS SEDANG: …",
        "initial_location": { "latitude": -6.21, "longitude": 106.848 },
        "latest_location": { "latitude": -6.21, "longitude": 106.848 },
        "responders_en_route": 1,
        "triggered_at": 1730304000000,
        "distance_m": 262,
        "masked": true
      }
    ]
  }
}
```

Aturan:

- Hanya `status IN ('TRIGGERED','ACKNOWLEDGED')`, exclude `sender_id = me`.
- Posisi acuan: `alerts.latitude/longitude` (koordinat trigger), bukan
  `alert_breadcrumbs` (hindari MAX() per alert).
- **Semua** item dimask ~3 desimal (`masked: true` di item dan di root),
  termasuk untuk responder terdaftar: ini surface discovery, bukan detail.
  Buka `GET /api/sos/{alertId}` untuk koordinat presisi (participant-only).
  Ini pengecualian yang disengaja, bukan definisi masking kedua.
- Maks 50, urut `distance_m ASC`.
- Implementasi v1: bounding box di SQL + haversine di Go
  (`usecase.CalculateDistanceMeters`, `internal/usecase/haversine_calculate.go`;
  sudah dipakai `Respond`). Jumlah alert aktif kecil; Redis GEO hanya kalau
  nanti jadi besar.
- Ini **bukan** radius fan-out: fan-out memakai tier adaptif 1/2/5 km
  (`internal/config/notification.go`), jadi alert bisa tampil di sini walau
  pemanggil bukan penerima push-nya.

---

## 4. Skema DB & migration **[berubah]**

Tidak perlu kolom baru untuk Fase 1–2.

Index yang **sudah ada** (jangan diduplikasi):

- `alerts`: `idx_alerts_status_triggered_at (status, triggered_at)`,
  `idx_alerts_sender_status (sender_id, status)` — dari
  `20261031000000_alerts_v2.up.sql`.
- `alert_recipients`: PK `(alert_id, user_id)` +
  `idx_alert_recipients_user_time (user_id, notified_at)`.
- `alert_responses`: `idx_responses_alert_id (alert_id)`,
  `idx_responses_alert_responder (alert_id, responder_id)` — leading column
  `alert_id`, tidak cocok untuk "helped per responder".
- `alert_breadcrumbs`: `idx_breadcrumbs_alert_time (alert_id, recorded_at)`
  — sudah pas untuk batch `MAX(recorded_at)` per alert.

Migration baru `db/migrations/20261110000000_alert_history_indexes.up.sql`:

```sql
-- Riwayat per sender, urut waktu (index lama hanya (sender_id, status)).
ALTER TABLE alerts
    ADD INDEX idx_alerts_sender_triggered_at (sender_id, triggered_at);

-- Hitung "helped" per responder (predikat tanpa alert_id).
ALTER TABLE alert_responses
    ADD INDEX idx_alert_responses_responder (responder_id, alert_id);
```

`.down.sql`:

```sql
ALTER TABLE alerts DROP INDEX idx_alerts_sender_triggered_at;
ALTER TABLE alert_responses DROP INDEX idx_alert_responses_responder;
```

Catatan:

- `role=received` melakukan join `alert_recipients (user_id)` → `alerts` PK,
  lalu sort `alerts.triggered_at DESC`. Sort tetap filesort, tapi hanya atas
  baris milik user tersebut (jumlah kecil) — tidak perlu index tambahan.
- Fase 3 (kalau dikerjakan) menambah satu index sendiri di migration terpisah,
  karena query-nya bounding box: `idx_alerts_status_position (status, latitude,
  longitude)`. Jangan dimasukkan sekarang.
- Migration dijalankan dengan CLI `migrate` terhadap `db/migrations` (DSN di
  `Archi.Md`, file itu gitignored). Jalankan `up` sebelum memakai endpoint.

---

## 5. TODO backend

Setiap langkah bisa di-commit sendiri.

### 5.1 Model (`internal/model/sos_model.go`)

- [x] `SearchAlertHistoryRequest` (field `json:"-"`, diisi controller):

```go
type SearchAlertHistoryRequest struct {
	UserID string `json:"-" validate:"required"`
	Role   string `json:"-" validate:"required,oneof=sent received"`
	Status string `json:"-" validate:"omitempty,oneof=TRIGGERED ACKNOWLEDGED RESOLVED CANCELLED EXPIRED active closed"`
	Page   int    `json:"-" validate:"min=1"`
	Size   int    `json:"-" validate:"min=1,max=100"`
	FromMs *int64 `json:"-" validate:"omitempty,gt=0"`
	ToMs   *int64 `json:"-" validate:"omitempty,gt=0"`
}
```

  Validasi `to_ms >= from_ms` manual di usecase (`gtefield` rewel untuk pointer).

- [x] Agregat summary:

```go
type AlertStatusCounts struct {
	Total     int64 `json:"total"`
	Active    int64 `json:"active"`
	Resolved  int64 `json:"resolved"`
	Cancelled int64 `json:"cancelled"`
	Expired   int64 `json:"expired"`
}

type AlertRespondedCounts struct {
	Total    int64 `json:"total"`
	Resolved int64 `json:"resolved"`
}

type AlertSummaryResponse struct {
	Sent      AlertStatusCounts    `json:"sent"`
	Received  AlertStatusCounts    `json:"received"`
	Responded AlertRespondedCounts `json:"responded"`
}
```

- [ ] (Fase 3) `AlertNearbyItem` di `internal/model/location_model.go` atau
      `sos_model.go`:

```go
type AlertNearbyItem struct {
	AlertDetailResponse
	DistanceM int `json:"distance_m"`
}

type NearbyAlertsResponse struct {
	RadiusM float64           `json:"radius_m"`
	Count   int               `json:"count"`
	Masked  bool              `json:"masked"`
	Alerts  []*AlertNearbyItem `json:"alerts"`
}
```

  Embedding `AlertDetailResponse` sudah membawa `masked` per item.

### 5.2 Repository (`internal/repository/alert_repository.go`) **[berubah]**

Semua method ikut konvensi `AlertRepository`: `ctx` lalu `db *gorm.DB`.

- [x] Helper grup status + filter bersama, jangan sebar string SQL:

```go
// alertStatusesFor maps the API filter to statuses. Empty -> nil (all).
func alertStatusesFor(filter string) []string {
	switch filter {
	case "":
		return nil
	case "active":
		return []string{entity.AlertStatusTriggered, entity.AlertStatusAcknowledged}
	case "closed":
		return []string{entity.AlertStatusResolved, entity.AlertStatusCancelled, entity.AlertStatusExpired}
	default:
		return []string{filter}
	}
}

func applyAlertHistoryFilter(tx *gorm.DB, req *model.SearchAlertHistoryRequest) *gorm.DB {
	if statuses := alertStatusesFor(req.Status); len(statuses) > 0 {
		tx = tx.Where("alerts.status IN ?", statuses)
	}
	if req.FromMs != nil {
		tx = tx.Where("alerts.triggered_at >= ?", *req.FromMs)
	}
	if req.ToMs != nil {
		tx = tx.Where("alerts.triggered_at <= ?", *req.ToMs)
	}
	return tx
}
```

- [x] `ListHistoryBySender(ctx, db, req) ([]*entity.Alert, int64, error)`:

```go
var out []*entity.Alert
q := applyAlertHistoryFilter(
	db.WithContext(ctx).Model(&entity.Alert{}).Where("alerts.sender_id = ?", req.UserID), req)
if err := q.Order("alerts.triggered_at DESC, alerts.id DESC").
	Offset((req.Page - 1) * req.Size).Limit(req.Size).Find(&out).Error; err != nil { ... }

var total int64
c := applyAlertHistoryFilter(
	db.WithContext(ctx).Model(&entity.Alert{}).Where("alerts.sender_id = ?", req.UserID), req)
if err := c.Count(&total).Error; err != nil { ... }
```

- [x] `ListHistoryForRecipient(ctx, db, req) ([]*entity.Alert, int64, error)` —
      join + filter, `SELECT alerts.*`, urutan sama; count memakai chain baru
      **tanpa** `Select`:

```go
join := func() *gorm.DB {
	return db.WithContext(ctx).Model(&entity.Alert{}).
		Joins("JOIN alert_recipients r ON r.alert_id = alerts.id").
		Where("r.user_id = ?", req.UserID)
}
applyAlertHistoryFilter(join(), req).
	Select("alerts.*").Order("alerts.triggered_at DESC, alerts.id DESC").
	Offset((req.Page - 1) * req.Size).Limit(req.Size).Find(&out)
applyAlertHistoryFilter(join(), req).Count(&total)
```

- [x] `CountBySenderStatus(ctx, db, senderID) (map[string]int64, error)`:

```sql
SELECT status, COUNT(*) AS total FROM alerts WHERE sender_id = ? GROUP BY status
```

- [x] `CountByRecipientStatus(ctx, db, userID) (map[string]int64, error)` — join
      yang sama, `GROUP BY alerts.status`.

- [x] `CountResponded(ctx, db, userID) (total, resolved int64, err error)`:

```go
db.WithContext(ctx).Model(&entity.AlertResponse{}).
	Where("responder_id = ?", userID).
	Select("COUNT(DISTINCT alert_id)").Scan(&total)

db.WithContext(ctx).Table("alert_responses r").
	Joins("JOIN alerts a ON a.id = r.alert_id").
	Where("r.responder_id = ? AND a.status = ?", userID, entity.AlertStatusResolved).
	Select("COUNT(DISTINCT r.alert_id)").Scan(&resolved)
```

  Jangan pakai `Distinct("r.alert_id").Count(...)` untuk query join: GORM hanya
  menerjemahkannya ke `COUNT(DISTINCT(?))` bila `Selects[0]` berupa satu nama
  kolom polos (`alert_id`). Dengan prefix tabel (`r.alert_id`) ia jatuh ke
  `COUNT(*)` dan hasilnya salah.

- [x] `CountRespondersByAlertIDs(ctx, db, alertIDs) (map[string]int, error)`:

```sql
SELECT alert_id, COUNT(DISTINCT responder_id) AS total
FROM alert_responses WHERE alert_id IN (?) GROUP BY alert_id
```

- [x] `LatestBreadcrumbsByAlertIDs(ctx, db, alertIDs) (map[string]*entity.AlertBreadcrumb, error)`
      — pakai `Raw` (derived table, kompatibel MySQL 5.7+/8.x; ekspansi slice
      `IN ?` di `Raw` paling dapat diprediksi):

```go
var rows []*entity.AlertBreadcrumb
db.WithContext(ctx).Raw(`
	SELECT b.* FROM alert_breadcrumbs b
	JOIN (SELECT alert_id, MAX(recorded_at) AS max_recorded
	      FROM alert_breadcrumbs
	      WHERE alert_id IN ?
	      GROUP BY alert_id) m
	  ON m.alert_id = b.alert_id AND m.max_recorded = b.recorded_at
	WHERE b.alert_id IN ?`, alertIDs, alertIDs).Scan(&rows)
```

  Kalau ada `recorded_at` kembar, simpan yang pertama (dedupe di Go). Tanpa
  dedupe, map akan menimpa sembarang — hasilnya tetap benar secara isi karena
  `recorded_at` sama, tapi jangan bergantung pada urutan.

- [x] `ListRespondedAlertIDs(ctx, db, userID, alertIDs) (map[string]bool, error)`:

```sql
SELECT DISTINCT alert_id FROM alert_responses
WHERE responder_id = ? AND alert_id IN (?)
```

- [ ] (Fase 3) `ListActiveNearby(ctx, db, lat, lng, radiusM float64, excludeUserID string, limit int) ([]*entity.Alert, error)`
      — bounding box `alerts.latitude BETWEEN ? AND ?`, `alerts.longitude
      BETWEEN ? AND ?`, `status IN (aktif)`, `sender_id <> ?`,
      `ORDER BY alerts.triggered_at DESC`, `LIMIT limit`. Delta derajat:
      `radiusM / 111320` untuk latitude; untuk longitude bagi
      `111320 * cos(lat)`. Refinement haversine di usecase.

### 5.3 Interface `AlertStore` (`internal/usecase/trigger_sos_usecase.go`)

- [x] Tambahkan ke interface (setelah `ListInbox`), lalu lengkapi **semua**
      implementasi fake:

```go
ListHistoryBySender(ctx context.Context, db *gorm.DB, req *model.SearchAlertHistoryRequest) ([]*entity.Alert, int64, error)
ListHistoryForRecipient(ctx context.Context, db *gorm.DB, req *model.SearchAlertHistoryRequest) ([]*entity.Alert, int64, error)
CountBySenderStatus(ctx context.Context, db *gorm.DB, senderID string) (map[string]int64, error)
CountByRecipientStatus(ctx context.Context, db *gorm.DB, userID string) (map[string]int64, error)
CountResponded(ctx context.Context, db *gorm.DB, userID string) (total, resolved int64, err error)
CountRespondersByAlertIDs(ctx context.Context, db *gorm.DB, alertIDs []string) (map[string]int, error)
LatestBreadcrumbsByAlertIDs(ctx context.Context, db *gorm.DB, alertIDs []string) (map[string]*entity.AlertBreadcrumb, error)
ListRespondedAlertIDs(ctx context.Context, db *gorm.DB, userID string, alertIDs []string) (map[string]bool, error)
```

  Fake yang wajib dilengkapi (hanya dua; sudah dicek dengan
  `grep -rn "PurgeBreadcrumbs(ctx context.Context"`):
  - `internal/delivery/http/sos_api_test.go` → `memAlerts` (`:183` dkk)
  - `internal/usecase/trigger_sos_usecase_test.go` → `fakeAlertRepository`

  Implementasi fake cukup in-memory: sort `triggered_at DESC, id DESC`, filter
  status/waktu/role, dan potong `page/size` (supaya test paging nyata).

### 5.4 Usecase (`internal/usecase/sos_history.go` baru) **[berubah]**

File baru, bukan menambah `sos_lifecycle.go` yang sudah ~860 baris.

- [x] `ListHistory(ctx, request *model.SearchAlertHistoryRequest) ([]*model.AlertDetailResponse, int64, error)`:

```go
if err := u.validate(request); err != nil { return nil, 0, err }        // 400
if request.FromMs != nil && request.ToMs != nil && *request.ToMs < *request.FromMs {
	return nil, 0, fiber.ErrBadRequest
}
if request.Role == "received" {
	alerts, total, err = u.Deps.Alerts.ListHistoryForRecipient(ctx, u.DB, request)
} else {
	alerts, total, err = u.Deps.Alerts.ListHistoryBySender(ctx, u.DB, request)
}
...
items, err := u.buildHistoryDetails(ctx, alerts, request.UserID, request.Role)
return items, total, err
```

- [x] `buildHistoryDetails(ctx, alerts []*entity.Alert, requesterID, role string) ([]*model.AlertDetailResponse, error)`
      — **4 query per halaman**, bukan per item:

  1. `CountRespondersByAlertIDs(ctx, ids)` → `responders_en_route`;
  2. `LatestBreadcrumbsByAlertIDs(ctx, ids)` → `latest_location`
     (fallback `initial_location` bila belum ada breadcrumb);
  3. `ListRespondedAlertIDs(ctx, requesterID, ids)` **hanya** bila
     `role != "sent"` → penentu masking;
  4. `u.Deps.Users.ListByIDs(ctx, senderIDs)` → `sender.name` (nil-safe/fail-open
     seperti `fillResponders`: error → warn, nama kosong).

  Lalu per item: `alertDetailBase(alert)` + nama sender dari map + masking
  helper. Tidak memanggil `buildDetail`, tidak memanggil
  `ListResponses`/`ListBreadcrumbs`:

```go
nameByID := ... // hasil ListByIDs (boleh kosong)
isParticipant := alert.SenderID == requesterID || responded[alert.ID]
...
```

- [x] Ekstrak dua helper dipakai **bersama** `buildDetail` (supaya tidak ada
      definisi kedua):

```go
// alertDetailBase maps an entity to the detail fields shared by every
// response (sender id, status, triage, timestamps). Locations are set
// afterwards because they depend on the viewer.
func alertDetailBase(alert *entity.Alert) *model.AlertDetailResponse

// setDetailLocations applies the existing masking rule: exact
// coordinates for the sender and recorded responders, ~3 decimals
// (model.MaskCoordinates) for everyone else.
func setDetailLocations(detail *model.AlertDetailResponse, initial, latest model.LocationPoint, isParticipant bool)
```

  `buildDetail` di `sos_lifecycle.go` diubah **hanya** untuk memanggil kedua
  helper itu (isi & perilaku identik). Semua call site yang ada (mis.
  `sos_lifecycle.go:49,60,78,275,291,318` + `GetAlert`) tetap memakai
  signature 4 parameter yang sama.

- [x] `Summary(ctx, userID string) (*model.AlertSummaryResponse, error)`
      (+ helper package-level `statusCounts`, bukan closure di dalam method):

```go
sent, err := u.Deps.Alerts.CountBySenderStatus(ctx, u.DB, userID)
// ... early return on err
recv, err := u.Deps.Alerts.CountByRecipientStatus(ctx, u.DB, userID)
// ... early return on err
respondedTotal, respondedResolved, err := u.Deps.Alerts.CountResponded(ctx, u.DB, userID)
// ... early return on err
return &model.AlertSummaryResponse{
	Sent:      statusCounts(sent),
	Received:  statusCounts(recv),
	Responded: model.AlertRespondedCounts{Total: respondedTotal, Resolved: respondedResolved},
}, nil

func statusCounts(m map[string]int64) model.AlertStatusCounts {
	c := model.AlertStatusCounts{
		Active:    m[entity.AlertStatusTriggered] + m[entity.AlertStatusAcknowledged],
		Resolved:  m[entity.AlertStatusResolved],
		Cancelled: m[entity.AlertStatusCancelled],
		Expired:   m[entity.AlertStatusExpired],
	}
	for _, v := range m {
		c.Total += v
	}
	return c
}
```

- [ ] (Fase 3) `ListNearby(ctx, userID string, radiusM float64) (*model.NearbyAlertsResponse, error)`
      — validasi radius (`100..5000`, default 1000), `u.Deps.Location.Get` →
      `fiber.ErrNotFound` bila belum heartbeat, `ListActiveNearby` dengan
      `limit 50` (bounding box + margin), hitung `CalculateDistanceMeters`,
      buang `distance > radiusM`, sort ASC, potong 50, isi
      `responders_en_route` lewat `CountRespondersByAlertIDs`, mask semua item
      lewat `setDetailLocations(..., false)`.

### 5.5 Controller (`internal/delivery/http/sos_controller.go`)

- [x] `ListHistory(ctx *fiber.Ctx) error` — pola sama dengan
      `LocationController.ListNearby`/`ContactController.List`:

```go
auth := middleware.GetUser(ctx)
request := &model.SearchAlertHistoryRequest{
	UserID: auth.ID,
	Role:   ctx.Query("role", "sent"),
	Status: ctx.Query("status", ""),
	Page:   ctx.QueryInt("page", 1),
	Size:   ctx.QueryInt("size", 20),
}
if raw := ctx.Query("from_ms"); raw != "" {
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil { return fiber.ErrBadRequest }
	request.FromMs = &v
}
// idem to_ms
responses, total, err := s.UseCase.ListHistory(ctx.UserContext(), request)
if err != nil { s.Log.WithError(err).Error("error listing alert history"); return mapSOSError(err) }

paging := &model.PageMetadata{
	Page:      request.Page,
	Size:      request.Size,
	TotalItem: total,
	TotalPage: int64(math.Ceil(float64(total) / float64(request.Size))),
}
return ctx.JSON(model.WebResponse[[]*model.AlertDetailResponse]{Data: responses, Paging: paging})
```

  Tambahkan import `math` (import `strconv` sudah ada).

- [x] `Summary(ctx *fiber.Ctx) error` —
      `ctx.JSON(model.WebResponse[*model.AlertSummaryResponse]{Data: result})`.

- [ ] (Fase 3) `ListNearby(ctx *fiber.Ctx) error` — `ctx.QueryInt("radius_m", 1000)`,
      `404` dari usecase diteruskan (`mapSOSError` sudah tidak perlu diubah
      karena `fiber.ErrNotFound` lolos lewat default branch).

- [x] `mapSOSError` tidak perlu diubah.

### 5.6 Route (`internal/delivery/http/route/route.go`)

- [x] Daftarkan **sebelum** `/api/sos/:alertId` (Fiber mencocokkan sesuai
      urutan registrasi; `/api/sos/inbox` dan `/api/sos/active` sudah jadi
      preseden):

```go
c.App.Get("/api/sos/inbox", c.SOSController.ListInbox)
c.App.Get("/api/sos/active", c.SOSController.ListActive)
c.App.Get("/api/sos/history", c.SOSController.ListHistory)   // baru
c.App.Get("/api/sos/summary", c.SOSController.Summary)       // baru
c.App.Get("/api/sos/nearby", c.SOSController.ListNearby)     // Fase 3
c.App.Get("/api/sos/:alertId", c.SOSController.Get)
```

### 5.7 Migration

- [x] `db/migrations/20261110000000_alert_history_indexes.up.sql` + `.down.sql`
      (§4, hanya 2 index).

### 5.8 Test

- [x] `internal/usecase/sos_history_test.go` (fake in-memory, tanpa MySQL):
  - sent history memuat alert `RESOLVED` (regression: "resolved selalu 0");
  - `status=closed` → RESOLVED + CANCELLED + EXPIRED, `active` → TRIGGERED +
    ACKNOWLEDGED;
  - `role=received` hanya alert di `alert_recipients`, tidak memuat alert sendiri;
  - `paging.total_item` benar, halaman 2 tanpa duplikat/kehilangan (fixture
    dengan `triggered_at` sama untuk menguji tie-break `id DESC`);
  - masking: non-peserta `masked: true` + koordinat ≠ asli, sender `masked`
    absen/false, penerima yang pernah `Respond` **tidak** dimask;
  - item tidak memuat `responders`/`breadcrumbs`;
  - batch: fake mencatat jumlah panggilan → `CountRespondersByAlertIDs` dan
    `LatestBreadcrumbsByAlertIDs` dipanggil **sekali** untuk satu halaman
    (regression guard N+1);
  - summary: `sent`/`received` per status + `responded.resolved` hanya alert
    yang benar; `total` = jumlah semua bucket.
- [x] `internal/delivery/http/sos_api_test.go`:
  - `200` + `paging` (`page`, `size`, `total_item`, `total_page`);
  - `400` untuk `role=foo`, `status=foo`, `size=500`, `page=0`,
    `from_ms=abc`, `to_ms<from_ms`;
  - `401` tanpa token — middleware di test ini adalah stub; tambahkan varian
    yang mengembalikan `fiber.ErrUnauthorized` saat header kosong;
  - `/api/sos/history` **tidak** tertangkap route `:alertId` (response list,
    bukan 404 "alert not found");
  - `memAlerts` dilengkapi 8 method baru §5.3.
- [x] Repository SQL **tidak** tercakup `go test ./internal/...` (tidak ada
      harness DB). Verifikasi lewat §7. Opsional: smoke test live di `test/`
      (pola package itu sudah butuh infra live), di-skip bila `config.json` /
      MySQL tidak tersedia.
- [x] `go build ./... && go vet ./... && go test ./internal/... -count=1` hijau.

### 5.9 Dokumentasi kontrak (wajib)

- [x] `api/api.md`:
  - tambahkan `GET /api/sos/history`, `GET /api/sos/summary` (+ `nearby` bila
    Fase 3) ke tabel **SOS Incident**;
  - contoh JSON + catatan masking/paging/`omitempty`;
  - ubah bagian "List dengan pagination (**hanya** `GET /api/contacts`)" agar
    menyebut `GET /api/sos/history`;
  - tambahkan migration `20261110000000_alert_history_indexes*` ke daftar
    migration di "Catatan implementasi".
- [x] `api/api-spec.json`: path + schema (`SearchAlertHistory` sebagai query
      params, `AlertSummaryResponse`, `AlertStatusCounts`,
      `AlertRespondedCounts`, `NearbyAlertsResponse`); validasi JSON
      (`python3 -m json.tool api/api-spec.json` atau `jq empty`).
- [x] Copy ulang `api/api.md` + `api/api-spec.json` ke `../frontend/api/`, lalu
      update baris "Last re-copy: …" di `../frontend/AGENTS.md` (sebut endpoint
      baru + commit hash). Frontend tidak boleh memakai endpoint ini sebelum
      copy itu ada.
- [x] Tulis handoff singkat di `docs/api-changes.md` (dibuat bila belum ada):
      endpoint baru, bentuk response, aturan masking, dan status copy.

---

## 6. Acceptance criteria

1. `GET /api/sos/history?role=sent` mengembalikan alert yang sudah `RESOLVED`
   (termasuk `resolved_at`), bukan hanya yang aktif.
2. `status=closed` = RESOLVED + CANCELLED + EXPIRED; `status=active` =
   TRIGGERED + ACKNOWLEDGED.
3. `role=received` mengembalikan alert dari `alert_recipients`, semua status,
   dan **tidak** mengembalikan alert yang dikirim sendiri.
4. Paging konsisten: 25 baris, `size=20` → 20 + 5 tanpa duplikat/kehilangan;
   `total_page` = ceil(total/size); tie `triggered_at` dipecah `id DESC`.
5. Masking: `masked: true` + koordinat 3 desimal untuk non-peserta; sender dan
   responder terdaftar mendapat koordinat penuh; item tidak memuat
   `responders`/`breadcrumbs`.
6. Satu halaman riwayat memakai ≤ 4 query batch (+1 query list +1 count),
   tidak tumbuh per item (`size=100` = jumlah query sama dengan `size=20`).
7. `GET /api/sos/summary` cocok dengan hitung manual dari `history` untuk semua
   peran/status; `responded.resolved` hanya alert yang benar-benar dijawab
   pemanggil; nilai nol bukan `null`.
8. `400` untuk `role`/`status`/`size`/`page`/rentang waktu tidak valid; `401`
   tanpa token.
9. `go build ./... && go vet ./... && go test ./internal/... -count=1` hijau;
   migration `up`/`down` bersih.

---

## 7. Verifikasi

```bash
cd ../backend
migrate -path db/migrations -database "$DSN" up
go build ./... && go vet ./...
go test ./internal/... -v -count=1
go run cmd/web/main.go

TOKEN=$(curl -s -X POST localhost:3000/api/users/_login \
  -H 'Content-Type: application/json' \
  -d '{"id":"user-a","password":"secret"}' | jq -r .data.token)

curl -s -H "Authorization: $TOKEN" 'localhost:3000/api/sos/history?role=sent&size=5' | jq
curl -s -H "Authorization: $TOKEN" 'localhost:3000/api/sos/summary' | jq
curl -s -o /dev/null -w '%{http_code}\n' -H "Authorization: $TOKEN" \
  'localhost:3000/api/sos/history?role=sent&status=closed&page=1&size=20'
curl -s -o /dev/null -w '%{http_code}\n' -H "Authorization: $TOKEN" \
  'localhost:3000/api/sos/history?role=foo'
```

Skenario end-to-end: A trigger → B respond → A resolve → `history?role=sent` A
memuat alert Resolved; `history?role=received` B memuat alert yang sama;
`summary` B `responded.resolved` naik.

Cek query plan (opsional, pastikan 2 index baru terpakai):

```sql
EXPLAIN SELECT * FROM alerts WHERE sender_id = 'user-a'
  AND status IN ('RESOLVED','CANCELLED','EXPIRED')
  ORDER BY triggered_at DESC, id DESC LIMIT 20;
EXPLAIN SELECT COUNT(DISTINCT alert_id) FROM alert_responses WHERE responder_id = 'user-b';
```

---

## 8. Risiko & pertanyaan terbuka

1. **Housekeeping belum ada.** `ExpireOldAlerts` dan `PurgeOldBreadcrumbs`
   sudah ditulis tapi tidak dipanggil siapa pun (`cmd/web` tidak punya ticker,
   worker tidak punya DB). Akibatnya `EXPIRED` praktis tidak pernah muncul dan
   baris `alerts`/`alert_breadcrumbs` tumbuh selamanya. Endpoint di spec ini
   tidak bergantung pada job itu (`status=closed` sudah mencakup `EXPIRED`),
   tetapi keputusan "pasang cron/ticker di web atau job terpisah" harus
   diambil sebelum produksi jangka panjang. Kalau `expired_at` dibutuhkan,
   tambahkan kolom di migration terpisah dan pakai `expiredAtMs` yang sekarang
   dibuang `ExpireOlderThan`.
2. **Retensi riwayat.** Paging wajib; kebijakan retensi (mis. 90 hari) belum
   diputuskan. Kalau ditambahkan, `history` dan `summary` harus memakai filter
   waktu yang sama.
3. **Duplikat respons.** Tidak ada unique `(alert_id, responder_id)`. Semua
   agregat memakai `DISTINCT`; pertimbangkan unique index + pemakaian
   `ErrDuplicateResponse` sebagai perbaikan terpisah.
4. **`responders` di riwayat** sengaja tidak disertakan. Detail "siapa yang
   menolong" tetap lewat `GET /api/sos/{alertId}` (participant-only).
5. **Nama sender** ikut terkirim seperti inbox sekarang. Kalau kebijakan
   privasi berubah (anonim), inbox + history + nearby harus diubah bersamaan.
6. **Fase 3 selalu memask** koordinat, termasuk untuk responder terdaftar.
   Ini keputusan produk (surface discovery), bukan inkonsistensi masking.
7. **Alternatif minimal-diff.** Kalau batch builder dianggap terlalu besar
   untuk Fase 1, fallback yang tetap benar: `ListHistory` memanggil
   `buildDetail(ctx, alert, requesterID, false)` per item (tanpa daftar
   responder karena `buildDetail` hanya mengisinya untuk peserta) dan
   menerima ~3 query/item. Jangan pakai fallback ini untuk `size=100`.
8. **Verifikasi SQL.** Belum ada harness DB di `go test ./internal/...`, jadi
   join/paging/agregat harus diverifikasi manual (§7) atau lewat test live
   opsional di `test/`.

---

## 9. Kesiapan implementasi

Bisa dikerjakan sekarang tanpa blocker teknis. Prasyarat dan batasan:

| Hal | Status |
|---|---|
| Skema DB | Cukup 2 index baru; tidak ada kolom baru. Butuh izin `ALTER TABLE` di MySQL (Aiven). |
| Dependency baru | Tidak ada (GORM, Fiber, validator, haversine sudah ada). |
| Perubahan interface | 8 method baru di `AlertStore` + 2 fake; tidak ada pemanggil lain yang rusak (`AlertSaver` tetap). |
| Endpoint lama | Tidak berubah perilakunya (`buildDetail` hanya di-refactor ke helper dengan output identik). |
| Housekeeping `EXPIRED` | **Tidak** memblokir; tapi tanpa ticker, `expired` di summary akan selalu 0. Putuskan dulu kalau kartu "Expired" mau bermakna. |
| Kontrak frontend | Endpoint tidak boleh dipakai frontend sebelum `api/` di-copy ke `../frontend/api/` (§5.9). |
| Test | Logika teruji tanpa infra; SQL terverifikasi manual (tidak ada harness DB). |
| Migrasi | Butuh CLI `migrate` + DSN (lihat `Archi.Md`, gitignored). |

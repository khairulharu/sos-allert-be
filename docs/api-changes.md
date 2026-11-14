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
- `backend/api/api.md` + `api-spec.json` tidak berubah untuk bagian media (sudah
  memuat ketiga endpoint); mirror-nya di `../frontend/api/` masih identik.
- Dokumen ini juga di-mirror ke `../frontend/docs/api-changes.md` (dengan banner
  "jangan diedit di sini" dan path disesuaikan). Sumber kebenaran tetap salinan
  backend ini — kalau ada perubahan, edit di sini lalu re-copy.

## Media insiden (audio/video)

**Tidak ada perubahan kontrak backend** di bagian ini: endpoint sudah ada dan
aktif sejak `20261108000000_alert_media_transcription`. Bagian ini ditulis
karena frontend memakai `uploadMedia` (sender) dan sekarang juga
`listMedia`/`mediaBlob` (responder yang sudah `respond`).

| Method | Path | Akses |
|---|---|---|
| `POST` | `/api/sos/{alertId}/media` | **sender saja** |
| `GET` | `/api/sos/{alertId}/media` | sender **atau** responder terdaftar |
| `GET` | `/api/sos/{alertId}/media/{mediaId}` | sender **atau** responder terdaftar |

Semua butuh header `Authorization: <token>` mentah (bukan `Bearer`).

### `POST /api/sos/{alertId}/media`

Multipart field `file`. Hanya `audio/*` atau `video/*`, maks **50 MB**
(`maxMediaBytes`). Field lain opsional; sender tidak bisa upload ke insiden
orang lain (`403`) dan insiden non-aktif ditolak (`409`).

Response `{ "data": AlertMediaResponse }`:

```json
{ "id": "...", "alert_id": "...", "filename": "sos-....webm",
  "content_type": "audio/webm", "size_bytes": 184320,
  "uploaded_at": 1762500000000,
  "url": "/api/sos/{alertId}/media/{mediaId}",
  "transcript": "tolong saya terjatuh",
  "transcription_status": "completed",
  "transcribed_at": 1762500001200,
  "triage": { "emergency_type": "MEDICAL", "severity": 0.75, "summary": "Kemungkinan darurat medis" },
  "escalated": true, "recipients_added": 4 }
```

- `transcription_status`: `completed` | `empty` (tak ada suara) | `too_large`
  (>25 MB) | `failed` (provider error, media tetap tersimpan) | `disabled`
  (`ai.stt` mati / bukan audio).
- `triage` absen kalau tidak ada sinyal; `transcript` absen kalau kosong.
- `escalated`/`recipients_added` hanya muncul saat transkrip menaikkan
  severity sehingga fan-out diperlebar. Frontend sudah memakainya
  (`AlertFlow.svelte` → `recipientsAdded`) dan `triage` di response ini yang
  **menimpa** klasifikasi dari `POST /api/sos/classify`.

### `GET /api/sos/{alertId}/media`

`{ "data": AlertMediaResponse[] }`, urut `uploaded_at ASC`. Item memakai bentuk
yang sama dengan response upload, kecuali `triage`/`escalated`/`recipients_added`
yang hanya ada di response upload (tidak disimpan per baris media). `transcript`
tetap ikut, jadi responder bisa menampilkan transkrip tanpa STT ulang. **Tidak**
ada paging.

### `GET /api/sos/{alertId}/media/{mediaId}`

Stream biner: `Content-Type` sesuai `content_type`, `Content-Disposition:
inline`, `Cache-Control: private, max-age=300`.

**Gotcha:** endpoint ini butuh header `Authorization`, jadi tidak bisa dipakai
langsung sebagai `<audio src>` / `<video src>`. Frontend harus `fetch` dulu lalu
`URL.createObjectURL(blob)` (atau `<audio controls>` diisi dari objek itu).
Tidak ada signed URL / token di query string (WS tracking juga tidak lagi
memakai `?token=`).

### Error

| Kondisi | Status |
|---|---|
| bukan sender/responder terdaftar | `403` |
| alert atau media tidak ada | `404` |
| `storage.backend` bukan `local` (S3 belum di-wire) | `412` |
| non audio/video | `400` |
| body upload >50 MB (batas server, bukan handler) | `413` |
| upload ke insiden non-aktif | `409` |

### Batasan yang perlu diketahui UI responder

1. **"Responder terdaftar"** = ada baris di `alert_responses`, yaitu sudah
   `POST /api/sos/{alertId}/respond`. Penerima notifikasi yang belum menekan
   "respond" dapat `403`, bukan `404`.
2. **Tidak ada event WS untuk media.** Hub hanya broadcast `LOCATION_UPDATE`,
   `ALERT_STATUS`, `ALERT_TRIAGE`, `RESPONDER_ADDED`. Tidak ada
   `MEDIA_ADDED`, jadi client harus polling `GET .../media` sendiri. Frontend
   sudah memilih polling (`AlertMedia.svelte`, 12 detik) — tidak perlu event
   baru di backend.
3. **`AlertDetailResponse` tidak memuat media.** `GET /api/sos/{alertId}`
   (termasuk item `history`) tidak memberi tahu bahwa ada rekaman; informasi
   media hanya lewat endpoint media di atas.
4. **Transkrip/triage bisa berbeda dari `POST /api/sos/classify`** karena klip
   diproses ulang (STT non-deterministik). Yang berlaku = hasil `media`
   (menimpa `alerts.summary`/`alerts.severity` dan dikirim sebagai
   `ALERT_TRIAGE`). Kalau upload gagal, nilai dari `classify` yang bertahan.
5. **Storage masih disk lokal** (`storage.local_dir`, default `./uploads`).
   File hilang kalau container di-redeploy tanpa volume persisten, dan
   multi-instance tidak berbagi file.

**Status: selesai.** Sisi frontend sudah jalan (PR `feat/media-audio`):
`sosApi.listMedia`/`mediaBlob` di `api.js`, `AlertMedia.svelte` mem-`fetch` blob
lalu membuat `URL.createObjectURL` (di-revoke saat sheet ditutup), daftar
di-poll tiap 12 detik selama sheet insiden terbuka, dan player + transkrip tampil
untuk responder yang sudah `POST .../respond` (`AlertIncoming.svelte` →
`enabled={!!responded}`). Jadi user yang menerima alert bisa memutar audio/video
dari pengirim SOS. Tidak ada perubahan kontrak backend yang dibutuhkan.

## Participant gate (pentest F-2/F-3/F-4)

Tiga endpoint sekarang hanya melayani **participant** = sender, penerima
notifikasi (`alert_recipients`), atau user yang sudah `respond`.

| Endpoint | Sebelum | Sesudah |
|---|---|---|
| `GET /api/sos/{alertId}` | `200` termask untuk user mana pun | `404` untuk non-participant |
| `POST /api/sos/{alertId}/acknowledge` | `200` untuk user mana pun | `403` untuk non-participant |
| `POST /api/sos/{alertId}/respond` | `200` untuk user mana pun | `403` untuk non-participant |

Yang perlu dicek di UI:

1. **`404` pada detail bukan berarti alert hilang.** Kalau sheet insiden dibuka
dari sumber selain inbox/active (mis. link lama, atau alert yang recipient-nya
sudah dihapus), tampilkan state "insiden tidak tersedia", bukan retry loop.
2. **Recipient yang belum respond tetap dapat payload termask** (`masked: true`,
`responders: []`). Itu disengaja: koordinat persis baru terbuka setelah
`POST .../respond`.
3. `GET /api/sos/active` dan `GET /api/sos/inbox` tidak berubah.

## `GET /api/location/nearby`: `distance_m` dibulatkan (pentest F-1)

`distance_m` sekarang dibulatkan ke kelipatan 100 m terdekat. Tidak ada field
lain yang berubah; `user_id`, koordinat termask, dan `count` tetap seperti
sebelumnya.

```json
{"data":{"radius_m":1000,"count":1,"masked":true,"responders":[
  {"user_id":"user-2","latitude":-6.209,"longitude":106.846,"distance_m":500,"last_seen":1731650000000}
]}}
```

Alasan: dua `distance_m` presisi cukup untuk menginterseksi dua lingkaran dan
mendapat posisi di dalam sel yang sudah diblur. Dengan bucket 100 m, dua
lingkaran tidak lagi bertemu di satu titik.

Yang perlu dicek di UI: label jarak jadi `500 m` / `1.2 km`, bukan `502 m`.
Sortir "nearest" tetap jalan (nilainya masih naik monoton). Pin di peta tidak
berubah sama sekali — berasal dari `latitude`/`longitude` yang sudah diblur.

## Sesi login pindah ke Redis (pentest F-8)

Kontrak `POST /api/users/_login` dan `DELETE /api/users` **tidak berubah**:
login tetap mengembalikan `{ "data": { "token": "<raw token>" } }` dan token
tetap dikirim mentah di header `Authorization`. Yang berubah hanya cara backend
menyimpannya:

- Token sekarang punya masa berlaku (`auth.token_ttl_hours`, default 168 jam).
  Setelah lewat, request berikutnya `401`.
- Login baru **selalu membatalkan token lama** (single-session). Kalau user
  login di device lain, device pertama langsung `401`.
- Logout menghapus sesi di Redis, jadi token langsung mati.
- Saat deploy perubahan ini, **semua token lama tidak berlaku** (kolom
  `users.token` dihapus). Semua user harus login ulang sekali.

Yang perlu dicek di UI:

1. Tangani `401` dengan menghapus token lokal lalu kembali ke layar login.
   Saat ini `src/lib/api.js` belum punya jalur khusus untuk `401`; setelah
   deploy, request pertama dengan token lama akan `401` sekali.
2. `503` pada endpoint ber-auth berarti session store (Redis) sedang down —
   tampilkan pesan "coba lagi", **jangan** hapus token.
3. Perilaku single-session: login di device kedua membuat device pertama
   menerima `401` pada request/heartbeat berikutnya.

## WebSocket tracking: auth lewat frame pertama (pentest F-8)

Token sesi tidak lagi dikirim di URL (`?token=...`). URL sekarang hanya
`/ws/sos/{alertId}/track`; query `?token=` diabaikan server (socket akan
ditutup sebagai unauthorized setelah 10 detik).

Alur baru:

1. Buka `new WebSocket(wsUrl(alertId))` — tanpa query token.
2. Di `onopen`, kirim frame pertama:
   ```json
   { "type": "auth", "token": "<raw session token>" }
   ```
3. Tunggu balasan `{ "type": "auth_ok" }` **sebelum** mengirim/flush breadcrumb
   dan sebelum menandai channel `open`.
4. `{ "error": "..." }` tetap terminal seperti sekarang: tampilkan
   `lastError`, `stopReconnecting()`, jangan reconnect.

Yang perlu diubah di `../frontend/src/lib/tracking.js`:

- `wsUrl()` tidak lagi menambah `?token=`; cukup `${WS_BASE}/ws/sos/${alertId}/track`.
- `ws.onopen` saat ini langsung flush `queue` dan `patch({connection:'open'})`.
  Ubah menjadi: kirim frame auth, lalu flush queue + set `open` di handler
  `auth_ok` (frame auth tidak boleh masuk antrean breadcrumb).
- Reconnect memakai token terbaru (`getToken()`); kalau `401`, auth frame
  berikutnya akan menerima `error` dan channel berhenti.
- `sendBreadcrumb` tetap diantrekan selama socket `CONNECTING`/sebelum
  `auth_ok`; jangan kirim breadcrumb sebagai frame pertama.

Klien native (mobile) tidak wajib mengirim frame auth: token mentah di header
`Authorization` saat handshake diterima dan server tetap membalas `auth_ok`.
Deadline frame pertama 10 detik.

Deploy backend dan frontend harus bersamaan: frontend lama yang masih memakai
`?token=` akan gagal terhubung setelah backend ini naik.

## Allowlist endpoint `POST /api/push/subscriptions`

`endpoint` sekarang harus menunjuk ke push service yang diizinkan
(`fcm.googleapis.com`, `updates.push.services.mozilla.com`,
`web.push.apple.com`; daftar diatur `webpush.allowed_hosts`). Endpoint lain
ditolak `400` — termasuk `http://127.0.0.1/...`, IP privat, dan metadata cloud
(stored SSRF). Endpoint yang tidak dikenal `400`, sama seperti body tanpa
`endpoint`.

Frontend: tidak ada perubahan kode — `PushSubscription.toJSON().endpoint` dari
browser sudah benar selama user memakai Chrome/Firefox/Safari. Kalau
pendaftaran push gagal `400`, tampilkan state "push tidak tersedia" dan
jangan retry berulang. Baris langganan lama yang endpoint-nya di luar
allowlist akan dihapus otomatis saat dispatch berikutnya.

## Rate limit `POST /api/sos/classify`

Route ini sekarang punya kuota per user: **10 panggilan per jam** (sliding
window di Redis, key `ratelimit:classify:<userID>`), diatur lewat
`sos.classify_rate_limit_per_hour`. Alasan: setiap call memakai STT (Whisper)
+ LLM berbayar.

- `429` + header `Retry-After` (detik, batas atas = panjang window, 3600)
  ketika kuota habis. Body tetap `{"errors": ...}`.
- `503` ketika store limiter (Redis) error — endpoint ini **fail-closed**,
  beda dengan limiter login yang fail-open.

Frontend: tangani `429` (tampilkan pesan kuota + jangan retry otomatis) dan
`503` (fallback ke trigger tanpa klasifikasi audio). Tidak ada perubahan pada
`POST /api/sos`.

## Yang belum dikerjakan

- `GET /api/sos/nearby` (Fase 3) belum ada. Kartu ke-3 UI tetap memakai
  `received.active`, jangan berlabel "Nearby" dulu.
- Tidak ada perubahan pada `GET /api/sos/active` atau `GET /api/sos/inbox`.

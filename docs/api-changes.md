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

- Re-copy terakhir: pentest F-6/F-7/F-9/F-11/F-12 (klaim client vs triage
  server, rate limit `classify`, allowlist endpoint push, recover + security
  headers, media download) dari backend `d931206`.
- `api/api.md` + `api/api-spec.json` diperbarui di backend untuk keempat
  perubahan itu (`TriggerSOSRequest.client_*`, `SOSCreatedResponse.client_*`,
  `SOSCreatedResponse.triage`, `classify` 429/503, `push/subscriptions` 400,
  media `attachment` + allowlist Content-Type).
- Sudah disalin ke `../frontend/api/` — `md5sum` keempat file sama per saat
  handoff ini.
- Baris "Last re-copy" di `../frontend/AGENTS.md` sudah diperbarui.
- Dokumen ini juga di-mirror ke `../frontend/docs/api-changes.md` (dengan banner
  "jangan diedit di sini"). Sumber kebenaran tetap salinan backend ini — kalau
  ada perubahan, edit di sini lalu re-copy.

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

## Triage hanya dari server — `severity`/`summary` client jadi "klaim"

> **Untuk agent frontend.** Perubahan kontrak, perlu penyesuaian di
> `src/lib/api.js` / alur trigger.

Dulu `POST /api/sos` menerima `severity` + `summary` (hasil `classify`) dan
**menyimpannya sebagai triage sistem**, ikut menentukan radius fan-out pertama
dan dikirim ke responder seolah-olah hasil analisis. Sekarang nilai itu tidak
dipercaya lagi:

| Dulu | Sekarang |
|---|---|
| `{"severity":0.9,"summary":"..."}` → jadi `alerts.severity/summary` + radius 5 km | diabaikan untuk triage dan radius |
| — | `{"client_severity":0.9,"client_summary":"..."}` → hanya **klaim pelapor**: ditampilkan berlabel "Pelapor menyebut: …" di notifikasi, muncul sebagai `client_severity`/`client_summary` di response, **tidak** mengubah triage/radius |

Yang menentukan triage dan radius sekarang hanya dua hal, dua-duanya dihitung
server:
1. hasil `POST /api/sos/classify` (audio) — **otomatis** dipakai trigger
   berikutnya dari user yang sama dalam **2 menit**, sekali pakai; klien tidak
   perlu mengirim apa pun;
2. triage `description` di body `POST /api/sos` (seperti sebelumnya).

Artinya untuk alur audio, klien cukup:

1. `POST /api/sos/classify` (multipart, field `file`) → `triage`;
2. `POST /api/sos` **tanpa** `severity`/`summary`;
3. server memakai hasil langkah 1 untuk radius dan `triage` di response.

Kalau klien ingin tetap menampilkan apa yang dilaporkan pengguna, kirim
`client_summary` (maks 500 karakter) dan/atau `client_severity` (0..1). Response
`POST /api/sos` mengembalikannya sebagai `client_severity`/`client_summary`.

Catatan tambahan:
- `severity`/`summary` (nama lama) sekarang **diabaikan** — tidak error, jadi
  aplikasi versi lama tetap jalan, hanya radius tidak lagi ikut nilai itu.
- `triage.severity` di response di-clamp ke `0..1` (dulu bisa >1 dari klien).
- `client_severity`/`client_summary` **tidak disimpan** ke DB: hanya dipakai saat trigger (response + notifikasi). Setelah itu `GET /api/sos/{alertId}` hanya memuat triage server.
- Payload push menambah `client_severity`/`client_summary`; `severity`/`summary`
  tetap berisi triage server.
- `GET /api/sos/{alertId}/media/{mediaId}` sekarang `Content-Disposition:
  attachment` dan hanya mengirim `Content-Type` dari allowlist
  (`audio/*`, `video/mp4|webm`, `image/jpeg|png`); tipe lain jadi
  `application/octet-stream`. Blob dari `fetch` tetap bisa diputar, tapi kalau
  kode memeriksa `blob.type`, siapkan fallback.

## `X-Idempotency-Key` di-scope per user (#35)

> **Untuk agent frontend.** Tidak ada perubahan bentuk request/response; ini
> catatan perilaku.

- Redis key sekarang `idempotency:sos:<user_id>:<key>`. `Retry` dari user yang
  sama dengan key yang sama tetap `201` replay alert yang sama (TTL 5 menit
  seperti sebelumnya).
- User **berbeda** dengan key yang sama tidak lagi saling menimpa: masing-masing
  dapat alert sendiri. Klien tidak boleh mengandalkan key global untuk dedup
  lintas akun/device — kirim key yang unik per percobaan trigger (mis. UUID).
- Server juga memverifikasi `alert.sender_id` saat replay; id alert milik user
  lain tidak pernah dikembalikan.
- `409` hanya muncul untuk retry milik user itu sendiri ketika trigger pertama
  masih berjalan (key sudah diklaim, hasil belum tersimpan).

## Klaim idempotency dilepas saat trigger gagal (#36)

> **Untuk agent frontend.** Tidak ada perubahan bentuk request/response; ini
> catatan perilaku.

- Sebelumnya, trigger yang gagal (lokasi tidak ada, gagal tulis DB, dan dulu
  juga gagal mencari penerima) meninggalkan klaim `pending` selama TTL 5
  menit, sehingga retry dengan key yang sama dijawab `409` padahal belum ada
  alert yang dibuat.
- Sekarang klaim dilepas pada setiap kegagalan sebelum hasil tersimpan: retry
  dengan `X-Idempotency-Key` yang sama setelah percobaan gagal akan diproses
  normal dan mengembalikan `201` (bukan `409`).
- Klaim hanya dilepas selama nilainya masih `pending` (compare-and-delete),
  jadi hasil trigger yang sudah selesai tidak pernah terhapus dan tetap
  di-replay seperti sebelumnya.
- Klien tidak perlu mengubah apa pun: key yang sama tetap aman dipakai ulang
  untuk retry setelah error jaringan/server.

## Fan-out SOS fail-open setelah alert tersimpan (#37)

> **Untuk agent frontend.** Tidak ada perubahan bentuk request/response; ini
> catatan perilaku, plus satu metrik baru.

- Sebelumnya, kalau pencarian penerima gagal (`GEOSEARCH`/`users:last_seen`
  error di Redis), `POST /api/sos` mengembalikan `5xx` **setelah** alert
  tersimpan: tidak ada satu pun notifikasi (termasuk SMS kontak darurat), dan
  alert tertinggal `TRIGGERED` selamanya.
- Sekarang fan-out fail-open: alert tetap dibuat, dispatch tetap dijalankan
  dengan daftar penerima kosong (jadi push tidak terkirim ke siapa pun, tetapi
  SMS fallback ke emergency contacts tetap jalan), dan response tetap `201`
  dengan `recipients_notified: 0`.
- `5xx` hanya tersisa untuk kegagalan yang membuat alert sendiri tidak
  tersimpan (atau lookup lokasi pengirim sebelum alert dibuat).
- Klien tidak perlu retry saat melihat `201` dengan `recipients_notified: 0`:
  alert sudah tercatat dan muncul di `GET /api/sos/active`; retry dengan
  `X-Idempotency-Key` yang sama akan me-replay alert itu, bukan membuat
  duplikat.
- Metrik baru di `GET /metrics`: `sos_fanout_failures_total` (counter trigger
  yang di-dispatch dengan fan-out kosong karena lookup penerima gagal). Metrik
  lama `sos_dispatch_latency_ms`, `push_delivery_failures_total`,
  `active_sos_count` tidak berubah.

## Hardening keamanan (#41)

Enam temuan review, semuanya mengubah kontrak HTTP di beberapa titik. Ringkas:

### 1. Safe PIN salah → `403`, bukan `401`

`POST /api/sos/{alertId}/cancel` dengan PIN salah sekarang menjawab **`403`**
dengan body `{"errors":"invalid safe PIN"}`. `401` sekarang **hanya** berarti
sesi tidak valid/kadaluarsa.

Frontend: jangan logout user ketika menerima `403` dari cancel — tampilkan
"PIN salah, coba lagi". Logout tetap hanya untuk `401`.

### 2. Kontak darurat divalidasi + dibatasi

`POST /api/emergency-contacts` sekarang menolak dengan **`422`** (bukan `200`
dan bukan `500`) bila:

| Kondisi | `errors` |
|---|---|
| nomor bukan E.164 | `phone number must be in E.164 format, e.g. +6281234567890` |
| sudah ada 10 kontak | `too many emergency contacts (max 10)` |
| nomor sudah terdaftar untuk user itu | `this phone number is already an emergency contact` |

- E.164 = `+` lalu 8–15 digit (digit pertama bukan `0`). Spasi, `-`, `(`, `)`,
  dan `.` dibuang sebelum validasi, dan nomor **disimpan dalam bentuk
  kanonik** (`+62 812-3456-7890` → `+6281234567890`).
- Kode premium/short code (`1234`) dan format nasional tanpa `+`
  (`081234567890`) ditolak.
- Maksimum **10 kontak per user** (bukan per nomor). Nomor yang sama tidak
  bisa didaftarkan dua kali.
- `GET /api/emergency-contacts` tidak berubah: baris lama yang tidak sesuai
  aturan baru tetap terbaca apa adanya (tidak difilter/diubah).

Frontend: kirim nomor lengkap dengan kode negara (`+62…`). Tangani `422`
dengan menampilkan `errors` di field nomor telepon.

### 3. Baca alert orang lain → `404` (bukan `403`)

Untuk user yang **bukan** peserta (bukan sender/responder), endpoint baca
sekarang menjawab `404` — sama seperti id acak — sehingga id alert tidak bisa
ditebak:

- `GET /api/sos/{alertId}/media`
- `GET /api/sos/{alertId}/media/{mediaId}`
- `GET /api/sos/{alertId}/breadcrumbs`
- frame auth `WS /ws/sos/{alertId}/track` (frame error `{"error":"alert not found"}`)

`GET /api/sos/{alertId}` memang sudah `404`. Aksi tulis (`cancel`,
`acknowledge`, `resolve`, `respond`, `POST …/media`, `POST …/breadcrumbs`)
**tetap `403`** untuk non-peserta; tidak ada perubahan di sana.

Frontend: perlakukan `404` pada endpoint baca di atas sebagai "insiden tidak
ditemukan / sudah tidak tersedia", bukan "tidak punya akses".

### 4. Endpoint Web Push wajib `https` + tidak bisa diambil alih

- `POST /api/push/subscriptions` menolak (`400`) endpoint yang bukan `https`,
  termasuk host yang ada di allowlist (`http://fcm.googleapis.com/...`).
  Allowlist host tetap berlaku seperti sebelumnya.
- Sebuah `endpoint` sekarang **milik user yang mendaftarkannya lebih dulu**.
  User lain yang mengirim endpoint yang sama mendapat **`409`**
  (`push endpoint is already registered to another user`) dan baris lama
  tidak berubah. User yang sama tetap bisa re-subscribe (upsert/refresh).

Frontend: kalau subscribe mengembalikan `409`, jangan retry dengan endpoint
yang sama; pakai `PushSubscription` milik browser itu sendiri atau tampilkan
"push tidak tersedia".

### 5. Body `5xx` generik

Semua response `>= 500` sekarang `{"errors":"internal server error"}`; detail
(SQL, path file, error provider) hanya masuk log server. Ini juga berlaku untuk
`502`/`503` yang sudah ada (mis. STT tidak dikonfigurasi, limiter Redis error):
**status code tetap sama**, hanya body-nya generik. Jadi percabangan di
frontend harus memakai status code, bukan isi `errors`. Pesan `4xx` tidak
berubah, jadi pesan validasi tetap bisa ditampilkan.

## `POST /api/sos/{alertId}/respond` dua kali → `409` (#42)

> **Untuk agent frontend.** Ini perubahan kontrak: satu status code baru.

- Sebelumnya setiap `respond` selalu menyisipkan baris baru, jadi double tap
  atau retry setelah timeout bisa menyimpan responder yang sama dua kali dan
  menggelembungkan `responders_en_route` / daftar `responders`.
- Sekarang `alert_responses` punya unique index `(alert_id, responder_id)`.
  Jawaban kedua dari responder yang sama tidak menulis baris baru dan
  dijawab **`409`** dengan body
  `{"errors":"responder has already responded to this alert"}`.
- Respons `200` pertama tetap sama (`response_id`, `eta_seconds`,
  `victim_distance_meters`). Klien tidak perlu mengubah apa pun untuk kasus
  sukses; kalau menerima `409`, anggap "sudah tercatat" — jangan retry, dan
  tampilkan status responder dari `GET /api/sos/{alertId}`.
- Responder **berbeda** tidak terpengaruh: masing-masing tetap mendapat `200`
  dan satu entri di `responders[]`.
- `403` (bukan peserta, atau sender mencoba respond) dan `409` untuk alert
  yang sudah `RESOLVED`/`CANCELLED` tidak berubah.

## WebSocket tracking: batas koneksi + keepalive (#42)

> **Untuk agent frontend.** Status code baru saat handshake dan close code baru.

- `WS /ws/sos/{alertId}/track` sekarang punya batas koneksi bersamaan:
  global (`sos.ws_max_connections`, default 500), per IP
  (`sos.ws_max_per_ip`, default 10) dan per user (`sos.ws_max_per_user`,
  default 3, dihitung setelah frame `auth` diverifikasi).
- Kalau batas global/IP sudah penuh, **handshake ditolak** dengan `429` +
  header `Retry-After: 5` (bukan upgrade lalu diputus). Frontend: jangan
  retry loop; tampilkan "live tracking tidak tersedia" dan coba lagi setelah
  `Retry-After`, atau fallback ke polling `GET /api/sos/{alertId}`.
- Kalau batas per user penuh, socket **sempat terbuka** lalu langsung dikirim
  frame `{"error":"too many live-tracking connections, retry shortly"}` dan
  ditutup dengan close code `1013` (try again later) — `auth_ok` tidak pernah
  dikirim. Frontend: tutup tab/socket lain dulu, jangan reconnect otomatis.
- Keepalive: server mengirim ping tiap ~25 s dan memutus klien yang tidak
  menjawab dalam ~60 s (browser menjawab otomatis; klien native harus membaca
  frame ping). Setiap frame server punya write deadline 5 s, jadi klien yang
  berhenti membaca akan diputus, bukan menggantung slot.
- Tidak ada perubahan pada frame `auth`/`auth_ok`, `LOCATION_UPDATE`,
  `ALERT_STATUS`, `ALERT_TRIAGE`, `RESPONDER_ADDED`.

## `POST /api/sos` tanpa lokasi → `422` (#42)

> **Untuk agent frontend.** Status code berubah dari `500` menjadi `422`.

- Kalau body `POST /api/sos` tidak memuat `latitude`/`longitude` **dan** user
  belum pernah mengirim heartbeat (`PUT /api/location`), server sekarang
  menjawab **`422`** dengan
  `{"errors":"location is required: send latitude and longitude in the SOS body or PUT /api/location first"}`.
- Sebelumnya kondisi ini keluar sebagai `500` dengan body generik, jadi user
  pertama kali tidak tahu apa yang harus diperbaiki.
- Frontend: kirim koordinat di body SOS bila tersedia (paling aman), atau
  pastikan `PUT /api/location` sudah sukses sebelum trigger; kalau menerima
  `422`, tampilkan pesan lokasi dan coba lagi setelah lokasi tersedia.

## Transkrip audio: klip senyap jadi `empty`, bukan teks halusinasi (#54)

> **Untuk agent frontend.** Ada perilaku baru + satu bug state yang harus
> diperbaiki di `AlertFlow.svelte`.

Whisper (Groq `whisper-large-v3-turbo`) dulu mengubah audio senyap/noise
menjadi teks subtitle seperti `"Terima kasih telah menonton!"` atau `"Hai"`,
lalu teks itu disimpan sebagai `transcript` dan dipakai triage. Sekarang
backend memutuskan sendiri:

- Request STT mengirim `response_format=verbose_json` dan `prompt` kosakata
  darurat Indonesia (fallback ke `json` bila provider menolak).
- Hasil dianggap **tidak ada suara** bila: semua segmen `no_speech_prob` ≥ 0.6,
  hanya ada repetisi (`compression_ratio` > 2.4), `avg_logprob` sangat rendah
  pada teks pendek, durasi < 0.5 s, atau teks pendek yang persis frasa
  halusinasi umum ("terima kasih", "hai", "something happen…", dst).
- Kasus itu mengembalikan `transcription_status: "empty"`, `transcript` kosong,
  **tidak** memanggil triage, dan **tidak** mengirim `ALERT_TRIAGE`.
- `POST /api/sos/classify` untuk klip senyap menjawab **`200`** dengan
  `{"transcription_status":"empty"}` — bukan `502`. `502` sekarang hanya untuk
  provider yang benar-benar error.
- `transcribed_at` hanya diisi ketika provider benar-benar menjawab (sukses
  atau `empty`); transkripsi yang gagal tidak lagi punya timestamp.

**Perbaikan wajib di FE (akar "transcript tidak sesuai audio"):**
`AlertFlow.svelte` menyimpan `transcript` sebagai state satu layar dan hanya
menimpanya bila nilainya truthy (`if (classified?.transcript)` baris ~246 dan
`if (result?.transcript)` baris ~513). Akibatnya klip baru yang senyap tetap
menampilkan transkrip klip sebelumnya. Lakukan:

1. Reset `transcript`/`transcriptionStatus`/`triageFromAudio` setiap SOS baru
   dimulai **dan** di awal `uploadClip`/sebelum `classify`.
2. Tetapkan hasil apa adanya: `transcript = result?.transcript || ''` dan
   `transcriptionStatus = result?.transcription_status || ''` — jangan
   mempertahankan teks lama saat response tidak membawa transkrip.
3. Tampilkan transkrip **per item** dari `GET /api/sos/{alertId}/media`
   (`AlertMedia.svelte` sudah benar); panel "Audio transcript · N s clip" di
   layar aktif sebaiknya terikat ke media id klip itu, bukan state global.
4. Kalau `classify` gagal/`empty`, jangan tampilkan transkrip dari klip lain —
   tampilkan catatan status (`empty`, `failed`, `disabled`, `too_large`).

## `severity` 0 adalah skor nyata, absen = belum diklasifikasi (#55)

> **Untuk agent frontend.** Tipe JSON `severity` di detail/history berubah
> menjadi optional number.

- Dulu skor 0 (level terendah rubrik Jev) dibuang dan kolomnya tetap `NULL`,
  sehingga UI jatuh ke default. Sekarang 0 disimpan dan dikirim sebagai `0`.
- `alerts.severity` `NULL` tetap berarti "belum pernah diklasifikasi". Di
  `AlertDetailResponse` (`GET /api/sos/{alertId}`, item `history`, `inbox`),
  field `severity` **absen** untuk kasus itu dan **ada** (boleh `0`) bila
  klasifikasi menghasilkan skor.
- `triage.severity` di response `POST /api/sos`, `POST /api/sos/classify`,
  `POST .../media`, dan frame `ALERT_TRIAGE` tidak berubah tipenya (number),
  termasuk saat nilainya 0.
- Awalan `radius` tidak berubah: skor 0 tetap memakai radius 1 km.
- Triage tanpa `emergency_type` (mis. engine hanya mengembalikan summary) kini
  dianggap **tanpa sinyal**, sama seperti `AITriage`: tidak menimpa
  `emergency_type` yang sudah ada dan tidak mengirim `ALERT_TRIAGE`.

Yang perlu dicek FE: jangan lagi memakai `severity` yang hilang sebagai "0";
`Number.isFinite` pada nilai `undefined` sudah menghasilkan `false`, jadi
kartu sebaiknya menampilkan state "belum diklasifikasi" alih-alih bar 0%.
Kombinasi dengan perbaikan triage di atas menutup keluhan "tipe darurat
menempel dari klip sebelumnya".

## Yang belum dikerjakan

- `GET /api/sos/nearby` (Fase 3) belum ada. Kartu ke-3 UI tetap memakai
  `received.active`, jangan berlabel "Nearby" dulu.
- Tidak ada perubahan pada `GET /api/sos/active` atau `GET /api/sos/inbox`.

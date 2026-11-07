# SOS-Allert Backend — Real-Time Geospatial Emergency Dispatch Engine

Mission-critical, high-availability Go backend for real-time proximity-based SOS alerts, emergency contact broadcast, and live responder dispatch.

Designed under Silicon Valley life-safety system principles: **<200ms dispatch latency (p99)**, **zero-drop multi-channel failover**, and **resilient geospatial telemetry**.

---

## 1. System Overview & Core Concept

When a user in distress triggers an emergency SOS:
1. **Instant Geolocation Capture & Ingestion**: The system locks the user's latest coordinates and device telemetry.
2. **Multi-Tiered Notification Fan-out**:
   - **Tier 1 (Emergency Circle)**: Instant Push + SMS/Voice call to registered family/emergency contacts regardless of distance.
   - **Tier 2 (Proximity Crowd Responders)**: Push alert to active nearby users within dynamic radius ($1\text{ km}$ default, adaptive based on responder density).
   - **Tier 3 (Emergency Services / PSAP)**: Escalation to local first responders / dispatch centers.
3. **Live Breadcrumb Tracking**: Continuous bidirectional streaming (WebSocket/gRPC) tracks the victim if moving (e.g. kidnapping, vehicular pursuit).
4. **Incident Lifecycle Management**: Full state machine from trigger to responder acknowledgment, resolution, or authenticated cancellation.

---

## 2. Feature Implementation & Priority Roadmap

### Status Legend
- ✅ **Done**: Implemented, tested, and running in current codebase.
- 🟡 **In Progress**: Partially built or stubbed; needs production hardening.
- 🔴 **P0 (Blocker for Production / Life-Safety)**: Must have before public launch.
- 🟠 **P1 (High Priority / Core Experience)**: Required for real-world reliability.
- 🔵 **P2 (Scale, Trust & Safety, Enterprise)**: Post-launch enhancements.

---

### Matrix Fitur

| Kategori | Fitur | Status | Prioritas | Keterangan & Spesifikasi Teknis |
|---|---|:---:|:---:|---|
| **Core Geospatial** | Haversine distance calculator | ✅ Done | - | Boundary-checked distance helper (`< 1 km`). |
| | Redis GEO user positioning (`PUT /api/location`) | ✅ Done | - | High-throughput Redis GEO `GEOADD` (`users:locations`) + upsert histori MySQL `locations`. |
| | Location read & nearby responders (`GET /api/location`, `GET /api/location/nearby`) | ✅ Done | - | Posisi terakhir + `stale`; responder aktif di radius 100–5000 m, stale > 2 menit difilter, koordinat dimask ~100 m, maks 50. |
| | Stale user filter (`users:last_seen`) | ✅ Done | - | Redis Hash timestamp tracking; excludes idle users > 2 min from SOS fan-out and the nearby map. |
| | Dynamic Adaptive Radius | ✅ Done | **P1** | Adapt radius ($500\text{m} - 5\text{km}$) based on urban/rural responder density; limit max recipient fan-out (cap: 50 recipients). |
| **AI Triage** | SOS description classifier | ✅ Done | **P1** | Optional AI triage on `POST /api/sos`: `description` → classify `DANGER/MEDICAL/ACCIDENT` + severity 0–1 + summary. Providers: offline keyword classifier (default, zero-dependency) / Ollama / OpenAI (`ai.*` config); severity drives the adaptive-radius start (more severe = broader fan-out); fail-open: engine errors fall back to rule-based, so zero-drop life safety never depends on AI. Audio evidence ≤ 25 MB is transcribed via Groq/Whisper-compatible STT and fed into the same triage engine. `POST /api/sos/classify` classifies a clip pre-trigger so the first fan-out can use it; a post-trigger clip that raises severity widens the fan-out to the new radius. |
| **Incident Lifecycle** | `POST /api/sos` trigger & record persistence | ✅ Done | - | Atomic alert persistence in MySQL (`alerts` table) + Redis GeoSearch. |
| | SOS State Machine | ✅ Done | **P0** | Lifecycle: `TRIGGERED` $\to$ `ACKNOWLEDGED` $\to$ `RESOLVED` / `CANCELLED` / `EXPIRED`. |
| | False Alarm Cancellation (`POST /api/sos/:id/cancel`) | ✅ Done | **P0** | 10s countdown grace period + secure 4-digit Duress/Safe PIN to abort accidental sirens or forced cancellations under duress. |
| | Incident Query & Active Status API | ✅ Done | **P0** | `GET /api/sos/:id` & `GET /api/sos/active` for polling/sync on app cold start. |
| | Idempotency Key & Deduplication | ✅ Done | **P0** | Prevent duplicate alerts caused by mobile retry/flaky cellular networks via `X-Idempotency-Key` in Redis. |
| **Multi-Channel Dispatch** | Composite `AlertDispatcher` | ✅ Done | - | FCM/APNs/Twilio when configured; log-only fallback when a channel has no credentials. |
| | APNs Critical Alerts & FCM v1 | 🟡 In Progress | **P0** | Apple Critical Alerts (bypasses iOS Silent/DND) + Android High-Priority FCM with custom siren sound channel. |
| | Emergency Contacts SMS/Call Fallback | 🟡 In Progress | **P0** | Integration with Twilio/AWS SNS/Infobip: SMS + automated voice call with Google Maps link to verified contacts. |
| | Kafka Asynchronous Notification Worker | ✅ Done | **P1** | Decouple HTTP trigger from slow push/SMS gateways via Kafka `sos.dispatched` topic with DLQ and exponential backoff. |
| | PSAP / 911 / E911 Integration | 🔵 Planned | **P2** | RapidSOS / Noonlight integration to push location directly to 911 CAD (Computer Aided Dispatch). |
| **Live Tracking & Telemetry** | WebSocket Live Tracking Stream | ✅ Done | **P1** | Bidirectional WS `/ws/sos/:id/track` for real-time moving coordinates update (interval 3-5s). |
| | Breadcrumb History (`alert_breadcrumbs`) | ✅ Done | **P1** | Store trajectory trail (lat, lng, speed, heading, altitude, accuracy) to reconstruct movement on responders' map. |
| | Device Telemetry Monitoring | 🟡 In Progress | **P1** | Track battery level, battery charging state, network quality, and automated fall/crash sensor flags. |
| | Incident Media & Audio Capture | ✅ Done | **P1** | 10-second ambient audio recording uploaded to `POST /api/sos/:id/media` (local storage); audio ≤ 25 MB is transcribed and used for post-trigger triage. S3/GCS backend still pending. |
| **Responder Operations** | Responder Acceptance & ETA | ✅ Done | **P1** | Responders can click "I am coming" (`POST /api/sos/:id/respond`), calculating ETA & live distance to victim. |
| | Anonymized In-Incident Comms | 🔵 Planned | **P2** | Ephemeral, masked chat or WebRTC voice channel between victim and responding volunteers. |
| | Emergency Medical ID Card | 🔵 Planned | **P2** | Victim's blood type, allergies, medications, and emergency contacts accessible only during active SOS. |
| **Safety & Geofencing** | Geofenced Safe Zones | 🔵 Planned | **P2** | Define "Home", "Campus", "Office" polygons; automatic check-in timers (e.g. "Alert if I don't reach home in 30 mins"). |
| **Anti-Abuse & Rate Limiting** | ✅ Done | **P0** | Redis sliding-window: SOS max 3 trigger/jam/user; auth (`POST /api/users`, `_login`) max 5 percobaan/menit/IP + lockout 15 menit. Wajib set `web.proxy_header` kalau deploy di belakang proxy/tunnel (§9), kalau tidak limit per-IP jadi satu bucket global untuk semua user. |
| | Privacy Masking & Ephemeral Retention | ✅ Done | **P2** | Mask exact victim street number to unconfirmed crowd responders; auto-purge precise breadcrumbs 30 days post-incident. |
| **Observability & SRE** | Prometheus Metrics (manual `/metrics`) | ✅ Done | **P1** | Expose metrics: `sos_dispatch_latency_ms`, `push_delivery_failures_total`, `active_sos_count`. |

---

## 3. Architecture & Data Flow

```mermaid
sequenceDiagram
    autonumber
    actor Victim as Victim Mobile App
    participant API as Fiber HTTP / WS Gateway
    participant Redis as Redis (GEO & Cache)
    participant DB as MySQL (Aiven)
    participant Kafka as Kafka Event Bus
    participant Worker as Notification Consumers
    participant Push as FCM / APNs / Twilio
    actor Responders as Nearby Users & Contacts

    Victim->>API: POST /api/sos (coords, battery, idempotency_key)
    activate API
    API->>Redis: Set Idempotency Lock & Fetch Last Known Location
    API->>DB: INSERT alerts (status: TRIGGERED, lat, lng, time)
    API->>Redis: GEOSEARCH users:locations (radius: 1km)
    Redis-->>API: Return Nearby Candidates
    API->>Redis: Filter Stale Candidates (users:last_seen < 120s)
    API->>Kafka: Produce Event: sos.triggered
    API-->>Victim: 200 OK (alert_id, status: TRIGGERED, recipient_count)
    deactivate API

    par Parallel Asynchronous Dispatch
        Kafka->>Worker: Consume sos.triggered
        Worker->>DB: Query Emergency Contacts (SMS/Voice)
        Worker->>Push: Send APNs Critical Alert + FCM Siren
        Push-->>Responders: High-Priority Emergency Siren Notification
    and Real-Time Tracking
        Victim->>API: WS /ws/sos/{id}/track (live coords stream every 3s)
        API->>Redis: XADD stream:sos:{id}:breadcrumbs
        API-->>Responders: WS Push Live Coordinate Vector & Map Pin
    end
```

---

## 4. API Specification

### Current Active Endpoints

| Method | Path | Auth | Keterangan |
|---|---|:---:|---|
| `POST` | `/api/users` | ❌ | Registrasi user baru |
| `POST` | `/api/users/_login` | ❌ | Login, return raw session token |
| `DELETE` | `/api/users` | ✅ | Logout session |
| `PATCH` | `/api/users/_current` | ✅ | Update profil user saat ini; kirim `safe_pin` (4 digit, disimpan sebagai hash), `fcm_token`, atau `apns_token` untuk mengaturnya |
| `GET` | `/api/users/_current` | ✅ | Ambil profil user saat ini |
| `PUT` | `/api/location` | ✅ | Heartbeat lokasi user ke Redis GEO (`users:locations` & `users:last_seen`) + upsert histori MySQL `locations` |
| `GET` | `/api/location` | ✅ | Baca heartbeat lokasi terakhir user (`last_seen`, `stale`) dari Redis |
| `GET` | `/api/location/nearby` | ✅ | Responder aktif di sekitar user (`radius_m` 100–5000, default 1000); stale > 2 menit & diri sendiri difilter, koordinat dimask ~100 m, maks 50. `users:last_seen` dibaca satu `HMGET` per request (bukan `HGET` per kandidat) |
| `POST` | `/api/sos` | ✅ | Trigger SOS (body opsional sesuai spek §4, header `X-Idempotency-Key` opsional, rate limit 3/jam — matikan dengan `sos.rate_limit_disabled: true`). `emergency_type`/`severity`/`summary` eksplisit menang atas hasil triage deskripsi |
| `POST` | `/api/sos/classify` | ✅ | Klasifikasi audio pra-trigger (multipart `file`, audio ≤ 25 MB, tidak disimpan) → `{emergency_type, severity, summary, transcript, transcription_status}`; kirim hasilnya di body trigger agar fan-out pertama sudah memakai level dari suara |
| `GET` | `/api/sos/active` | ✅ | Daftar insiden aktif milik user (untuk cold start) |
| `GET` | `/api/sos/inbox` | ✅ | Inbox responder: insiden aktif yang push-nya pernah dikirim ke user ini |
| `GET` | `/api/sos/:alertId` | ✅ | Detail insiden; lokasi dimask untuk viewer yang bukan sender/responder |
| `POST` | `/api/sos/:alertId/cancel` | ✅ | Batalkan false alarm (PIN 4 digit + alasan) |
| `POST` | `/api/sos/:alertId/acknowledge` | ✅ | `TRIGGERED` → `ACKNOWLEDGED` |
| `POST` | `/api/sos/:alertId/resolve` | ✅ | `TRIGGERED`/`ACKNOWLEDGED` → `RESOLVED` (sender saja) |
| `POST` | `/api/sos/:alertId/respond` | ✅ | Responder menjawab, hitung ETA & jarak |
| `GET/POST` | `/api/sos/:alertId/breadcrumbs` | ✅ | Riwayat breadcrumb / kirim satu titik tracking |
| `POST` | `/api/sos/:alertId/media` | ✅ | Upload media (multipart `file`, audio/video, max 50 MB, penyimpanan lokal); audio ≤ 25 MB ditranskrip (Groq/Whisper-compatible) lalu transcript masuk triage; response memuat `url`, `transcript`, `transcription_status`, `triage`. Kalau triage audio menaikkan severity, fan-out diperluas ke radius lebih lebar: `escalated: true` + `recipients_added: N` |
| `GET` | `/api/sos/:alertId/media` | ✅ | Daftar media insiden (sender/responder saja) |
| `GET` | `/api/sos/:alertId/media/:mediaId` | ✅ | Stream/download media; header `Authorization`, `Content-Type` asli |
| `WS` | `/ws/sos/:alertId/track?token=<token>` | ✅ | Live tracking stream (sender kirim breadcrumb, responder terima `LOCATION_UPDATE`) |
| `GET/POST` | `/api/emergency-contacts` | ✅ | CRUD Tier-1 emergency contacts untuk SMS fallback |
| `GET` | `/api/push/vapid-public-key` | ✅ | VAPID public key untuk `PushManager.subscribe()`; `503` saat `webpush.enabled: false` atau key kosong |
| `GET/POST` | `/api/push/subscriptions` | ✅ | Daftar / simpan langganan Web Push browser (`endpoint`, `p256dh`, `auth`, `user_agent`); `endpoint` unik → re-subscribe = upsert |
| `DELETE` | `/api/push/subscriptions` | ✅ | Hapus langganan milik user (body `{"endpoint": "..."}`); endpoint tak dikenal tetap `200` |
| `GET` | `/metrics` | ❌ | Metrik Prometheus (`sos_dispatch_latency_ms`, `push_delivery_failures_total`, `active_sos_count`) |
| `GET/POST` | `/api/contacts` | ✅ | CRUD daftar kontak |
| `GET/POST` | `/api/contacts/:id/addresses` | ✅ | CRUD alamat kontak |

> **Format Auth**: Header `Authorization: <token>` (raw token langsung, tanpa prefix `Bearer `).

---

### Target Roadmap Endpoints (P0 & P1 Specifications)

#### 1. Incident Lifecycle Management

```http
POST /api/sos
Header: Authorization: <token>, X-Idempotency-Key: <uuid>
Body:
{
  "latitude": -6.2088,
  "longitude": 106.8456,
  "accuracy_meters": 4.5,
  "battery_percentage": 18,
  "emergency_type": "DANGER", // DANGER | MEDICAL | ACCIDENT (optional; AI triage can classify it)
  "description": "Ada orang pingsan di pantai, tidak sadar", // optional; feeds AI triage
  "severity": 0.8, // optional; hasil POST /api/sos/classify, menang atas triage deskripsi
  "summary": "PRIORITAS TINGGI: Kemungkinan darurat medis" // optional; ikut ke push/notifikasi
}
Response 201 Created:
{
  "data": {
    "alert_id": "alt_01HXYZ...",
    "status": "TRIGGERED",
    "grace_period_seconds": 10,
    "recipients_notified": 7,
    "triggered_at": 1730304000000,
    "triage": { "emergency_type": "MEDICAL", "severity": 0.8, "summary": "PRIORITAS TINGGI: Kemungkinan darurat medis" }
  }
}
```

```http
POST /api/sos/:alertId/cancel
Header: Authorization: <token>
Body:
{
  "pin": "1234",          // Verified against user's safe PIN
  "reason": "FALSE_ALARM" // ACCIDENTAL | SAFE_NOW | TEST
}
Response 200 OK:
{
  "data": {
    "alert_id": "alt_01HXYZ...",
    "status": "CANCELLED",
    "cancelled_at": 1730304015000
  }
}
```

```http
GET /api/sos/:alertId
Header: Authorization: <token>
Response 200 OK:
{
  "data": {
    "alert_id": "alt_01HXYZ...",
    "sender": { "id": "usr_1", "name": "Jane Doe" },
    "status": "TRIGGERED",
    "emergency_type": "MEDICAL",
    "severity": 0.8,                                   // ada kalau triage jalan
    "summary": "PRIORITAS TINGGI: Kemungkinan darurat medis",
    "initial_location": { "latitude": -6.2088, "longitude": 106.8456 },
    "latest_location": { "latitude": -6.2091, "longitude": 106.8460, "updated_at": 1730304050000 },
    "responders_en_route": 2,
    "triggered_at": 1730304000000
  }
}
```

> `masked: true` muncul (dan koordinat dibulatkan ~100 m) kalau viewer bukan
> sender/responder. `severity`/`summary` ikut tersimpan di tabel `alerts`, jadi
> tampil juga di `GET /api/sos/active` dan `GET /api/sos/inbox`.

#### 2. Live Tracking Stream (WebSocket)

```
WS /ws/sos/:alertId/track?token=<token>

Client -> Server (Pelapor kirim breadcrumb):
{
  "latitude": -6.2092,
  "longitude": 106.8462,
  "speed_mps": 4.2,
  "heading_deg": 135.0,
  "battery_percentage": 17,
  "timestamp": 1730304060000
}

Server -> Responders (Broadcast posisi terkini):
{
  "type": "LOCATION_UPDATE",
  "alert_id": "alt_01HXYZ...",
  "latitude": -6.2092,
  "longitude": 106.8462,
  "speed_mps": 4.2,
  "heading_deg": 135.0
}

Server -> Responders (perubahan status, tanpa perlu polling):
{
  "type": "ALERT_STATUS",
  "alert_id": "alt_01HXYZ...",
  "status": "RESOLVED",   // ACKNOWLEDGED | RESOLVED | CANCELLED
  "updated_at": 1730304075000
}

Server -> Sender/Responders (triage dari audio yang baru ditranskrip):
{
  "type": "ALERT_TRIAGE",
  "alert_id": "alt_01HXYZ...",
  "emergency_type": "MEDICAL",
  "severity": 0.8,
  "summary": "PRIORITAS TINGGI: Kemungkinan darurat medis",
  "updated_at": 1730304075000
}
```

#### 3. Responder Coordination

```http
POST /api/sos/:alertId/respond
Header: Authorization: <token>
Body:
{
  "responder_latitude": -6.2100,
  "responder_longitude": 106.8470
}
Response 200 OK:
{
  "data": {
    "response_id": "rsp_01ABC...",
    "eta_seconds": 180,
    "victim_distance_meters": 240
  }
}
```

---

## 5. AI Triage (Text + Audio Evidence)

Optional AI layer di `POST /api/sos`. Saat pelapor mengisi `description`
(misal `"ada orang pingsan di pantai, tidak sadar"`), backend mengklasifikasikannya
menjadi **emergency type** (`DANGER/MEDICAL/ACCIDENT`), **severity score 0–1**,
dan **summary singkat**. Severity dipakai untuk memilih radius adaptive fan-out
(1/2/5 km): insiden makin urgent → fan-out makin lebar ke user sekitar. Hasil
triage dikembalikan di response sebagai `triage`.

### Fail-open (life-safety first)

AI **tidak pernah** memblokir trigger SOS:

- `ai.enabled: false`, atau engine error/timeout → berperilaku persis pre-AI
  (request default, radius mulai 1 km).
- `description` kosong / tidak terdeteksi keyword → zero signal (DANGER default, 1 km).
- Request `emergency_type` eksplisit menang atas hasil klasifikasi AI.

### Provider

| Provider | `ai.provider` | Keterangan |
|---|---|---|
| Offline classifier (default) | `offline` | Rule-based keyword (ID/EN), deterministic, zero-credential, microseconds. |
| Ollama (lokal) | `ollama` | `POST {ollama.base_url}/api/chat` dengan structured JSON output. |
| OpenAI / compatible gateway | `openai` | chat completions + `response_format: json_object`. |

Provider live (`ollama`/`openai`) + fallback otomatis ke offline classifier saat
gagal/timeout. Mapping severity → radius: `0–0.33` → 1 km, `0.34–0.66` → 2 km,
`≥0.67` → 5 km.

### Speech-to-text untuk audio evidence

Frontend merekam 10 detik audio setelah alert aktif, lalu mengirim satu file
`.webm`/`.m4a`/`.ogg` ke `POST /api/sos/:alertId/media`. Backend:

1. Menyimpan media (bukti insiden).
2. Audio ≤ 25 MB ditranskrip lewat provider OpenAI-compatible (Groq/OpenAI/local).
3. Transcript dimasukkan ke engine triage yang sama.
4. `alerts.emergency_type/severity/summary` di-update dan `ALERT_TRIAGE`
   disiarkan lewat WebSocket.
5. Response upload memuat `transcript`, `transcription_status`, dan `triage`.

STT/triage fail-open: kalau provider error, media tetap tersimpan dengan
`transcription_status: "failed"`; upload tidak gagal. Nilai status:
`disabled`, `too_large`, `completed`, `empty`, `failed`.

### Klasifikasi audio pra-trigger (`POST /api/sos/classify`)

Frontend merekam klip selama countdown 10 detik, jadi klip itu sudah ada saat
alert dikirim. Supaya **fan-out pertama** sudah memakai level dari suara (bukan
dari deskripsi), kirim klipnya dulu:

```http
POST /api/sos/classify
Header: Authorization: <token>
Content-Type: multipart/form-data  (field `file`, audio ≤ 25 MB)
Response 200:
{
  "data": {
    "emergency_type": "MEDICAL",
    "severity": 0.8,
    "summary": "PRIORITAS TINGGI: Kemungkinan darurat medis",
    "transcript": "ada orang pingsan tidak sadar",
    "transcription_status": "completed"
  }
}
```

Endpoint ini **tidak menyimpan apa pun** (audio, media, alert). Hasilnya
dikirim ulang di body `POST /api/sos` (`emergency_type` + `severity` +
`summary`), dan nilai eksplisit itu menang atas triage deskripsi, sehingga
radius adaptive (1/2/5 km) langsung mengikuti severity dari suara. Karena
fan-out baru jalan setelah grace period 10 detik, latency STT (ratusan ms)
terserap oleh countdown dan tidak menunda notifikasi.

Error: `400` untuk non-audio / audio > 25 MB, `503` kalau `ai.stt` belum aktif
(tidak ada key), `502` kalau provider STT gagal. Frontend boleh mengabaikannya
dan tetap memakai triage deskripsi.

### Re-fanout saat audio menaikkan severity

Kalau klip baru diunggah **setelah** trigger (alur `POST /api/sos/:alertId/media`),
transkrip bisa menaikkan severity dari level yang dipakai fan-out pertama.
Backend memperlebar sendiri jangkauannya:

- Hanya responder **baru** (di radius lebih lebar, belum pernah dicatat untuk
  alert ini) yang dinotifikasi; penerima awal tidak di-notify ulang.
- Notifikasi ini tetap menunggu grace period (`TRIGGERED` + 10 detik) dan
  re-check status, jadi alert yang di-cancel di countdown tidak menyebar.
- Kalau dispatch lewat Kafka, event `sos.dispatched` kedua dikirim dengan daftar
  penerima tambahan saja.
- Response upload menandai perluasan: `escalated: true`, `recipients_added: N`.
- Kontak darurat pengirim **tidak** di-SMS dua kali (kanal push-only).

Responder yang sudah menjawab level baru tetap menerima frame `ALERT_TRIAGE`
di WebSocket tracking; mengubah tampilan sheet responder jadi tugas frontend.

**API key Groq** ditempel di `backend/config.json` → `ai.stt.api_key`
(nilai `gsk_...`), bukan di frontend. Set `ai.stt.enabled: true`.

| Provider STT | `ai.stt.provider` | Key |
|---|---|---|
| Groq (default) | `groq` | `ai.stt.api_key` (`gsk_...`) |
| OpenAI Whisper | `openai` | `ai.stt.api_key`, fallback `ai.openai.api_key` |
| Local Whisper (OpenAI-compatible) | `local` | tidak perlu key |

Model default Groq: `whisper-large-v3-turbo`, language default `id`.
Batas Groq free tier: 25 MB per file; backend hanya mengirim audio ≤ 25 MB ke STT.

### Config (`config.json`, section `ai`)

```json
"ai": {
  "enabled": true,
  "provider": "offline",            // offline | ollama | openai
  "timeout_seconds": 5,
  "stt": {
    "enabled": true,
    "provider": "groq",             // groq | openai | local
    "base_url": "https://api.groq.com/openai/v1",
    "api_key": "gsk_...",
    "model": "whisper-large-v3-turbo",
    "language": "id",
    "timeout_seconds": 30
  },
  "ollama": { "base_url": "http://localhost:11434", "model": "llama3" },
  "openai": { "base_url": "https://api.openai.com/v1", "api_key": "", "model": "gpt-4o-mini" }
}
```

### Contoh

```bash
curl -X POST http://localhost:3000/api/sos \
  -H "Authorization: <token>" \
  -d '{"latitude":-8.65,"longitude":115.22,"description":"ada orang pingsan, tidak sadar"}'
```

```json
{
  "data": {
    "alert_id": "alt_01...",
    "status": "TRIGGERED",
    "recipients_notified": 7,
    "triage": {
      "emergency_type": "MEDICAL",
      "severity": 0.8,
      "summary": "PRIORITAS TINGGI: Kemungkinan darurat medis"
    }
  }
}
```

### Isi Push Notification

Hasil triage ikut masuk ke push (FCM), dan tetap sama saat dispatch lewat
Kafka worker (`sos.dispatched` membawa `severity` + `summary`):

- `data.severity` → `"0.80"` (format 2 desimal, hanya kalau triage jalan).
- `data.summary` → ringkasan AI (hanya kalau tidak kosong).
- Body notifikasi → `<summary> — Emergency MEDICAL nearby. Open the app for the live location.`
  Tanpa summary, body tetap seperti sebelumnya.

**Kontrak penerimaan push (wajib di client):** backend mengirim ke
`fcm_token`/`apns_token` user kalau terdaftar; kalau tidak ada, ke FCM topic
`user_<user_id>`. Jadi app harus memilih salah satu:

1. Kirim token setelah login/refresh: `PATCH /api/users/_current`
   `{"fcm_token": "<token>"}` (atau `apns_token`), **atau**
2. Subscribe topic `user_<user_id>` di FCM setelah login.

Tanpa salah satu, push tidak akan pernah sampai ke device. User juga harus
mengirim heartbeat `PUT /api/location` minimal tiap 2 menit agar masuk radius
penerima (`users:last_seen` yang lebih tua difilter).

Setelah push dibuka, responder memakai `GET /api/sos/inbox` (riwayat insiden
yang pernah dinotifikasi) dan `GET /api/sos/:alertId/media` + `url` media untuk
memutar rekaman.

---

## 6. Skema Database & Evolusi

### Tabel `alerts` (Saat Ini)
```sql
CREATE TABLE alerts (
    id           VARCHAR(100) NOT NULL PRIMARY KEY,
    sender_id    VARCHAR(100) NOT NULL,
    longitude    DOUBLE       NOT NULL,
    latitude     DOUBLE       NOT NULL,
    triggered_at BIGINT       NOT NULL,
    FOREIGN KEY (sender_id) REFERENCES users (id)
);
```

### Migrasi `alerts` v2 (diterapkan di `db/migrations/20261031000000_alerts_v2.*.sql`, termasuk tabel `alert_breadcrumbs`, `emergency_contacts`, `alert_responses`, `alert_media`, dan kolom `safe_pin_hash`/`fcm_token`/`apns_token` di `users`)

```sql
ALTER TABLE alerts
    ADD COLUMN status              VARCHAR(20)  NOT NULL DEFAULT 'TRIGGERED', -- TRIGGERED, ACKNOWLEDGED, RESOLVED, CANCELLED
    ADD COLUMN emergency_type      VARCHAR(30)  NOT NULL DEFAULT 'DANGER',
    ADD COLUMN cancellation_reason VARCHAR(100) NULL,
    ADD COLUMN resolved_at         BIGINT       NULL,
    ADD COLUMN cancelled_at        BIGINT       NULL,
    ADD INDEX idx_alerts_status_triggered_at (status, triggered_at);

CREATE TABLE alert_breadcrumbs (
    id          BIGINT AUTO_INCREMENT PRIMARY KEY,
    alert_id    VARCHAR(100) NOT NULL,
    latitude    DOUBLE       NOT NULL,
    longitude   DOUBLE       NOT NULL,
    speed_mps   FLOAT        NULL,
    heading_deg FLOAT        NULL,
    recorded_at BIGINT       NOT NULL,
    FOREIGN KEY (alert_id) REFERENCES alerts(id) ON DELETE CASCADE,
    INDEX idx_breadcrumbs_alert_time (alert_id, recorded_at)
);

CREATE TABLE emergency_contacts (
    id           VARCHAR(100) NOT NULL PRIMARY KEY,
    user_id      VARCHAR(100) NOT NULL,
    contact_name VARCHAR(100) NOT NULL,
    phone_number VARCHAR(30)  NOT NULL,
    relationship VARCHAR(50)  NOT NULL,
    is_verified  BOOLEAN      NOT NULL DEFAULT FALSE,
    created_at   BIGINT       NOT NULL,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);
```

### Migrasi triage & inbox (`20261101000000_alerts_triage.*.sql`, `20261102000000_alert_recipients.*.sql`)

- `alerts.severity DOUBLE NULL` + `alerts.summary VARCHAR(512) NULL` — hasil AI triage
  ikut tersimpan supaya detail/active/inbox menampilkan data yang sama dengan push.
- `alert_recipients(alert_id, user_id, notified_at)` — daftar user yang dinotifikasi,
  dipakai `GET /api/sos/inbox`; baris dihapus otomatis saat alert dihapus.

### Migrasi lokasi (`20261107000000_locations_user_unique.*.sql`)

- Menambah `UNIQUE KEY uq_locations_user_id (user_id)` pada tabel `locations`.
- `PUT /api/location` meng-upsert satu baris histori per user (koordinat terakhir)
  setelah Redis GEO berhasil ditulis. Kegagalan MySQL hanya di-log dan tidak
  menggagalkan heartbeat.
- Endpoint `GET /api/location/nearby` memakai Redis GEO + `users:last_seen`,
  bukan tabel histori ini.

### Migrasi transkripsi media (`20261108000000_alert_media_transcription.*.sql`)

- Menambah `alert_media.transcript TEXT NULL`,
  `alert_media.transcription_status VARCHAR(20) NOT NULL DEFAULT 'disabled'`, dan
  `alert_media.transcribed_at BIGINT NULL`.
- Dipakai saat upload audio: transcript disimpan bersama media dan hasil triage
  di-update ke tabel `alerts`.

Semua migrasi sudah diterapkan di DB development saat ini (versi
`20261108000000`). Untuk environment lain, jalankan `migrate up`.

---

## 7. Desain Penyimpanan Lokasi

- **Redis GEO** (`users:locations`): Menyimpan geospatial coordinates semua user aktif. Format data: Member = `userId`, Value = `(longitude, latitude)`.
- **Redis Hash** (`users:last_seen`): Key = `userId`, Value = Epoch ms saat `PUT /api/location` terakhir diterima. User yang offline/inaktif $> 2$ menit difilter dari broadcast SOS dan `GET /api/location/nearby` (bukan dihapus).
- **Redis Key Value** (`idempotency:sos:{key}`): TTL 5 menit untuk deduplikasi request pengiriman SOS.
- **Redis Sorted Set** (`ratelimit:sos:{userId}`): sliding window rate limit, max 3 trigger SOS per jam per user. `sos.rate_limit_disabled: true` melewati pemeriksaan ini (idempotency & grace period tetap jalan).
- **Redis Sorted Set** (`ratelimit:auth:{ip}`): sliding window 5 percobaan auth per menit per IP.
- **Redis Key Value** (`lockout:auth:{ip}`): TTL 15 menit setelah limit auth terlampaui.
- **MySQL** (`locations` table): Satu baris histori per user (unique `user_id`), di-upsert pada setiap `PUT /api/location`. Dipakai untuk audit/riwayat, bukan untuk pencarian radius real-time.
- **MySQL** (`alerts` & `alert_breadcrumbs`): Persistence insiden resmi untuk audit dan investigasi forensik.

---

## 8. Non-Functional Requirements & SLA

| Metrik | Target SLA | Mitigasi Kegagalan |
|---|---|---|
| **API Response Time (`POST /api/sos`)** | $p99 < 150\text{ ms}$ | Async event bus; database write didahulukan sebelum push notification. |
| **Notification Fan-out Latency** | $< 2.0\text{ s}$ ke seluruh penerima | Kafka worker pool multi-partition dengan FCM HTTP v1 batch send. |
| **Availability / Uptime** | $99.95\%$ | Multi-AZ deployment, Redis Sentinel/Cluster, stateless Fiber web instances. |
| **Delivery Reliability** | $100\%$ zero-drop untuk Emergency Contacts | Fallback otomatis ke SMS Twilio jika Push notification tidak terkirim dalam 5 detik. |

---

## 9. Panduan Menjalankan Service

### Prasyarat
- Go 1.22+
- MySQL (Aiven / Local)
- Redis 7+
- Kafka (Opsional, disabled by default via config)

`config.json` dan `ca.pem` harus berada di direktori root backend; keduanya
sudah masuk `.gitignore`, jadi jangan pernah commit kredensial aslinya. Mulai
dari template yang di-commit:

```bash
cp config.json.example config.json   # lalu isi kredensial asli
```

`config.json.example` memuat **seluruh** key yang dibaca kode (`app`, `web`,
`log`, `database`, `redis`, `kafka`, `sos`, `auth`, `fcm`, `apns`, `twilio`,
`webpush`, `storage`, `ai`) dengan nilai placeholder. Tambahkan key baru di sana
setiap kali menambah setelan, supaya deploy dari nol tidak kehilangan
konfigurasi.

### WAJIB: `web.proxy_header` di belakang proxy / tunnel

Kalau semua trafik masuk lewat proxy (Cloudflare Tunnel, nginx, Traefik, load
balancer), backend melihat **semua klien datang dari IP proxy yang sama**.
Rate limit auth (§2, §4) dihitung per IP klien, jadi bucket-nya jadi satu
global: **1 user salah login 5 kali → semua user lain ikut kena `429` +
`Retry-After` selama 15 menit.** Set header yang membawa IP klien, lalu restart
service:

```json
"web": {
  "port": 3000,
  "prefork": false,
  "proxy_header": "X-Forwarded-For",
  "trusted_proxies": ["127.0.0.1", "::1"]
}
```

- `cloudflared` dan nginx (`proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;`) mengisi `X-Forwarded-For` dengan IP klien.
- Anti-spoofing: set `web.trusted_proxies` (IP/CIDR proxy yang dipercaya, mis. `[\"127.0.0.1\",\"::1\"]` untuk cloudflared/nginx di host yang sama) supaya `X-Forwarded-For` hanya dipakai kalau request datang dari alamat itu; dari luar daftar itu header diabaikan dan `ctx.IP()` memakai IP TCP asli. Tanpa `trusted_proxies`, header dipakai apa adanya (kompatibel config lama) tapi bisa dipalsukan kalau app bisa diakses langsung — backend log warning saat startup.
- Biarkan `""` untuk dev lokal / saat app diekspos langsung.
- Cek setelah deploy: 5 login gagal dari satu device tidak boleh memblokir device lain, dan key Redis `ratelimit:auth:<ip>` harus memuat IP klien, bukan IP proxy.
- Tanpa section `auth`, limiter memakai default `rate_limit_per_minute: 5` dan `lockout_minutes: 15`.

### Web Push ke browser (VAPID)

Endpoint push alternatif yang **tidak butuh kredensial Firebase**: notifikasi
dikirim langsung ke browser yang login (service worker `frontend/public/sw.js`),
jadi demo tetap jalan tanpa `fcm.project_id`. Jalur FCM v1/APNs/Twilio tidak
berubah; keduanya jalan berdampingan dan masing-masing gagal-terbuka.

```json
"webpush": {
  "enabled": true,
  "vapid_public_key": "<base64url>",
  "vapid_private_key": "<base64url>",
  "subscriber": "sos@allert.my.id",
  "ttl_seconds": 300
}
```

- Generate pasangan key sekali: `webpush.GenerateVAPIDKeys()` (base64url tanpa padding). `private_key` cuma dipakai backend; browser mengambil public key dari `GET /api/push/vapid-public-key`.
- `subscriber` diisi **email polos** atau URL `https://…` — library yang menambahkan prefix `mailto:` sendiri.
- `enabled: false` atau key kosong → kanal dianggap tidak terkonfigurasi (`ErrPushNotConfigured`); dispatcher hanya menulis log dan SOS tetap sukses.
- Alur: user menekan toggle **Push notifications** di Profile (butuh gesture untuk prompt izin) → browser `PushManager.subscribe()` → `POST /api/push/subscriptions` → saat SOS fan-out, `AlertDispatcher` mengenkripsi payload (RFC 8291) dan mengirim per langganan. Endpoint yang dijawab `404`/`410` oleh push service langsung dihapus dari `push_subscriptions`.
- Butuh secure context: `https://` atau `localhost` (alamat LAN `http://` tidak bisa).
- Kalau `kafka.producer.enabled: true`, dispatch pindah ke worker dan worker tidak punya akses database, sehingga web push tidak terkirim — jalankan web push dengan producer Kafka `false`.

### Menjalankan Database Migration
```bash
# Menggunakan migrate CLI
migrate -path db/migrations -database "mysql://user:pass@tcp(host:port)/dbname" up
```

### Menjalankan Server
```bash
# 1. Jalankan HTTP API Server (port 3000)
go run cmd/web/main.go

# 2. Jalankan Worker Kafka (jika Kafka enabled)
go run cmd/worker/main.go

# 3. Build & Vet
go build ./... && go vet ./...
```

---

## 10. Status Implementasi & Yang Belum Live

**CORS**: terkunci ke origin produksi `https://allert.my.id` + dev `http://localhost:5173` (di `internal/config/fiber.go`); header `Authorization` + `X-Idempotency-Key` diizinkan (raw token, tanpa `Bearer`). Origin lain ditolak.

Yang sudah diimplementasi di branch ini (P0 + P1, lihat matriks §2):
state machine, cancel + PIN + grace period 10 detik, query/active API,
idempotency & rate limit, adaptive radius (1/2/5 km, cap 50), WebSocket
tracking + breadcrumbs + telemetri breadcrumb (baterai/speed/heading/altitude/accuracy; flag fall/crash & kualitas jaringan belum), responder & ETA, Kafka producer
`sos.dispatched` + worker consumer, metrik `/metrics`, masking lokasi,
retensi breadcrumb 30 hari, dan klien FCM v1 / APNs Critical Alert /
Twilio SMS dengan fallback log-only saat kredensial belum diisi.

Lokasi juga sudah melayani sisi baca: `GET /api/location` (posisi terakhir +
`stale`) dan `GET /api/location/nearby` (responder aktif di radius tertentu,
koordinat dimask ~100 m, stale > 2 menit difilter). Setiap heartbeat meng-upsert
satu baris histori ke MySQL `locations`. Response `POST /api/sos/:id/acknowledge`
sekarang memakai sudut pandang pemanggil, jadi user di luar insiden tetap menerima
koordinat yang dimask.

Rate limit auth juga aktif: `POST /api/users` dan `POST /api/users/_login`
dibatasi 5 percobaan/menit per IP, lalu lockout 15 menit (`429` +
`Retry-After`). Limiter gagal-terbuka saat Redis error agar login tidak mati.

Feature **AI Triage** udah diimplementasi di branch ini: klasifikasi
deskripsi insiden (`description` → `DANGER/MEDICAL/ACCIDENT`), severity
0–1, dan summary. Provider default = offline keyword classifier
(zero-credential, deterministik); `ai.provider: "ollama"` / `"openai"`
diaktif klien LLM dengan fallback automatic ke offline classifier
(fail-open — AI mati tak pernah nge-blokir trigger SOS). Severity AI
nambah start radius adaptive fan-out (1/2/5 km); `ai.enabled: false`
atum perilakum pre-AI persis (tanpa deskripsi → zero signal, radius
mulai 1 km). LLM sungguhan (Ollama/OpenAI) belum tês pergian — path
offline dites otomatis di unit test.

Audio evidence sudah terhubung ke triage: upload `.webm`/`.m4a`/`.ogg`
≤ 25 MB ditranskrip via provider OpenAI-compatible (default Groq
`whisper-large-v3-turbo`), transcript masuk engine triage, alert
di-update, dan `ALERT_TRIAGE` disiarkan lewat WebSocket. Kalau STT
belum dikonfigurasi/gagal, media tetap tersimpan dan triage awal tidak
berubah.

Feature **Web Push browser (VAPID)** sudah live dan terverifikasi end-to-end:
`POST /api/sos` → `AlertDispatcher` → push terenkripsi (RFC 8291) → push service
browser → `sw.js` menampilkan notifikasi OS, tanpa kredensial Firebase. Langganan
disimpan di tabel `push_subscriptions` (satu baris per browser, `endpoint` unik),
endpoint yang dijawab `404`/`410` dihapus otomatis, dan user tanpa langganan hanya
dicatat di log. Konfigurasi + alur ada di §9.

Yang **belum** terverifikasi live dan butuh kredensial/keputusan produk:
- **FCM/APNs/Twilio**: klien HTTP sudah ada dan dites dengan server
  tiruan, tapi belum pernah mengirim push/SMS sungguhan. Butuh
  `fcm.project_id` + access token OAuth2, `.p8` APNs + bundle id, dan
  SID/token/nomor Twilio, plus device token per user (`fcm_token` /
  `apns_token` via `PATCH /api/users/_current`; tanpa token, FCM
  memakai topic `user_<id>`). Kanal ini terpisah dari Web Push browser
  (§9) yang sudah terbukti mengirim notifikasi sungguhan.
- **PSAP / 911 (RapidSOS/Noonlight)**, **komunikasi anonim WebRTC**,
  **Emergency Medical ID Card**, dan **Geofenced Safe Zones**: belum
  diimplementasi (integrasi eksternal / keputusan produk).
- **Media S3/GCS**: upload tersimpan lokal (`storage.local_dir`);
  backend S3 belum di-wire.
- **OpenTelemetry**: hanya metrik Prometheus manual; tracing OTEL
  belum ditambahkan. **DLQ Kafka** (`sos.dispatched.dlq`) belum di-wire;
  pesan gagal di-redelivery oleh consumer group.

Jalankan semua migrasi (`migrate up`; versi terakhir `20261109000000_push_subscriptions`)
sebelum memakai endpoint baru, dan atur Safe PIN dulu (`PATCH /api/users/_current`,
`{"safe_pin": "1234"}`) agar cancel bisa diverifikasi.

---

## 11. Menjalankan Pengujian (Testing)

Test dibagi mengikuti konvensi package Go: unit/integration test yang membutuhkan
fake atau helper internal tetap berada dekat package yang diuji; test Redis live
lintas package berada di `test/`. Semua test tercakup oleh `go test ./...`, tetapi
package `test` membutuhkan `config.json` dan Redis yang dapat dijangkau.

```bash
# Semua unit/integration test tanpa MySQL, Redis, atau Kafka live:
go test ./internal/... -v -count=1

# Seluruh test, termasuk Redis live:
go test ./... -v -count=1

# Helper distance saja:
go test ./test/ -run '^TestCalculateDistance' -v -count=1

# Build dan pemeriksaan statis:
go build ./... && go vet ./...
```

Coverage utama yang diuji:
- trigger SOS, idempotency, rate limit, adaptive radius, dan error persistence;
- lifecycle alert, PIN, responder/ETA, breadcrumbs, media, dan akses peserta;
- AI triage, dispatch FCM/APNs/Twilio melalui `httptest`, consumer Kafka;
- HTTP SOS end-to-end dengan in-memory stores, WebSocket hub, metrics;
- state machine entity alert (`IsActive` dan `CanTransitionTo`);
- helper geospatial distance dan repository lokasi Redis live.

> `test/` berisi test stateful yang memutasi Redis live (`user-B` dihapus oleh
> `TestDelete`). Jalankan hanya terhadap Redis isolasi/fixture yang sesuai.
Tanpa `config.json`, gunakan `go test ./internal/...`.

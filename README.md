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
| | Redis GEO user positioning (`PUT /api/location`) | ✅ Done | - | High-throughput Redis GEO `GEOADD` (`users:locations`). |
| | Stale user filter (`users:last_seen`) | ✅ Done | - | Redis Hash timestamp tracking; auto-prunes idle users > 2 min. |
| | Dynamic Adaptive Radius | ✅ Done | **P1** | Adapt radius ($500\text{m} - 5\text{km}$) based on urban/rural responder density; limit max recipient fan-out (cap: 50 recipients). |
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
| | Incident Media & Audio Capture | 🟡 In Progress | **P1** | Automatic 10-second ambient audio recording uploaded to S3/GCS (`POST /api/sos/:id/media`) as evidence. |
| **Responder Operations** | Responder Acceptance & ETA | ✅ Done | **P1** | Responders can click "I am coming" (`POST /api/sos/:id/respond`), calculating ETA & live distance to victim. |
| | Anonymized In-Incident Comms | 🔵 Planned | **P2** | Ephemeral, masked chat or WebRTC voice channel between victim and responding volunteers. |
| | Emergency Medical ID Card | 🔵 Planned | **P2** | Victim's blood type, allergies, medications, and emergency contacts accessible only during active SOS. |
| **Safety & Geofencing** | Geofenced Safe Zones | 🔵 Planned | **P2** | Define "Home", "Campus", "Office" polygons; automatic check-in timers (e.g. "Alert if I don't reach home in 30 mins"). |
| | Anti-Abuse & Rate Limiting | ✅ Done | **P0** | Redis sliding-window rate limit (max 3 SOS triggers/hour/user) + spam detection to stop prank/malicious triggers. |
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
| `PUT` | `/api/location` | ✅ | Heartbeat lokasi user ke Redis GEO (`users:locations` & `users:last_seen`) |
| `POST` | `/api/sos` | ✅ | Trigger SOS (body opsional sesuai spek §4, header `X-Idempotency-Key` opsional, rate limit 3/jam) |
| `GET` | `/api/sos/active` | ✅ | Daftar insiden aktif milik user (untuk cold start) |
| `GET` | `/api/sos/:alertId` | ✅ | Detail insiden; lokasi dimask untuk viewer yang bukan sender/responder |
| `POST` | `/api/sos/:alertId/cancel` | ✅ | Batalkan false alarm (PIN 4 digit + alasan) |
| `POST` | `/api/sos/:alertId/acknowledge` | ✅ | `TRIGGERED` → `ACKNOWLEDGED` |
| `POST` | `/api/sos/:alertId/resolve` | ✅ | `TRIGGERED`/`ACKNOWLEDGED` → `RESOLVED` (sender saja) |
| `POST` | `/api/sos/:alertId/respond` | ✅ | Responder menjawab, hitung ETA & jarak |
| `GET/POST` | `/api/sos/:alertId/breadcrumbs` | ✅ | Riwayat breadcrumb / kirim satu titik tracking |
| `POST` | `/api/sos/:alertId/media` | ✅ | Upload media (multipart `file`, audio/video, max 50 MB, penyimpanan lokal) |
| `WS` | `/ws/sos/:alertId/track?token=<token>` | ✅ | Live tracking stream (sender kirim breadcrumb, responder terima `LOCATION_UPDATE`) |
| `GET/POST` | `/api/emergency-contacts` | ✅ | CRUD Tier-1 emergency contacts untuk SMS fallback |
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
  "emergency_type": "DANGER" // DANGER | MEDICAL | ACCIDENT
}
Response 201 Created:
{
  "data": {
    "alert_id": "alt_01HXYZ...",
    "status": "TRIGGERED",
    "grace_period_seconds": 10,
    "recipients_notified": 7,
    "triggered_at": 1730304000000
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
    "sender": { "id": "usr_1", "name": "Jane Doe", "phone": "+62812..." },
    "status": "TRIGGERED",
    "initial_location": { "latitude": -6.2088, "longitude": 106.8456 },
    "latest_location": { "latitude": -6.2091, "longitude": 106.8460, "updated_at": 1730304050000 },
    "responders_en_route": 2
  }
}
```

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

## 5. Skema Database & Evolusi

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

---

## 6. Desain Penyimpanan Lokasi

- **Redis GEO** (`users:locations`): Menyimpan geospatial coordinates semua user aktif. Format data: Member = `userId`, Value = `(longitude, latitude)`.
- **Redis Hash** (`users:last_seen`): Key = `userId`, Value = Epoch ms saat `PUT /api/location` terakhir diterima. Membuang user yang offline/inaktif $> 2$ menit dari broadcast SOS.
- **Redis Key Value** (`idempotency:sos:{key}`): TTL 5 menit untuk deduplikasi request pengiriman SOS.
- **Redis Sorted Set** (`ratelimit:sos:{userId}`): sliding window rate limit, max 3 trigger SOS per jam per user.
- **MySQL** (`locations` table): Historis koordinat stasioner.
- **MySQL** (`alerts` & `alert_breadcrumbs`): Persistence insiden resmi untuk audit dan investigasi forensik.

---

## 7. Non-Functional Requirements & SLA

| Metrik | Target SLA | Mitigasi Kegagalan |
|---|---|---|
| **API Response Time (`POST /api/sos`)** | $p99 < 150\text{ ms}$ | Async event bus; database write didahulukan sebelum push notification. |
| **Notification Fan-out Latency** | $< 2.0\text{ s}$ ke seluruh penerima | Kafka worker pool multi-partition dengan FCM HTTP v1 batch send. |
| **Availability / Uptime** | $99.95\%$ | Multi-AZ deployment, Redis Sentinel/Cluster, stateless Fiber web instances. |
| **Delivery Reliability** | $100\%$ zero-drop untuk Emergency Contacts | Fallback otomatis ke SMS Twilio jika Push notification tidak terkirim dalam 5 detik. |

---

## 8. Panduan Menjalankan Service

### Prasyarat
- Go 1.22+
- MySQL (Aiven / Local)
- Redis 7+
- Kafka (Opsional, disabled by default via config)

File kredensial `config.json` dan `ca.pem` harus berada di direktori root backend (`.gitignore` diaktifkan):
```json
{
  "app": { "name": "sos-backend", "port": 3000 },
  "database": {
    "username": "avnadmin",
    "password": "...",
    "host": "...",
    "port": 18770,
    "database": "defaultdb",
    "ssl": { "ca_cert": "ca.pem" }
  },
  "redis": {
    "host": "...",
    "port": 15164,
    "password": "..."
  },
  "kafka": {
    "producer": { "enabled": false }
  },
  "sos": {
    "grace_period_seconds": 10,
    "rate_limit_per_hour": 3,
    "max_recipients": 50
  },
  "fcm": { "enabled": false, "project_id": "...", "access_token": "..." },
  "apns": {
    "enabled": false, "key_id": "...", "team_id": "...",
    "bundle_id": "...", "private_key_path": "AuthKey.p8", "production": false
  },
  "twilio": {
    "enabled": false, "account_sid": "...",
    "auth_token": "...", "from_number": "+1..."
  },
  "storage": { "backend": "local", "local_dir": "./uploads" }
}
```

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

## 9. Status Implementasi & Yang Belum Live

Yang sudah diimplementasi di branch ini (P0 + P1, lihat matriks §2):
state machine, cancel + PIN + grace period 10 detik, query/active API,
idempotency & rate limit, adaptive radius (1/2/5 km, cap 50), WebSocket
tracking + breadcrumbs + telemetri breadcrumb (baterai/speed/heading/altitude/accuracy; flag fall/crash & kualitas jaringan belum), responder & ETA, Kafka producer
`sos.dispatched` + worker consumer, metrik `/metrics`, masking lokasi,
retensi breadcrumb 30 hari, dan klien FCM v1 / APNs Critical Alert /
Twilio SMS dengan fallback log-only saat kredensial belum diisi.

Yang **belum** terverifikasi live dan butuh kredensial/keputusan produk:
- **FCM/APNs/Twilio**: klien HTTP sudah ada dan dites dengan server
  tiruan, tapi belum pernah mengirim push/SMS sungguhan. Butuh
  `fcm.project_id` + access token OAuth2, `.p8` APNs + bundle id, dan
  SID/token/nomor Twilio, plus device token per user (`fcm_token` /
  `apns_token` via `PATCH /api/users/_current`; tanpa token, FCM
  memakai topic `user_<id>`).
- **PSAP / 911 (RapidSOS/Noonlight)**, **komunikasi anonim WebRTC**,
  **Emergency Medical ID Card**, dan **Geofenced Safe Zones**: belum
  diimplementasi (integrasi eksternal / keputusan produk).
- **Media S3/GCS**: upload tersimpan lokal (`storage.local_dir`);
  backend S3 belum di-wire.
- **OpenTelemetry**: hanya metrik Prometheus manual; tracing OTEL
  belum ditambahkan. **DLQ Kafka** (`sos.dispatched.dlq`) belum di-wire;
  pesan gagal di-redelivery oleh consumer group.

Jalankan migrasi `20261031000000_alerts_v2` sebelum memakai endpoint
baru, dan atur Safe PIN dulu (`PATCH /api/users/_current`,
`{"safe_pin": "1234"}`) agar cancel bisa diverifikasi.

---

## 10. Menjalankan Pengujian (Testing)

```bash
# Unit test SOS (Zero dependency, menggunakan Mock/Fake in-memory):
# trigger, idempotency, rate limit, cancel/PIN, respond/ETA, breadcrumbs,
# adaptive radius, FCM/APNs/Twilio clients (httptest), hub & metrics
go test ./internal/usecase/ ./internal/tracking/ ./internal/metrics/ -v -count=1

# Unit test TriggerSOS (subset lama)
go test ./internal/usecase/ -run '^TestTriggerSOS' -v -count=1

# Helper distance unit tests
go test ./test/ -run '^TestCalculateDistance' -v -count=1

# Full integration test suite (Membutuhkan Redis live sesuai config.json)
go test ./test/ -v -count=1
```

> **Catatan Penting**: Test di dalam `./test/` bersifat stateful dan memutasi data di Redis live (`user-B` dihapus oleh `TestDelete`). Gunakan database isolasi atau unit test mock untuk CI/CD pipeline.

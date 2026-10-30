# SOS Alert — Web (SvelteKit)

Frontend MVP untuk lomba AI Web Innovation Challenge. Frontend ini
hanya menampilkan dan mengirim data: semua panggilan AI terjadi di
backend Go, dan tidak ada kunci AI di browser.

## Menjalankan

```sh
cp .env.example .env
# Atur PUBLIC_API_URL ke alamat backend Go (default http://localhost:3000)
npm install
npm run dev
```

Build produksi (statis, ke `dist/`):

```sh
npm run build
npm run preview
```

## Alur demo

1. Daftar/masuk, isi PIN aman 4 digit, lalu simpan PIN dan lokasi
   (koordinat simulasi default: Canggu).
2. Pilih salah satu contoh laporan, atau tulis/ucapkan laporan sendiri.
3. Tekan "Lihat hasil AI": backend mengembalikan kategori, urgensi,
   ringkasan dua bahasa, dan panduan pertolongan pertama.
4. Koreksi kategori/urgensi bila perlu, lalu "Konfirmasi & kirim SOS".
5. Di bagian responder, muat alert aktif, lihat detail, lalu
   "Saya merespons" dari lokasi simulasi yang sama.

Backend harus jalan dan migrasi `20261031000000_alerts_v2` +
`20261031000001_alerts_ai` sudah diterapkan. Tanpa konfigurasi AI
di backend, triage berjalan dengan classifier aturan (mode
rule-only) dan demo tetap bisa jalan. Halaman memeriksa
`GET /healthz` saat dimuat dan menampilkan status backend serta mode
AI apa adanya, supaya demo tidak berpura-pura memakai model bahasa
saat sedang memakai aturan.

Untuk menjalankan backend secara lokal dari nol, lihat README utama
bagian 8: `docker compose up -d` di root repo, salin
`config.example.json` menjadi `config.json`, terapkan migrasi, lalu
`go run cmd/web/main.go`.

## Desain

Desain mengikuti skill `frontend-design`: satu identitas yang
berakar pada dunia produknya, bukan template umum.

- **Konsep:** konsol sinyal darurat. Yang dibuka pertama adalah
  konsolnya sendiri, bukan halaman pemasaran. Langkah 01 sampai 04
  memang berurutan, jadi penomoran langkah dipakai dengan sengaja.
- **Warna:** Abyss `#081C26` (laut malam), Mist `#EEF3F1`, Signal
  `#E63B1F` (hanya untuk SOS dan urgensi tertinggi), Buoy `#F2A93B`,
  Reef `#0E7C6B` (aman/terhubung).
- **Tipografi:** Barlow Condensed untuk judul gaya rambu, Barlow
  untuk isi. Judul besar memakai huruf kapital sebagai rambu;
  label dan tombol memakai kalimat biasa.
- **Satu momen gerak:** cincin denyut pada panel sinyal saja,
  dimatikan bila pengguna memilih reduced motion. Fokus keyboard
  selalu terlihat.

Naskah demo 4 menit ada di halaman, di bawah bagian responder.

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
rule-only) dan demo tetap bisa jalan.

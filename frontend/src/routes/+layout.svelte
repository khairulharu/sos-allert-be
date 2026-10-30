<script>
  import '../app.css';
  let { children } = $props();
</script>

<header class="top">
  <strong>SOS Alert</strong>
  <span>AI triage darurat untuk Bali — Build with AI, Solve for Bali</span>
</header>

<main>
  {@render children()}
</main>

<footer>
  MVP lomba. AI membantu memahami laporan dan menentukan prioritas, tetapi bukan pengganti
  layanan darurat resmi. Lokasi presisi hanya terlihat oleh pelapor dan responder yang terlibat.
</footer>

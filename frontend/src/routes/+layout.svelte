<script>
  import '../app.css';
  let { children } = $props();
</script>

<header class="masthead">
  <div class="mast-inner">
    <p class="wordmark"><span class="dot" aria-hidden="true"></span>SOS Alert</p>
    <p class="mast-tag">AI triage darurat untuk Bali</p>
  </div>
</header>

<main>
  {@render children()}
</main>

<footer>
  MVP lomba AI Web Innovation Challenge, bidang Bali Smart Society. AI membantu memahami
  laporan dan menentukan prioritas, tetapi bukan pengganti layanan darurat resmi. Lokasi
  presisi hanya terlihat oleh pelapor dan responder yang terlibat.
</footer>

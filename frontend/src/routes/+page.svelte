<script>
  import { onMount } from 'svelte';
  import { api, API_URL } from '../lib/api.js';
  import { DEMO_EXAMPLES, CATEGORIES, CATEGORY_LABELS } from '../lib/demo.js';

  let token = $state('');
  let userId = $state('');
  let userName = $state('');
  let password = $state('');
  let pin = $state('');

  let latitude = $state(-8.6478);
  let longitude = $state(115.1385);
  let areaLabel = $state('Canggu, Bali');
  let message = $state('');

  let triage = $state(null);
  let guide = $state(null);
  let guideLang = $state('id');
  let correctCategory = $state('');
  let correctSeverity = $state(3);

  let created = $state(null);
  let activeAlerts = $state([]);
  let lookupId = $state('');
  let detail = $state(null);
  let busy = $state(false);
  let error = $state('');
  let notice = $state('');
  let listening = $state(false);
  let recognition = null;
  let health = $state({ state: 'checking', aiMode: '' });

  onMount(async () => {
    const saved = localStorage.getItem('sos_token');
    const savedId = localStorage.getItem('sos_user_id');
    if (saved) {
      token = saved;
      userId = savedId || '';
      loadCurrent();
    }
    try {
      const probe = await api.health();
      health = { state: 'ok', aiMode: probe.ai_mode || 'rules' };
    } catch {
      health = { state: 'down', aiMode: '' };
    }
  });

  function saveAuth(t, id, name) {
    token = t;
    userId = id;
    userName = name || '';
    localStorage.setItem('sos_token', t);
    localStorage.setItem('sos_user_id', id);
  }

  async function loadCurrent() {
    try {
      const me = await api.current(token);
      userName = me.name || userName;
    } catch {
      // Token may be stale; the user can log in again.
    }
  }

  function formatTime(ms) {
    if (!ms) return 'Belum ada';
    return new Date(ms).toLocaleString('id-ID', {
      timeZone: 'Asia/Makassar',
      day: 'numeric',
      month: 'short',
      hour: '2-digit',
      minute: '2-digit'
    });
  }

  function mapsLink(lat, lng) {
    return `https://www.google.com/maps/search/?api=1&query=${lat},${lng}`;
  }

  function categoryLabel(code) {
    return CATEGORY_LABELS[code] || code;
  }

  function severityClass(sev) {
    return sev >= 4 ? 'hot' : sev === 3 ? 'warn' : '';
  }

  async function run(fn) {
    error = '';
    notice = '';
    busy = true;
    try {
      await fn();
    } catch (e) {
      error = e.message;
    } finally {
      busy = false;
    }
  }

  async function doRegister() {
    await run(async () => {
      const res = await api.register({ id: userId, password, name: userName });
      saveAuth(res.token, res.id, res.name);
      notice = 'Akun dibuat dan kamu sudah masuk.';
    });
  }

  async function doLogin() {
    await run(async () => {
      const res = await api.login({ id: userId, password });
      saveAuth(res.token, res.id, res.name);
      notice = 'Berhasil masuk.';
    });
  }

  function doLogout() {
    token = '';
    localStorage.removeItem('sos_token');
    localStorage.removeItem('sos_user_id');
    triage = null;
    created = null;
  }

  async function savePinAndLocation() {
    await run(async () => {
      if (pin) await api.updateUser(token, { safe_pin: pin });
      await api.updateLocation(token, { latitude, longitude });
      notice = 'Lokasi tersimpan. PIN aman tersimpan jika diisi, dan dibutuhkan untuk membatalkan SOS.';
    });
  }

  function useExample(ex) {
    message = ex.message;
    areaLabel = ex.area;
    triage = null;
    guide = null;
    created = null;
  }

  function toggleListening() {
    const SR = window.SpeechRecognition || window.webkitSpeechRecognition;
    if (!SR) {
      error = 'Browser ini tidak mendukung input suara. Tulis laporannya saja.';
      return;
    }
    if (listening && recognition) {
      recognition.stop();
      return;
    }
    recognition = new SR();
    recognition.lang = 'id-ID';
    recognition.interimResults = false;
    recognition.onresult = (e) => {
      const text = e.results[0][0].transcript;
      message = message ? `${message} ${text}` : text;
    };
    recognition.onend = () => (listening = false);
    recognition.onerror = () => {
      listening = false;
      error = 'Input suara gagal. Tulis laporannya saja.';
    };
    recognition.start();
    listening = true;
  }

  async function preview() {
    await run(async () => {
      const res = await api.previewTriage(token, { message, area_label: areaLabel });
      triage = res.triage;
      guide = res.guide;
      correctCategory = res.triage.category;
      correctSeverity = res.triage.severity;
      created = null;
      notice = `AI memahami laporan ini sebagai ${categoryLabel(res.triage.category)} dengan urgensi ${res.triage.severity} dari 5. Periksa dan koreksi bila perlu, lalu kirim SOS.`;
    });
  }

  async function confirmSOS() {
    await run(async () => {
      await api.updateLocation(token, { latitude, longitude });
      const payload = {
        latitude,
        longitude,
        message,
        area_label: areaLabel
      };
      if (triage) {
        payload.triage_override = {
          category: correctCategory,
          severity: correctSeverity,
          summary_id: triage.summary_id,
          summary_en: triage.summary_en
        };
      }
      created = await api.triggerSOS(token, payload, crypto.randomUUID());
      lookupId = created.alert_id;
      notice = `SOS terkirim. ${created.recipients_notified} penolong dalam jangkauan diberitahu setelah masa tenggang ${created.grace_period_seconds} detik.`;
    });
  }

  async function loadActive() {
    await run(async () => {
      activeAlerts = await api.listActive(token);
    });
  }

  async function loadDetail() {
    await run(async () => {
      detail = await api.getAlert(token, lookupId.trim());
    });
  }

  async function respond() {
    await run(async () => {
      const res = await api.respond(token, lookupId.trim(), {
        responder_latitude: latitude,
        responder_longitude: longitude
      });
      notice = `Kamu merespons. Jarak ${res.victim_distance_meters} meter, perkiraan tiba ${res.eta_seconds} detik.`;
      await loadDetail();
    });
  }

  async function cancelSOS() {
    await run(async () => {
      await api.cancel(token, lookupId.trim(), { pin, reason: 'FALSE_ALARM' });
      notice = 'SOS dibatalkan.';
      await loadDetail();
    });
  }
</script>

<section class="hero">
  <h1>Bantuan yang mengerti bahasamu.</h1>
  <p>
    Satu kalimat panik, dalam bahasa apa pun, cukup. AI mengubahnya menjadi
    ringkasan dua bahasa dan prioritas yang jelas, lalu meneruskannya ke
    penolong terdekat di sekitarmu di Bali.
  </p>
  <ul class="hero-list">
    <li>Satu kalimat cukup</li>
    <li>Ringkasan Indonesia dan Inggris</li>
    <li>SOS tidak menunggu AI</li>
  </ul>
</section>

<div class="status-strip" aria-live="polite">
  {#if health.state === 'checking'}
    <span class="pill"><span class="pip"></span>Memeriksa backend</span>
  {:else if health.state === 'ok'}
    <span class="pill ok"><span class="pip"></span>Backend terhubung</span>
    <span class="pill">Mode AI: {health.aiMode === 'llm' ? 'Model bahasa' : 'Aturan kata kunci'}</span>
  {:else}
    <span class="pill bad"><span class="pip"></span>Backend belum terhubung</span>
    <span class="muted">Jalankan backend dulu, lalu muat ulang halaman ini.</span>
  {/if}
  <span class="api-url">API: {API_URL}</span>
</div>

<section class="panel">
  <div class="step-head"><span class="step-num" aria-hidden="true">01</span><h2>Akun dan lokasi</h2></div>
  <p class="muted">
    Akun memakai ID dan kata sandi seperti di backend yang sudah ada. Token
    disimpan di browser ini saja. Lokasi di bawah hanya simulasi untuk demo.
  </p>
  <div class="grid">
    <div>
      <label for="uid">ID pengguna</label>
      <input id="uid" bind:value={userId} placeholder="mis. khai" autocomplete="username" />
      <label for="uname">Nama, untuk registrasi</label>
      <input id="uname" bind:value={userName} placeholder="Khairul Aswad" autocomplete="name" />
    </div>
    <div>
      <label for="pwd">Kata sandi</label>
      <input id="pwd" type="password" bind:value={password} autocomplete="current-password" />
      <label for="pin">PIN aman 4 digit, untuk membatalkan SOS</label>
      <input id="pin" inputmode="numeric" maxlength="4" bind:value={pin} placeholder="1234" />
    </div>
  </div>
  <div class="row" style="margin-top: 14px;">
    {#if token}
      <button class="secondary" onclick={doLogout}>Keluar</button>
      <span>Masuk sebagai <strong>{userName || userId}</strong></span>
    {:else}
      <button onclick={doLogin} disabled={busy}>Masuk</button>
      <button class="secondary" onclick={doRegister} disabled={busy}>Daftar akun baru</button>
    {/if}
  </div>
  <div class="grid" style="margin-top: 6px;">
    <div>
      <label for="lat">Latitude</label>
      <input id="lat" type="number" step="0.0001" bind:value={latitude} />
    </div>
    <div>
      <label for="lng">Longitude</label>
      <input id="lng" type="number" step="0.0001" bind:value={longitude} />
    </div>
  </div>
  <label for="area">Label area, kasar. Ini yang boleh dikirim ke AI, bukan koordinat presisi.</label>
  <input id="area" bind:value={areaLabel} />
  <div class="row" style="margin-top: 14px;">
    <button class="secondary" onclick={savePinAndLocation} disabled={!token || busy}>Simpan PIN dan lokasi</button>
    <a href={mapsLink(latitude, longitude)} target="_blank" rel="noreferrer">Buka lokasi di peta</a>
  </div>
</section>

<div aria-live="polite">
  {#if error}<div class="error">{error}</div>{/if}
  {#if notice}<div class="notice">{notice}</div>{/if}
</div>

<section class="panel signal-panel" id="report">
  <span class="beacon" aria-hidden="true"></span>
  <div class="step-head"><span class="step-num" aria-hidden="true">02</span><h2>Laporkan daruratnya</h2></div>
  <p class="muted">
    Tulis atau ucapkan satu dua kalimat saja, dalam bahasa apa pun. AI
    merapikannya di backend. Kalau AI lambat atau gagal, alert tetap terkirim.
  </p>
  <label for="msg">Laporan darurat</label>
  <textarea id="msg" bind:value={message} maxlength="500" placeholder="Contoh: Help! My friend fell off his scooter, leg bleeding..."></textarea>
  <p class="char-count">{message.length}/500 karakter</p>
  <div class="row">
    <button class="secondary" onclick={toggleListening} disabled={busy}>
      {listening ? 'Berhenti mendengarkan' : 'Ucapkan laporan'}
    </button>
    <button onclick={preview} disabled={!token || busy || !message.trim()}>
      {busy ? 'Memproses' : 'Lihat hasil AI'}
    </button>
  </div>

  <h3>Contoh siap pakai untuk demo</h3>
  <div class="grid">
    {#each DEMO_EXAMPLES as ex}
      <button class="example" onclick={() => useExample(ex)}>
        <strong>{ex.label}</strong>
        <span>“{ex.message}”</span>
      </button>
    {/each}
  </div>
</section>

{#if triage}
  <section class="panel result sev-{triage.severity}" aria-live="polite">
    <div class="step-head"><span class="step-num" aria-hidden="true">03</span><h2>Hasil AI, periksa dulu</h2></div>
    <div class="severity-row">
      <p class="severity-num">{triage.severity}<small>/5</small></p>
      <div>
        <p style="margin: 0 0 6px; font-size: 20px; font-weight: 700;">{categoryLabel(triage.category)}</p>
        <p style="margin: 0;">
          <span class="badge {severityClass(triage.severity)}">Urgensi {triage.severity} dari 5</span>
          <span class="badge">Keyakinan {Math.round(triage.confidence * 100)}%</span>
          <span class="badge">Sumber: {triage.source === 'llm' ? 'Model bahasa' : triage.source === 'override' ? 'Koreksimu' : 'Aturan kata kunci'}</span>
          {#if triage.needs_clarification}<span class="badge hot">Laporan kurang jelas, urgensi dinaikkan</span>{/if}
        </p>
      </div>
    </div>

    <div class="grid" style="margin-top: 12px;">
      <div>
        <h3>Ringkasan, Indonesia</h3>
        <p>{triage.summary_id}</p>
        <h3>Summary, English</h3>
        <p>{triage.summary_en}</p>
      </div>
      <div>
        <h3>Teks asli pelapor</h3>
        <p class="quote">“{triage.original_text}”</p>
        {#if triage.keywords?.length}<p class="muted">Kata kunci: {triage.keywords.join(', ')}</p>{/if}
        {#if triage.needs?.length}<p class="muted">Kebutuhan: {triage.needs.join(', ')}</p>{/if}
        <p class="muted">Teks asli selalu ditampilkan ke responder, jadi tidak ada yang hilang dalam terjemahan.</p>
      </div>
    </div>

    {#if guide}
      <h3>{guideLang === 'id' ? guide.title_id : guide.title_en}</h3>
      <div class="row" role="group" aria-label="Bahasa panduan">
        <button class="secondary" onclick={() => (guideLang = 'id')} aria-pressed={guideLang === 'id'}>Indonesia</button>
        <button class="secondary" onclick={() => (guideLang = 'en')} aria-pressed={guideLang === 'en'}>English</button>
      </div>
      <ol class="steps">
        {#each guideLang === 'id' ? guide.steps_id : guide.steps_en as step}
          <li>{step}</li>
        {/each}
      </ol>
      <p class="disclaimer">{guideLang === 'id' ? guide.disclaimer_id : guide.disclaimer_en}</p>
    {/if}

    <div class="grid">
      <div>
        <label for="cat">Koreksi kategori, jika AI salah</label>
        <select id="cat" bind:value={correctCategory}>
          {#each CATEGORIES as c}<option value={c}>{categoryLabel(c)}</option>{/each}
        </select>
      </div>
      <div>
        <label for="sev">Koreksi urgensi, 1 sampai 5</label>
        <input id="sev" type="number" min="1" max="5" bind:value={correctSeverity} />
      </div>
    </div>
    <div class="row" style="margin-top: 16px;">
      <button class="danger big" onclick={confirmSOS} disabled={!token || busy}>
        {busy ? 'Mengirim' : 'Kirim SOS sekarang'}
      </button>
    </div>
    <p class="muted">Mengirim dengan hasil yang sudah kamu konfirmasi di atas.</p>
  </section>
{/if}

{#if created}
  <section class="panel" aria-live="polite">
    <h2>SOS terkirim</h2>
    <dl class="facts">
      <div><dt>ID alert</dt><dd class="alert-id">{created.alert_id}</dd></div>
      <div><dt>Status</dt><dd>{created.status}</dd></div>
      <div><dt>Penolong diberitahu</dt><dd>{created.recipients_notified} orang dalam jangkauan</dd></div>
      <div><dt>Masa tenggang</dt><dd>{created.grace_period_seconds} detik sebelum notifikasi disebar</dd></div>
      <div><dt>Dikirim</dt><dd>{formatTime(created.triggered_at)}</dd></div>
      {#if created.triage}
        <div><dt>Hasil AI</dt><dd>{categoryLabel(created.triage.category)}, urgensi {created.triage.severity} dari 5 ({created.triage.source})</dd></div>
      {/if}
    </dl>
    <div class="row">
      <a href={mapsLink(latitude, longitude)} target="_blank" rel="noreferrer">Buka lokasi di peta</a>
    </div>
  </section>
{/if}

<section class="panel">
  <div class="step-head"><span class="step-num" aria-hidden="true">04</span><h2>Sisi responder</h2></div>
  <p class="muted">
    Daftar di bawah menampilkan alert aktif milik akun yang masuk. Untuk
    melihat alert dari akun lain, masukkan ID alert-nya. Lokasi pada detail
    disamarkan untuk yang bukan pelapor atau responder.
  </p>
  <div class="row">
    <button class="secondary" onclick={loadActive} disabled={!token || busy}>Muat alert aktif saya</button>
    <input style="max-width: 320px;" bind:value={lookupId} placeholder="ID alert, dari hasil SOS" aria-label="ID alert" />
    <button class="secondary" onclick={loadDetail} disabled={!token || busy || !lookupId.trim()}>Lihat detail</button>
    <button onclick={respond} disabled={!token || busy || !lookupId.trim()}>Saya merespons</button>
    <button class="secondary" onclick={cancelSOS} disabled={!token || busy || !lookupId.trim() || !pin}>Batalkan, pakai PIN</button>
  </div>
  {#if activeAlerts.length}
    <ul class="active-list">
      {#each activeAlerts as a}
        <li>
          <button class="secondary" onclick={() => { lookupId = a.alert_id || a.id; loadDetail(); }}>
            {a.alert_id || a.id}
          </button>
          <span> {a.emergency_type}, status {a.status}</span>
          {#if a.triage}<span class="muted">, AI: {categoryLabel(a.triage.category)}, urgensi {a.triage.severity} dari 5</span>{/if}
        </li>
      {/each}
    </ul>
  {/if}
  {#if detail}
    <dl class="facts" style="margin-top: 16px;">
      <div><dt>ID alert</dt><dd class="alert-id">{detail.alert_id}</dd></div>
      <div><dt>Status</dt><dd>{detail.status}</dd></div>
      <div><dt>Jenis darurat</dt><dd>{detail.emergency_type}</dd></div>
      <div><dt>Pelapor</dt><dd>{detail.sender?.name || detail.sender?.id || 'Tidak diketahui'}</dd></div>
      <div><dt>Lokasi awal</dt><dd>{detail.initial_location?.latitude}, {detail.initial_location?.longitude}{#if detail.masked} (disamarkan){/if}</dd></div>
      <div><dt>Lokasi terakhir</dt><dd>{detail.latest_location?.latitude}, {detail.latest_location?.longitude}{#if detail.masked} (disamarkan){/if}</dd></div>
      <div><dt>Responder di jalan</dt><dd>{detail.responders_en_route} orang</dd></div>
      <div><dt>Dikirim</dt><dd>{formatTime(detail.triggered_at)}</dd></div>
      {#if detail.triage}
        <div><dt>Hasil AI</dt><dd>{categoryLabel(detail.triage.category)}, urgensi {detail.triage.severity} dari 5. {detail.triage.summary_id}</dd></div>
      {/if}
      {#if detail.original_message}
        <div><dt>Laporan asli</dt><dd>“{detail.original_message}”</dd></div>
      {/if}
    </dl>
    {#if detail.guide}
      <h3>Panduan untuk responder</h3>
      <ol class="steps">
        {#each detail.guide.steps_id as step}<li>{step}</li>{/each}
      </ol>
    {/if}
  {/if}
</section>

<details class="demo-script">
  <summary>Naskah demo 4 menit untuk juri</summary>
  <ol>
    <li><strong>Menit 0 sampai 1.</strong> Buka halaman, tunjukkan status backend dan mode AI. Jelaskan masalahnya dalam satu kalimat: turis panik tidak bisa menjelaskan darurat dalam bahasa lokal.</li>
    <li><strong>Menit 1 sampai 2.</strong> Pilih contoh turis Inggris, tekan Lihat hasil AI. Tunjukkan urgensi, ringkasan dua bahasa, dan panduan pertolongan pertama.</li>
    <li><strong>Menit 2 sampai 3.</strong> Koreksi kategorinya, kirim SOS, lalu tunjukkan detail alert dari sisi responder dan tombol Saya merespons.</li>
    <li><strong>Menit 3 sampai 4.</strong> Pilih contoh ambigu “sakit banget, tolong”. Tunjukkan AI menaikkan urgensi karena laporannya kurang jelas, dan jelaskan SOS tidak pernah menunggu AI.</li>
  </ol>
</details>

<section class="panel">
  <h2>Cara kerjanya</h2>
  <ol class="steps">
    <li><strong>Laporan panik</strong> ditulis atau diucapkan, dalam bahasa apa pun, beserta lokasi.</li>
    <li><strong>AI triage di backend</strong> mengubahnya menjadi kategori, urgensi, dan ringkasan dua bahasa. Tanpa AI, triage aturan tetap jalan dan SOS tidak tertunda.</li>
    <li><strong>Dispatch cerdas</strong> menentukan radius dan jumlah penerima dari urgensinya, lalu responder menerima ringkasan dalam bahasa mereka.</li>
  </ol>
</section>

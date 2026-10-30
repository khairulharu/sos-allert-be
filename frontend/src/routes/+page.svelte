<script>
  import { onMount } from 'svelte';
  import { api, API_URL } from '../lib/api.js';
  import { DEMO_EXAMPLES, CATEGORIES } from '../lib/demo.js';

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

  onMount(() => {
    const saved = localStorage.getItem('sos_token');
    const savedId = localStorage.getItem('sos_user_id');
    if (saved) {
      token = saved;
      userId = savedId || '';
      loadCurrent();
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
      notice = 'Lokasi tersimpan. PIN aman tersimpan jika diisi (dibutuhkan untuk membatalkan SOS).';
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
      notice = `AI memahami laporan ini sebagai ${res.triage.category} (urgensi ${res.triage.severity}/5, sumber: ${res.triage.source}). Periksa dan koreksi bila perlu, lalu konfirmasi SOS.`;
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
      notice = `SOS terkirim. Alert ${created.alert_id} — ${created.recipients_notified} penerima dalam jangkauan. Masa tenggang ${created.grace_period_seconds} detik berlaku sebelum notifikasi disebar.`;
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
      notice = `Kamu merespons. Jarak ${res.victim_distance_meters} m, perkiraan tiba ${res.eta_seconds} detik.`;
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

<section class="card">
  <h2>1. Masuk dulu</h2>
  <p class="muted">
    Backend: <code>{API_URL}</code>. Akun memakai ID dan kata sandi seperti di backend yang sudah ada.
    Token disimpan di browser ini saja.
  </p>
  <div class="grid">
    <div>
      <label for="uid">ID pengguna</label>
      <input id="uid" bind:value={userId} placeholder="mis. khai" />
      <label for="uname">Nama (untuk registrasi)</label>
      <input id="uname" bind:value={userName} placeholder="Khairul Aswad" />
    </div>
    <div>
      <label for="pwd">Kata sandi</label>
      <input id="pwd" type="password" bind:value={password} />
      <label for="pin">PIN aman 4 digit (untuk batal SOS)</label>
      <input id="pin" inputmode="numeric" maxlength="4" bind:value={pin} placeholder="1234" />
    </div>
  </div>
  <div class="row" style="margin-top: 12px;">
    {#if token}
      <button class="secondary" onclick={doLogout}>Keluar</button>
      <span>Masuk sebagai <strong>{userName || userId}</strong></span>
    {:else}
      <button onclick={doLogin} disabled={busy}>Masuk</button>
      <button class="secondary" onclick={doRegister} disabled={busy}>Daftar akun baru</button>
    {/if}
  </div>
</section>

{#if error}<div class="error">{error}</div>{/if}
{#if notice}<div class="notice">{notice}</div>{/if}

<section class="card">
  <h2>2. Ceritakan daruratnya, AI yang merapikan</h2>
  <p class="muted">
    Tulis atau ucapkan satu dua kalimat saja, dalam bahasa apa pun. Lokasi terisi otomatis dari
    koordinat di bawah. AI mengubah laporan panik menjadi ringkasan dua bahasa dan menentukan
    prioritasnya. SOS tidak menunggu AI: kalau AI lambat, alert tetap terkirim.
  </p>

  <div class="grid">
    <div>
      <label for="lat">Latitude (simulasi lokasi)</label>
      <input id="lat" type="number" step="0.0001" bind:value={latitude} />
      <label for="lng">Longitude (simulasi lokasi)</label>
      <input id="lng" type="number" step="0.0001" bind:value={longitude} />
      <label for="area">Label area (kasar, ini yang boleh dikirim ke AI)</label>
      <input id="area" bind:value={areaLabel} />
      <div class="row" style="margin-top: 12px;">
        <button class="secondary" onclick={savePinAndLocation} disabled={!token || busy}>Simpan PIN &amp; lokasi</button>
      </div>
    </div>
    <div>
      <label for="msg">Laporan darurat</label>
      <textarea id="msg" bind:value={message} placeholder="Contoh: Help! My friend fell off his scooter, leg bleeding..."></textarea>
      <div class="row">
        <button class="secondary" onclick={toggleListening} disabled={busy}>
          {listening ? 'Berhenti mendengarkan…' : '🎤 Ucapkan laporan'}
        </button>
        <button onclick={preview} disabled={!token || busy || !message.trim()}>Lihat hasil AI</button>
      </div>
    </div>
  </div>

  <h3>Contoh siap pakai untuk demo</h3>
  <div class="grid">
    {#each DEMO_EXAMPLES as ex}
      <button class="example" onclick={() => useExample(ex)}>
        <strong>{ex.label}</strong>
        “{ex.message}”
      </button>
    {/each}
  </div>
</section>

{#if triage}
  <section class="card">
    <h2>3. Hasil AI — periksa, koreksi, lalu konfirmasi</h2>
    <p>
      <span class="badge">{triage.category}</span>
      <span class="badge sev{triage.severity}">Urgensi {triage.severity}/5</span>
      <span class="badge">Keyakinan {Math.round(triage.confidence * 100)}%</span>
      <span class="badge">Sumber: {triage.source}</span>
      {#if triage.needs_clarification}<span class="badge sev4">Perlu klarifikasi — urgensi dinaikkan</span>{/if}
    </p>
    <div class="grid">
      <div>
        <h3>Ringkasan (Indonesia)</h3>
        <p>{triage.summary_id}</p>
        <h3>Summary (English)</h3>
        <p>{triage.summary_en}</p>
      </div>
      <div>
        <p class="muted">Teks asli pelapor, selalu ditampilkan ke responder:</p>
        <p>“{triage.original_text}”</p>
        {#if triage.keywords?.length}<p class="muted">Kata kunci: {triage.keywords.join(', ')}</p>{/if}
        {#if triage.needs?.length}<p class="muted">Kebutuhan: {triage.needs.join(', ')}</p>{/if}
      </div>
    </div>

    {#if guide}
      <h3>Panduan pertolongan pertama — {guideLang === 'id' ? guide.title_id : guide.title_en}</h3>
      <div class="row">
        <button class="secondary" onclick={() => (guideLang = 'id')}>Indonesia</button>
        <button class="secondary" onclick={() => (guideLang = 'en')}>English</button>
      </div>
      <ol class="steps">
        {#each guideLang === 'id' ? guide.steps_id : guide.steps_en as step}
          <li>{step}</li>
        {/each}
      </ol>
      <p class="muted">{guideLang === 'id' ? guide.disclaimer_id : guide.disclaimer_en}</p>
    {/if}

    <div class="grid">
      <div>
        <label for="cat">Koreksi kategori (jika AI salah)</label>
        <select id="cat" bind:value={correctCategory}>
          {#each CATEGORIES as c}<option value={c}>{c}</option>{/each}
        </select>
      </div>
      <div>
        <label for="sev">Koreksi urgensi (1–5)</label>
        <input id="sev" type="number" min="1" max="5" bind:value={correctSeverity} />
      </div>
    </div>
    <div class="row" style="margin-top: 12px;">
      <button class="danger" onclick={confirmSOS} disabled={!token || busy}>Konfirmasi &amp; kirim SOS</button>
      <span class="muted">Mengirim dengan hasil yang sudah kamu konfirmasi.</span>
    </div>
  </section>
{/if}

{#if created}
  <section class="card">
    <h2>SOS terkirim</h2>
    <pre>{JSON.stringify(created, null, 2)}</pre>
  </section>
{/if}

<section class="card">
  <h2>4. Sisi responder</h2>
  <p class="muted">
    Daftar di bawah menampilkan alert aktif milik akun yang masuk. Untuk melihat alert dari akun
    lain, masukkan ID alert-nya. Lokasi pada detail dimasking untuk yang bukan pelapor/responder.
  </p>
  <div class="row">
    <button class="secondary" onclick={loadActive} disabled={!token || busy}>Muat alert aktif saya</button>
    <input style="max-width: 320px;" bind:value={lookupId} placeholder="ID alert, mis. dari hasil SOS" />
    <button class="secondary" onclick={loadDetail} disabled={!token || busy || !lookupId.trim()}>Lihat detail</button>
    <button onclick={respond} disabled={!token || busy || !lookupId.trim()}>Saya merespons</button>
    <button class="secondary" onclick={cancelSOS} disabled={!token || busy || !lookupId.trim() || !pin}>Batalkan (pakai PIN)</button>
  </div>
  {#if activeAlerts.length}
    <ul>
      {#each activeAlerts as a}
        <li>
          <button class="secondary" onclick={() => { lookupId = a.alert_id; loadDetail(); }}>
            {a.alert_id}
          </button>
          {a.emergency_type} — {a.status}
          {#if a.triage}· AI: {a.triage.category} (urgensi {a.triage.severity}/5, {a.triage.source}){/if}
        </li>
      {/each}
    </ul>
  {/if}
  {#if detail}
    <pre>{JSON.stringify(detail, null, 2)}</pre>
  {/if}
</section>

<section class="card">
  <h2>Cara kerjanya</h2>
  <ol>
    <li><strong>Laporan panik</strong> ditulis atau diucapkan, dalam bahasa apa pun, beserta lokasi.</li>
    <li><strong>AI triage di backend</strong> mengubahnya menjadi kategori, urgensi, dan ringkasan dua bahasa. Tanpa AI, triage aturan tetap jalan dan SOS tidak tertunda.</li>
    <li><strong>Dispatch cerdas</strong> menentukan radius dan jumlah penerima dari urgensinya, lalu responder menerima ringkasan dalam bahasa mereka.</li>
  </ol>
</section>

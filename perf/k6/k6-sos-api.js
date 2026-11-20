// SOS-Allert API — k6 load test (read-only, authenticated)
//
//   BASE_URL   default https://api.allert.my.id
//   SOS_TOKEN  optional: reuse an existing session token (skips login)
//   SOS_USER / SOS_PW  login account (omit both when SOS_TOKEN is set)
//
// Usage:
//   K6_WEB_DASHBOARD=true K6_WEB_DASHBOARD_EXPORT=report.html \
//   SOS_USER=<account> SOS_PW=<password> \
//   k6 run --out json=results.json k6-sos-api.js
//
// NOTE: hits production READ-ONLY routes. Never adds /api/sos (trigger),
// /api/sos/classify (spends STT+LLM tokens) or any write route.

import http from 'k6/http';
import { check } from 'k6';
import { Counter, Rate, Trend } from 'k6/metrics';

const BASE_URL = __ENV.BASE_URL || 'https://api.allert.my.id';

// Weighted read mix — mirrors what an active client polls.
const ENDPOINTS = [
  { name: 'GET /api/users/_current',       path: '/api/users/_current',       weight: 15 },
  { name: 'GET /api/contacts',             path: '/api/contacts',             weight: 15 },
  { name: 'GET /api/location',             path: '/api/location',             weight: 12 },
  { name: 'GET /api/location/nearby',      path: '/api/location/nearby',      weight: 10 },
  { name: 'GET /api/sos/active',           path: '/api/sos/active',           weight: 10 },
  { name: 'GET /api/sos/history',          path: '/api/sos/history',          weight: 10 },
  { name: 'GET /api/sos/summary',          path: '/api/sos/summary',          weight: 8 },
  { name: 'GET /api/sos/inbox',            path: '/api/sos/inbox',            weight: 6 },
  { name: 'GET /api/emergency-contacts',   path: '/api/emergency-contacts',   weight: 6 },
  { name: 'GET /api/push/vapid-public-key', path: '/api/push/vapid-public-key', weight: 4 },
  { name: 'GET /api/push/subscriptions',   path: '/api/push/subscriptions',   weight: 4 },
];
const TOTAL_WEIGHT = ENDPOINTS.reduce((a, e) => a + e.weight, 0);
const SMOKE = __ENV.SMOKE === '1';

// One Trend per endpoint: k6 does not emit `name:`-tagged submetrics for
// custom tags, so per-endpoint numbers only exist if each endpoint owns a
// metric of its own.
const EP_TRENDS = {};
for (const e of ENDPOINTS) {
  const key = 'ep_' + e.path.replace(/^\//, '').replace(/[^a-z0-9]+/gi, '_');
  EP_TRENDS[e.path] = new Trend(key, true);
}
const epFail = new Rate('ep_failures');
const checksTotal = new Counter('checks_total');

export const options = {
  scenarios: {
    api_read_mix: {
      executor: 'ramping-arrival-rate',
      exec: 'readMix',
      startRate: 2,
      timeUnit: '1s',
      stages: SMOKE
        ? [{ target: 2, duration: '5s' }, { target: 2, duration: '5s' }]
        : [
            { target: 5, duration: '20s' },   // warm-up
            { target: 10, duration: '30s' },  // ramp
            { target: 10, duration: '40s' },  // steady state
            { target: 0, duration: '10s' },   // cool-down
          ],
      preAllocatedVUs: 10,
      maxVUs: 30,
      gracefulStop: '10s',
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.02'],                    // <2% non-2xx
    http_req_duration: ['p(95)<2500', 'p(99)<4000'],   // incl. Cloudflare hop
    checks: ['rate>0.98'],
  },
  summaryTrendStats: ['avg', 'min', 'med', 'p(90)', 'p(95)', 'p(99)', 'max', 'count'],
  discardResponseBodies: true,
  noConnectionReuse: false,
};

// One login for the whole run: the auth limiter allows 5/min per IP,
// so every VU must reuse the token instead of logging in.
export function setup() {
  if (__ENV.SOS_TOKEN) {
    return { token: __ENV.SOS_TOKEN, via: 'env' };
  }
  const id = __ENV.SOS_USER || '';
  const pw = __ENV.SOS_PW || '';
  if (!pw) {
    throw new Error('SOS_USER + SOS_PW (or SOS_TOKEN) are required: /api/users/_login is rate limited');
  }
  const res = http.post(
    `${BASE_URL}/api/users/_login`,
    JSON.stringify({ id, password: pw }),
    // responseType text: the global discardResponseBodies would null the body
    { headers: { 'Content-Type': 'application/json' }, responseType: 'text', tags: { name: 'POST /api/users/_login' } },
  );
  if (res.status !== 200) {
    throw new Error(`login failed: HTTP ${res.status} — ${String(res.body).slice(0, 200)}`);
  }
  let parsed = {};
  try { parsed = JSON.parse(res.body); } catch (e) { /* fall through */ }
  const token = ((parsed.data) || {}).token || '';
  if (!token) throw new Error('login ok but no token in response');
  return { token, via: 'login' };
}

function pickEndpoint() {
  let r = Math.random() * TOTAL_WEIGHT;
  for (const e of ENDPOINTS) {
    r -= e.weight;
    if (r <= 0) return e;
  }
  return ENDPOINTS[0];
}

export function readMix(data) {
  const ep = pickEndpoint();
  const headers = { Authorization: data.token }; // raw token, no "Bearer" prefix
  const res = http.get(`${BASE_URL}${ep.path}`, {
    headers,
    tags: { name: ep.name, endpoint: ep.path },
  });

  const ok = check(res, {
    [`${ep.name} → 200`]: (r) => r.status === 200,
    [`${ep.name} → < 3s`]: (r) => r.timings.duration < 3000,
  });

  EP_TRENDS[ep.path].add(res.timings.duration);
  epFail.add(!ok, { endpoint: ep.path });
  checksTotal.add(1);
}

// Writes summary.json (fed to build-dashboard.py) plus a compact console table.
export function handleSummary(data) {
  const m = data.metrics;
  const keyOf = (p) => 'ep_' + p.replace(/^\//, '').replace(/[^a-z0-9]+/gi, '_');

  const rows = ENDPOINTS.map((e) => {
    const s = (m[keyOf(e.path)] || {}).values;
    return s ? [e.name, s.count, s.avg, s['p(95)'], s['p(99)'], s.max] : null;
  }).filter(Boolean);

  const pad = (v, w) => String(Math.round(v)).padStart(w);
  let out = '\nEndpoint latency (ms) — ' + BASE_URL + '\n';
  out += 'endpoint                                 reqs   avg   p95   p99   max\n';
  for (const r of rows) {
    out += `${r[0].padEnd(40)} ${pad(r[1], 6)} ${pad(r[2], 5)} ${pad(r[3], 5)} ${pad(r[4], 5)} ${pad(r[5], 5)}\n`;
  }
  const o = m.http_req_duration.values;
  out += `\nOverall: ${o.count} reqs, avg ${Math.round(o.avg)}ms, p95 ${Math.round(o['p(95)'])}ms, ` +
    `p99 ${Math.round(o['p(99)'])}ms, failed ${(m.http_req_failed.values.rate * 100).toFixed(2)}%, ` +
    `iterations ${m.iterations.values.count}, max VUs ${m.vus_max.values.max}\n`;

  // setup_data carries the session token — never let it reach summary.json
  // (these artifacts get committed / shared).
  const safe = JSON.parse(JSON.stringify(data));
  delete safe.setup_data;

  return {
    stdout: out,
    [__ENV.SUMMARY_FILE || 'summary.json']: JSON.stringify(safe, null, 2),
  };
}

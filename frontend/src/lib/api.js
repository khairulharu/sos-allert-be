// API client for the Go backend. The browser never talks to an AI
// provider directly and never holds an AI key: every AI call goes
// through the backend (POST /api/ai/triage, POST /api/sos). Auth is
// the backend's raw token in the Authorization header (no Bearer).
import { env } from '$env/dynamic/public';

export const API_URL = env.PUBLIC_API_URL || 'http://localhost:3000';

async function request(path, token, options = {}) {
  const res = await fetch(`${API_URL}${path}`, {
    ...options,
    headers: {
      'Content-Type': 'application/json',
      ...(token ? { Authorization: token } : {}),
      ...(options.headers || {})
    }
  });
  const body = await res.json().catch(() => ({}));
  if (!res.ok) {
    throw new Error(body.errors || body.error || `Request failed (${res.status})`);
  }
  return body.data;
}

export const api = {
  // Unauthenticated probe. The frontend uses it to say, in plain
  // words, whether the backend is reachable and which triage mode
  // is active (llm or rules). It returns the probe payload as-is,
  // not a wrapped `data` object.
  health: async () => {
    const res = await fetch(`${API_URL}/healthz`);
    if (!res.ok) throw new Error(`Backend tidak merespons (${res.status})`);
    return res.json();
  },
  register: (payload) => request('/api/users', null, { method: 'POST', body: JSON.stringify(payload) }),
  login: (payload) => request('/api/users/_login', null, { method: 'POST', body: JSON.stringify(payload) }),
  current: (token) => request('/api/users/_current', token),
  updateUser: (token, payload) =>
    request('/api/users/_current', token, { method: 'PATCH', body: JSON.stringify(payload) }),
  updateLocation: (token, payload) =>
    request('/api/location', token, { method: 'PUT', body: JSON.stringify(payload) }),
  previewTriage: (token, payload) =>
    request('/api/ai/triage', token, { method: 'POST', body: JSON.stringify(payload) }),
  triggerSOS: (token, payload, idempotencyKey) =>
    request('/api/sos', token, {
      method: 'POST',
      body: JSON.stringify(payload),
      headers: idempotencyKey ? { 'X-Idempotency-Key': idempotencyKey } : {}
    }),
  listActive: (token) => request('/api/sos/active', token),
  getAlert: (token, alertId) => request(`/api/sos/${alertId}`, token),
  respond: (token, alertId, payload) =>
    request(`/api/sos/${alertId}/respond`, token, { method: 'POST', body: JSON.stringify(payload) }),
  cancel: (token, alertId, payload) =>
    request(`/api/sos/${alertId}/cancel`, token, { method: 'POST', body: JSON.stringify(payload) })
};

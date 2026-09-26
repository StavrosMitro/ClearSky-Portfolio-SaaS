// front-end/public/api/_request.js

// NOTE: in your browser, "orchestrator" isn't a DNS name.
// Use localhost:8080 (or adjust if you run the orchestrator elsewhere).
const API_BASE = 'http://localhost:8080';

/**
 * Generic request helper that:
 *  • automatically JSON‐stringifies objects (except GETs, which become query strings)
 *  • sends FormData unchanged
 *  • sends the HttpOnly session cookie
 *  • unwraps the public `{ data, request_id }` response envelope
 */
export async function request(path, { method = 'GET', body, headers } = {}) {
  // build full URL
  let url = API_BASE + path;
  const opts = { method, credentials: 'include', headers: { ...headers } };

  // If GET + plain object, turn into query string
  if (method.toUpperCase() === 'GET' && body && !(body instanceof FormData)) {
    const qs = new URLSearchParams(body).toString();
    url += (url.includes('?') ? '&' : '?') + qs;
  }
  // If it's FormData, send as-is
  else if (body instanceof FormData) {
    opts.body = body;
  }
  // Otherwise (non-GET JSON payload)
  else if (body !== undefined) {
    opts.body = JSON.stringify(body);
    opts.headers = { 'Content-Type': 'application/json', ...opts.headers };
  }

  const res = await fetch(url, opts);
  const json = await res.json().catch(() => ({}));

  if (!res.ok) {
    const error = new Error(json.error?.message || res.statusText || 'Request failed');
    error.code = json.error?.code;
    error.requestId = json.request_id;
    throw error;
  }
  return json.data;
}

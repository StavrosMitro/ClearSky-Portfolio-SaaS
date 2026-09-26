// public/api/stats.js
import { request } from './_request.js';

/**
 * GET /stats/available
 * returns either { data: […] } or […] directly
 */
export async function getAvailableStats() {
  return request('/stats/available');
}

/**
 * POST /stats/distributions
 * again, unwrap .data if present
 */
export async function getDistributions({ course, declarationPeriod, classTitle }) {
  return request('/stats/distributions', {
    method: 'POST',
    body: { course, declarationPeriod, classTitle }
  });
}

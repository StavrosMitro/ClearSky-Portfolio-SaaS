// public/api/stats.js
import { request } from './_request.js';

/** Gradings the signed-in user may see (SRS 2.6). */
export const getAvailableStats = () => request('/stats/available');

/** Precomputed charts of one grading: { grading, distributions: { grade, Q1, … } }. */
export const getDistributions = gradingId =>
  request(`/stats/gradings/${encodeURIComponent(gradingId)}/distributions`);

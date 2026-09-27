// public/api/grades.js – posting grade workbooks (SRS 2.5, 2.10)
import { request } from './_request.js';

/** Upload a workbook; returns the preview to confirm or cancel. */
export const uploadWorkbook = (kind, file) => {
  const fd = new FormData();
  fd.append('kind', kind);
  fd.append('file', file);
  return request('/grades/uploads', { method: 'POST', body: fd });
};

export const confirmUpload = id =>
  request(`/grades/uploads/${encodeURIComponent(id)}/confirm`, { method: 'POST' });

export const cancelUpload = id =>
  request(`/grades/uploads/${encodeURIComponent(id)}/cancel`, { method: 'POST' });

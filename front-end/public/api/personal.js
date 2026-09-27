// front-end/public/api/personal.js
import { request } from './_request.js';

/** The student's grades: one entry per grading (SRS 2.7). */
export const getMyGrades = async () => {
  const data = await request('/personal/grades');
  return Array.isArray(data) ? data : [];
};

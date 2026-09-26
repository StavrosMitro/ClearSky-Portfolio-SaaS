// front-end/public/api/personal.js
import { request } from './_request.js';

/**
 * Fetch all past/future courses & periods for the logged-in student.
 * Orchestrator: GET /personal/grades → { status, data: […] }
 */
export const getStudentCourses = async () => {
  const data = await request('/personal/grades');
  return Array.isArray(data) ? data : [];
};

/**
 * Fetch the grade entries for a given course & exam period.
 * Orchestrator: GET /personal/grades?course_id=…&exam_period=…
 */
export const getPersonalGrades = async ({ course_id, exam_period }) => {
  const qs = new URLSearchParams();
  if (course_id)   qs.append('course_id',   course_id);
  if (exam_period) qs.append('exam_period', exam_period);

  const suffix = qs.toString() ? `?${qs}` : '';
  const data = await request(`/personal/grades${suffix}`);
  return Array.isArray(data) ? data : [];
};

// public/js/api/institution.js
import { request } from './_request.js';

export const registerInstitution = ({ name, email, director }) =>
  request('/registration', {
    method: 'POST',
    body: { name, contact_email: email, director }
  });

// ← new helper:
export const getInstitutions = () =>
  request('/institutions', { method: 'GET' });

/**
 * Register an instructor; they receive an email link to choose a password.
 * @param {{ email: string }} payload
 */
export const registerInstructor = ({ email }) =>
  request('/institution/instructors', { method: 'POST', body: { email } });

/**
 * Upload the student registry CSV (columns: student_id, email).
 * @param {File} file
 */
export const uploadStudentRoster = file => {
  const fd = new FormData();
  fd.append('file', file);
  return request('/institution/student-roster', { method: 'POST', body: fd });
};

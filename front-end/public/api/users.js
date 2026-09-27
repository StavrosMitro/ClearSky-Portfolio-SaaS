import { request } from './_request.js';

/**
 * Ask for a student confirmation email. The student ID and university email
 * must match the secretariat's student registry.
 * @param {{ student_id: string, email: string }} payload
 */
export const requestStudentRegistration = ({ student_id, email }) =>
  request('/user/register', {
    method: 'POST',
    body  : { student_id, email }
  });

/**
 * Ask for a password-reset link. The reply is the same whether or not the
 * account exists.
 * @param {{ email: string }} payload
 */
export const requestPasswordReset = ({ email }) =>
  request('/user/forgot-password', {
    method: 'POST',
    body  : { email }
  });

/**
 * Finish an emailed link (student confirmation, instructor invitation or
 * password reset) by
 * choosing a password.
 * @param {{ token: string, password: string }} payload
 */
export const activateAccount = ({ token, password }) =>
  request('/user/activate', {
    method: 'POST',
    body  : { token, password }
  });

/**
 * Finish a first Google sign-in by confirming the student ID. The signup
 * ticket travels in an HttpOnly cookie, never through JavaScript.
 * @param {{ student_id: string }} payload
 */
export const completeGoogleSignup = ({ student_id }) =>
  request('/user/google-signup', {
    method: 'POST',
    body  : { student_id }
  });

/**
 * Log in an existing user.
 */
export const loginUser = ({ username, password }) =>
  request('/user/login', {
    method: 'POST',
    body  : { username, password }
  });

/**
 * Change password for an existing user.
 * @param {{ username: string, old_password: string, new_password: string }} payload
 */
export const changePassword = ({ username, old_password, new_password }) =>
  request('/user/change-password', {
    method: 'PATCH',
    body  : { username, old_password, new_password }
  });

/**
 * Login via Google token.
 * @param {string} token  Google ID token
 * @param {string} role   User role (optional)
 */
export const googleLoginUser = token =>
  request('/user/google-login', {
    method: 'POST',
    body  : { token }
  });

export const logoutUser = () => request('/user/logout', { method: 'POST' });

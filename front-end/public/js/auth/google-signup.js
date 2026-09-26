// auth/google-signup.js – student-ID step of a first Google sign-in
import { completeGoogleSignup } from '../../api/users.js';

const form   = document.querySelector('#google-signup-form');
const status = document.querySelector('#google-signup-status');

form.addEventListener('submit', async e => {
  e.preventDefault();
  status.style.display = 'none';
  const student_id = form.student_id.value.trim();
  if (!student_id) {
    status.textContent   = 'Your student ID is required';
    status.style.display = 'block';
    return;
  }

  const button = form.querySelector('button[type="submit"]');
  button.disabled = true;
  try {
    // The orchestrator sets the HttpOnly session cookie.
    await completeGoogleSignup({ student_id });
    await fetch('/api/session', { method: 'POST', credentials: 'same-origin' });
    window.location.href = '/student';
  } catch (err) {
    status.textContent   = err.message;
    status.style.display = 'block';
    button.disabled = false;
  }
});

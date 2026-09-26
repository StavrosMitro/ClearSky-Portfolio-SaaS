// auth/signup.js – roster-verified student registration (step 1 of 2)
import { requestStudentRegistration } from '../../api/users.js';

const form   = document.querySelector('#signup-form');
const status = document.querySelector('#signup-status');

function show(message, isError) {
  status.textContent   = message;
  status.style.color   = isError ? '#c00' : '#006400';
  status.style.display = 'block';
}

form.addEventListener('submit', async e => {
  e.preventDefault();
  const student_id = form.student_id.value.trim();
  const email      = form.email.value.trim();
  if (!student_id || !email) {
    return show('Student ID and university email are required', true);
  }

  const button = form.querySelector('button[type="submit"]');
  button.disabled = true;
  try {
    await requestStudentRegistration({ student_id, email });
    form.reset();
    show('Check your university inbox: we sent you a confirmation link (valid for 24 hours).', false);
  } catch (err) {
    show(err.message, true);
  } finally {
    button.disabled = false;
  }
});

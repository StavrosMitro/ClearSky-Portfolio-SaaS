// auth/forgot-password.js – ask for a password-reset email
import { requestPasswordReset } from '../../api/users.js';

const form   = document.querySelector('#forgot-form');
const status = document.querySelector('#forgot-status');

function show(message, isError) {
  status.textContent   = message;
  status.style.color   = isError ? '#c00' : '#006400';
  status.style.display = 'block';
}

form.addEventListener('submit', async e => {
  e.preventDefault();
  const email = form.email.value.trim();
  if (!email) {
    return show('Your email is required', true);
  }
  const button = form.querySelector('button[type="submit"]');
  button.disabled = true;
  try {
    const { message } = await requestPasswordReset({ email });
    form.reset();
    show(message, false);
  } catch (err) {
    show(err.message, true);
  } finally {
    button.disabled = false;
  }
});

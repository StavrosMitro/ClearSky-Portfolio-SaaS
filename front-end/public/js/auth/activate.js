// auth/activate.js – finish an emailed link by choosing a password
import { activateAccount } from '../../api/users.js';

const form   = document.querySelector('#activate-form');
const status = document.querySelector('#activate-status');

// The token arrives in the URL fragment, which browsers never send to a
// server. Keep it in memory and drop it from the address bar and history.
const token = new URLSearchParams(window.location.hash.slice(1)).get('token');
history.replaceState(null, '', window.location.pathname);

function show(message, isError) {
  status.textContent   = message;
  status.style.color   = isError ? '#c00' : '#006400';
  status.style.display = 'block';
}

if (!token) {
  form.querySelector('button[type="submit"]').disabled = true;
  show('This link is incomplete. Open the link from your email again.', true);
}

form.addEventListener('submit', async e => {
  e.preventDefault();
  const password = form.password.value;
  if (password.length < 8) {
    return show('The password must contain at least 8 characters', true);
  }
  if (password !== form.confirm.value) {
    return show('The passwords do not match', true);
  }

  const button = form.querySelector('button[type="submit"]');
  button.disabled = true;
  try {
    await activateAccount({ token, password });
    window.location.href = '/login?activated=1';
  } catch (err) {
    show(err.message, true);
    button.disabled = false;
  }
});

// front-end/public/js/auth/login.js
import { flash } from '../../script.js';
import { loginUser } from '../../api/users.js';

const form     = document.querySelector('#login-form');
const errorMsg = document.querySelector('#error-msg');

form.addEventListener('submit', async e => {
  e.preventDefault();
  errorMsg.style.display = 'none';

  // ──────────────────────────────────────────────────────────────
  // 1) Build payload (username or e-mail)
  // ──────────────────────────────────────────────────────────────
  const input    = form.username.value.trim();
  const password = form.password.value;
  // Email addresses are valid usernames; the API uses one canonical field.
  const payload = { username: input, password };

  try {
    // ────────────────────────────────────────────────────────────
    // 2) Ask orchestrator to log us in. The token stays in an HttpOnly cookie.
    // ────────────────────────────────────────────────────────────
    const { role } = await loginUser(payload);

    // 4) Tell the Express layer to remember who we are (for EJS templates)
    await fetch('/api/session', {
      method : 'POST',
      credentials: 'same-origin'
    });

    // ────────────────────────────────────────────────────────────
    // 5) Redirect according to role
    // ────────────────────────────────────────────────────────────
    if (['institution_representative', 'representative'].includes(role)) {
      window.location.href = '/institution';
    } else if (role === 'instructor') {
      window.location.href = '/instructor';
    } else if (role === 'student') {
      window.location.href = '/student';
    } else {
      window.location.href = '/';
    }
  } catch (err) {
    errorMsg.textContent = err.message;
    errorMsg.style.display = 'block';
  }
});

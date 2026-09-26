// front-end/public/js/auth/logout.js
import { flash } from '../../script.js';
import { logoutUser } from '../../api/users.js';

const logoutBtn = document.querySelector('#logout-button');
if (logoutBtn) {
  logoutBtn.addEventListener('click', async e => {
    e.preventDefault();
	try { await logoutUser(); } catch (_) { /* local session is still cleared */ }
    flash('Logged out');
    window.location.href = '/login';
  });
}

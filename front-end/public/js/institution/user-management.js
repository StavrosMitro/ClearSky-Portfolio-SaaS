import { flash } from '../../script.js';
import { changePassword } from '../../api/users.js';
import { registerInstructor, uploadStudentRoster } from '../../api/institution.js';

// Register instructor handler
const instructorForm = document.querySelector('#instructor-form');
instructorForm.addEventListener('submit', async e => {
  e.preventDefault();
  const email = instructorForm.email.value.trim();
  if (!email) {
    return flash('The instructor e-mail is required');
  }
  try {
    const { resent } = await registerInstructor({ email });
    flash(resent ? 'Invitation sent again ✔' : 'Instructor registered; invitation sent ✔');
    instructorForm.reset();
  } catch (err) {
    flash(`Error: ${err.message}`);
  }
});

// Student registry upload handler
const rosterForm   = document.querySelector('#roster-form');
const rosterResult = document.querySelector('#roster-result');
rosterForm.addEventListener('submit', async e => {
  e.preventDefault();
  const file = rosterForm.file.files[0];
  rosterResult.style.display = 'none';
  if (!file) {
    return flash('Choose a CSV file first');
  }
  try {
    const result = await uploadStudentRoster(file);
    rosterResult.style.color = '#006400';
    rosterResult.textContent =
      `Imported ${result.received} rows: ${result.inserted} new, ` +
      `${result.updated} updated, ${result.unchanged} unchanged.`;
    rosterForm.reset();
  } catch (err) {
    // The message lists the offending line numbers.
    rosterResult.style.color = '#c00';
    rosterResult.textContent = err.message;
  }
  rosterResult.style.display = 'block';
});

// Change password handler
const changeForm = document.querySelector('#change-pass-form');
changeForm.addEventListener('submit', async e => {
  e.preventDefault();

  const payload = {
    username     : changeForm.username.value.trim(),
    old_password : changeForm.old_password.value,
    new_password : changeForm.new_password.value
  };

  try {
    await changePassword(payload);
    flash('Password changed ✔');
    changeForm.reset();
  } catch (err) {
    flash(`Error: ${err.message}`);
  }
});

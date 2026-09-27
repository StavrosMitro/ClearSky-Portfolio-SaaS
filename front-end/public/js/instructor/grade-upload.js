// Shared by the initial and final grade pages (SRS 2.5 / 2.10):
// upload → preview (course, period, number of grades, problems)
// → CONFIRM publishes, CANCEL discards.
import { flash } from '../../script.js';
import { uploadWorkbook, confirmUpload, cancelUpload } from '../../api/grades.js';

export function setupGradeUpload(kind) {
  const form     = document.querySelector('#upload-form');
  const course   = document.querySelector('#preview-course');
  const period   = document.querySelector('#preview-period');
  const count    = document.querySelector('#preview-count');
  const problems = document.querySelector('#preview-problems');
  const confirm  = document.querySelector('#preview-confirm');
  const cancel   = document.querySelector('#preview-cancel');
  let uploadId = null;

  function showPreview(preview) {
    uploadId = preview?.upload_id ?? null;
    course.value = preview ? `${preview.course_title ?? ''} (${preview.course_code ?? ''})` : '';
    period.value = preview?.period ?? '';
    count.value  = preview ? `${preview.grade_count} students, ${preview.question_count} questions` : '';
    problems.innerHTML = '';
    for (const problem of preview?.problems ?? []) {
      const li = document.createElement('li');
      li.textContent = problem;
      problems.appendChild(li);
    }
    if (preview?.requires_credit) {
      const li = document.createElement('li');
      li.textContent = 'Publishing uses 1 credit of your institution (initial + final).';
      li.style.color = '#555';
      problems.appendChild(li);
    }
    confirm.disabled = !preview?.can_confirm;
    cancel.disabled  = !uploadId;
  }
  showPreview(null);

  form.addEventListener('submit', async e => {
    e.preventDefault();
    const file = form.querySelector('input[type="file"]').files[0];
    if (!file) return flash('Please select an XLSX file.');
    try {
      showPreview(await uploadWorkbook(kind, file));
      flash('Check the preview, then confirm.');
    } catch (err) {
      showPreview(null);
      flash(err.message || 'Upload failed');
    }
  });

  confirm.addEventListener('click', async () => {
    if (!uploadId) return;
    confirm.disabled = true;
    try {
      const result = await confirmUpload(uploadId);
      showPreview(null);
      form.reset();
      flash(`${kind === 'final' ? 'Final' : 'Initial'} grades published for ${result.course_title} (${result.period})` +
        (result.charged ? ' – 1 credit used' : ''));
    } catch (err) {
      confirm.disabled = false;
      flash(err.message || 'Publishing failed');
    }
  });

  cancel.addEventListener('click', async () => {
    if (!uploadId) return;
    try {
      await cancelUpload(uploadId);
    } catch (_) { /* nothing to undo */ }
    showPreview(null);
    form.reset();
    flash('Upload cancelled');
  });
}

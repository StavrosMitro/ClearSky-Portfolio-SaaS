// front-end/public/js/student/personal.js – one grading's grades (SRS 2.7)
import { flash } from '../../script.js';
import { getMyGrades } from '../../api/personal.js';

window.addEventListener('DOMContentLoaded', async () => {
  const gradingId = new URLSearchParams(location.search).get('grading');
  if (!gradingId) {
    location.href = '/student/my-courses';
    return;
  }
  const tbody = document.querySelector('table tbody');
  try {
    const grade = (await getMyGrades()).find(g => g.grading_id === gradingId);
    if (!grade) {
      tbody.innerHTML = '<tr><td colspan="4" style="text-align:center;">No grades found.</td></tr>';
      return;
    }
    const rows = [[grade.period, `${grade.course_title} (${grade.course_code})`, grade.state, grade.total ?? '—']];
    // Per-question detail exists only while the grading is open (SRS REQ016).
    (grade.question_scores ?? []).forEach((score, i) => {
      const weight = grade.question_weights?.[i];
      rows.push(['', `Q${i + 1}` + (weight != null ? ` (weight ${weight})` : ''), '', score ?? '—']);
    });
    tbody.innerHTML = '';
    for (const cells of rows) {
      const tr = document.createElement('tr');
      for (const text of cells) {
        const td = document.createElement('td');
        td.textContent = text;
        tr.appendChild(td);
      }
      tbody.appendChild(tr);
    }
  } catch (err) {
    flash(err.message || 'Failed to load grades');
  }
});

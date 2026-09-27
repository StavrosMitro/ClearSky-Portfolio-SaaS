// front-end/public/js/student/my-courses.js – the student's gradings (SRS 2.7)
import { flash } from '../../script.js';
import { getMyGrades } from '../../api/personal.js';

const link = (href, label, enabled = true) => {
  const a = document.createElement('a');
  a.textContent = label;
  a.className = enabled ? 'button' : 'button button--secondary';
  if (enabled) a.href = href;
  else Object.assign(a.style, { pointerEvents: 'none', opacity: '0.6' });
  return a;
};

window.addEventListener('DOMContentLoaded', async () => {
  const tbody = document.querySelector('table tbody');
  try {
    const grades = await getMyGrades();
    if (!grades.length) {
      tbody.innerHTML = '<tr><td colspan="4" style="text-align:center;">No courses found.</td></tr>';
      return;
    }
    tbody.innerHTML = '';
    for (const g of grades) {
      const open = g.state === 'open';
      const id = encodeURIComponent(g.grading_id);
      const tr = document.createElement('tr');
      if (open) tr.style.background = '#e6e7ea';
      for (const text of [`${g.course_title} (${g.course_code})`, g.period, g.state]) {
        const td = document.createElement('td');
        td.textContent = text;
        tr.appendChild(td);
      }
      const actions = document.createElement('td');
      actions.append(
        link(`/student/personal?grading=${id}`, 'View grades'), ' ',
        link(`/student/request?grading=${id}`, 'Ask review', open), ' ',
        link(`/student/status?grading=${id}`, 'Status'),
      );
      tr.appendChild(actions);
      tbody.appendChild(tr);
    }
  } catch (err) {
    flash(err.message || 'Failed to load courses');
  }
});

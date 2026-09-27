// front-end/public/js/instructor/review-list.js – requests on my gradings (SRS 2.9)
import { flash } from '../../script.js';
import { getReviewInbox } from '../../api/instructor.js';

window.addEventListener('DOMContentLoaded', async () => {
  const tbody = document.querySelector('table tbody');
  try {
    const reviews = await getReviewInbox();
    if (!reviews.length) {
      tbody.innerHTML = '<tr><td colspan="4" style="text-align:center;">No review requests</td></tr>';
      return;
    }
    tbody.innerHTML = '';
    for (const r of reviews) {
      const tr = document.createElement('tr');
      for (const text of [`${r.course_title} (${r.course_code})`, r.period, `${r.student_display} (${r.student_id})`]) {
        const td = document.createElement('td');
        td.textContent = text;
        tr.appendChild(td);
      }
      const action = document.createElement('td');
      if (r.status === 'pending') {
        const a = document.createElement('a');
        a.className = 'button';
        a.href = `/instructor/reply?request=${encodeURIComponent(r.id)}`;
        a.textContent = 'Reply';
        action.appendChild(a);
      } else {
        action.textContent = r.status;
      }
      tr.appendChild(action);
      tbody.appendChild(tr);
    }
  } catch (err) {
    flash(err.message || 'Failed to fetch review list');
  }
});

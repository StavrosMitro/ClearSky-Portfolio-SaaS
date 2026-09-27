// front-end/public/js/student/review-request.js – ask for a review (SRS 2.8)
import { flash } from '../../script.js';
import { postReviewRequest } from '../../api/student.js';

const form = document.querySelector('#student-review-request-form');
form?.addEventListener('submit', async e => {
  e.preventDefault();
  const grading_id = new URLSearchParams(location.search).get('grading');
  if (!grading_id) {
    location.href = '/student/my-courses';
    return;
  }
  try {
    await postReviewRequest({ grading_id, message: form.message.value.trim() });
    flash('Review request submitted!');
    setTimeout(() => (location.href = `/student/status?grading=${encodeURIComponent(grading_id)}`), 1200);
  } catch (err) {
    flash(err.message);
  }
});

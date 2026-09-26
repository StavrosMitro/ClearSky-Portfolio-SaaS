// front-end/public/js/student/review-status.js
import { flash } from '../../script.js';
import { getReviewStatus } from '../../api/student.js';

window.addEventListener('DOMContentLoaded', async () => {
  const params      = new URLSearchParams(location.search);
  const course_id   = params.get('course');
  const exam_period = params.get('period');   // optional

  if (!course_id) {
    flash('Please pick a course first');
    window.location.href = '/student/my-courses';
    return;
  }

  try {
    const data = await getReviewStatus({ course_id, exam_period });
    // If backend returns a message (no review found), show it
    if (data && data.message) {
      flash(data.message);
      return;
    }
    // Fill all fields in the panel
    document.getElementById('course_id').value = data.course_id ?? '';
    document.getElementById('exam_period').value = data.exam_period ?? '';
    document.getElementById('student_message').value = data.student_message ?? '';
    document.getElementById('status').value = data.status ?? '';
    document.getElementById('instructor_action').value = data.instructor_action ?? '';
    document.getElementById('instructor_reply_message').value = data.instructor_reply_message ?? '';
    document.getElementById('review_created_at').value = data.review_created_at
      ? new Date(data.review_created_at).toLocaleString()
      : '';
    document.getElementById('reviewed_at').value = data.reviewed_at
      ? new Date(data.reviewed_at).toLocaleString()
      : '';
  } catch (err) {
    flash(err.message);
  }
});

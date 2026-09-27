// front-end/public/js/student/review-status.js – a review request's status
import { flash } from '../../script.js';
import { getMyReviews } from '../../api/student.js';

const ACTIONS = { total_accept: 'Total accept', partial_accept: 'Partial accept', reject: 'Reject' };
const when = value => (value ? new Date(value).toLocaleString() : '');

window.addEventListener('DOMContentLoaded', async () => {
  const gradingId = new URLSearchParams(location.search).get('grading');
  if (!gradingId) {
    location.href = '/student/my-courses';
    return;
  }
  try {
    const review = (await getMyReviews()).find(r => r.grading_id === gradingId);
    if (!review) {
      flash('You have not asked for a review of this course.');
      return;
    }
    const fields = {
      course_id: `${review.course_title} (${review.course_code})`,
      exam_period: review.period,
      student_message: review.message,
      status: review.status,
      instructor_action: ACTIONS[review.reply_action] ?? '',
      instructor_reply_message: review.reply_message ?? '',
      review_created_at: when(review.created_at),
      reviewed_at: when(review.replied_at),
    };
    for (const [id, value] of Object.entries(fields)) {
      document.getElementById(id).value = value;
    }
  } catch (err) {
    flash(err.message);
  }
});

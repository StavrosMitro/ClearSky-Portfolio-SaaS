// front-end/public/js/instructor/reply.js – answer one review request
import { flash } from '../../script.js';
import { getReview, replyToReview } from '../../api/instructor.js';

const form = document.querySelector('#instructor-reply-form');
const requestId = new URLSearchParams(location.search).get('request');

window.addEventListener('DOMContentLoaded', async () => {
  if (!requestId) {
    location.href = '/instructor/review-list';
    return;
  }
  try {
    const review = await getReview(requestId);
    document.getElementById('course_id').value = `${review.course_title} (${review.course_code})`;
    document.getElementById('exam_period').value = review.period;
    document.getElementById('student_id').value = `${review.student_display} (${review.student_id})`;
    document.getElementById('student_message').value = review.message;
    document.getElementById('review_created_at').value = new Date(review.created_at).toLocaleString();
  } catch (err) {
    flash(err.message || 'Failed to fetch the review request');
  }
});

form.addEventListener('submit', async e => {
  e.preventDefault();
  try {
    await replyToReview(requestId, { action: form.decision.value, message: form.message.value.trim() });
    flash('Reply sent!');
    setTimeout(() => (location.href = '/instructor/review-list'), 1200);
  } catch (err) {
    flash(err.message || 'Failed to send reply');
  }
});

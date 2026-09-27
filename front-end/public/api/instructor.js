// front-end/public/api/instructor.js – replies to review requests (SRS 2.9)
import { request } from './_request.js';

export const getReviewInbox = async () => {
  const data = await request('/reviews/inbox');
  return Array.isArray(data) ? data : [];
};

export const getReview = id => request(`/reviews/${encodeURIComponent(id)}`);

/** action: total_accept | partial_accept | reject */
export const replyToReview = (id, { action, message }) =>
  request(`/reviews/${encodeURIComponent(id)}/reply`, { method: 'POST', body: { action, message } });

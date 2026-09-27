// student.js – grade review requests (SRS 2.8)
import { request } from './_request.js';

export const postReviewRequest = ({ grading_id, message }) =>
  request('/reviews', { method: 'POST', body: { grading_id, message } });

/** The student's review requests with their status and replies. */
export const getMyReviews = async () => {
  const data = await request('/reviews/mine');
  return Array.isArray(data) ? data : [];
};

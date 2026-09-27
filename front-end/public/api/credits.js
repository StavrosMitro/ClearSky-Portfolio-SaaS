// credits.js – the representative's institution credits (SRS 2.4)
import { request } from './_request.js';

export const purchaseCredits = ({ amount }) =>
  request('/purchase', { method: 'PATCH', body: { amount } });

/** The institution with its current credits. */
export const getMyCredits = () => request('/mycredits');

export const getCreditHistory = () => request('/institution/credit-history');

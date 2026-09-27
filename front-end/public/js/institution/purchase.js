// public/js/institution/purchase.js – buy credits for my institution (SRS 2.4)
import { flash } from '../../script.js';
import { purchaseCredits, getMyCredits } from '../../api/credits.js';

const balance = document.querySelector('#credit-balance');

async function showBalance() {
  try {
    const inst = await getMyCredits();
    balance.textContent = `${inst.name}: ${inst.credits} credits (1 credit = one course grading)`;
  } catch (err) {
    balance.textContent = err.message; // e.g. the institution is not registered yet
  }
}

document.addEventListener('DOMContentLoaded', () => {
  showBalance();
  const form = document.querySelector('#purchase-form');
  form.addEventListener('submit', async e => {
    e.preventDefault();
    try {
      const result = await purchaseCredits({ amount: Number(form.amount.value) });
      flash(`Purchased. Balance: ${result.credits} credits`);
      form.reset();
      showBalance();
    } catch (err) {
      flash(err.message || 'Purchase failed');
    }
  });
});

import assert from 'node:assert/strict';
import test from 'node:test';
import { formatMoney, formatNumber } from '../src/lib/format.ts';

test('custom upstream units render without crashing the page', () => {
  for (const currency of ['CUSTOM', '积分', 'USDT', 'invalid-code']) {
    assert.equal(formatMoney({ amount: 734.291604, currency }), `${formatNumber(734.291604)} ${currency}`);
  }
  assert.equal(formatMoney({ amount: 0, currency: 'CUSTOM' }), `${formatNumber(0)} CUSTOM`);
});

test('standard currencies and missing balances retain their formatting', () => {
  for (const currency of ['USD', 'CNY', 'EUR']) {
    const expected = new Intl.NumberFormat(undefined, { style: 'currency', currency, maximumFractionDigits: 2 }).format(12.34);
    assert.equal(formatMoney({ amount: 12.34, currency }), expected);
    assert.equal(formatMoney({ amount: 12.34, currency: ` ${currency} ` }), expected);
  }
  assert.equal(formatMoney(), '—');
  assert.equal(formatMoney({ amount: 12.34, currency: '' }), formatMoney({ amount: 12.34, currency: 'USD' }));
});

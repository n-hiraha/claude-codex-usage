import test from 'node:test';
import assert from 'node:assert/strict';
import { renderUsageGauge } from '../src/usage-gauge.mjs';

const account = (used = 9, name = 'default') => ({ name, provider: 'codex', limits: [{ id: 'codex', primary: { usedPercent: used, windowDurationMins: 10080, resetsAt: 1760000000 } }] });

test('renders usage fill, remaining percentage and reset countdown', () => {
  const output = renderUsageGauge({ accounts: [account()] }, { now: 1759481600000 });
  assert.match(output, /Codex/);
  assert.match(output, /━/);
  assert.match(output, /残91%/);
  assert.match(output, /↻6d/);
});

test('zero, full and unknown usage remain distinguishable', () => {
  assert.match(renderUsageGauge({ accounts: [account(0)] }), /残100%/);
  assert.match(renderUsageGauge({ accounts: [account(100)] }), /残0%/);
  assert.match(renderUsageGauge({ accounts: [{ name: 'default', limits: [{ id: 'codex', primary: {} }] }] }), /残\?/);
  assert.match(renderUsageGauge({ accounts: [{ name: 'default', limits: [{ id: 'codex', primary: {} }] }] }), /不明/);
});

test('shows multiple accounts and both windows when space permits', () => {
  const two = account(40, 'work');
  two.limits[0].secondary = { usedPercent: 60, resetsAt: 1760000000 };
  const output = renderUsageGauge({ accounts: [account(20), two] }, { width: 100, now: 1759481600000 });
  assert.match(output, /Codex/);
  assert.match(output, /Codex work/);
  assert.match(output, /残80%/);
  assert.match(output, /残40%/);
});

test('small widths collapse accounts and sanitize control/format injection', () => {
  const output = renderUsageGauge({ accounts: [account(20, '#(touch x)\n'), account(30, 'other')] }, { width: 50, stale: true });
  assert.ok(!output.includes('#(touch'));
  assert.ok(!output.includes('\n'));
  assert.match(output, /stale/);
  assert.ok(output.length < 200);
  assert.match(output, /\+1/);
  const plain = output.replace(/#\[[^\]]*\]/g, '');
  assert.equal((plain.match(/\[/g) || []).length, (plain.match(/\]/g) || []).length);
});

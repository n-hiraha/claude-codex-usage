import test from 'node:test';
import assert from 'node:assert/strict';
import { renderUsageGauge } from '../src/usage-gauge.mjs';

test('Claude shows separate All models and Fable gauges', () => {
  const result = { accounts: [{ name: 'claude', provider: 'claude', limits: [
    { id: 'claude', primary: { usedPercent: 24, windowDurationMins: 300 }, secondary: { usedPercent: 71, windowDurationMins: 10080 } },
    { id: 'claude_fable', secondary: { usedPercent: 63, windowDurationMins: 10080 } },
  ] }] };
  const output = renderUsageGauge(result, { width: 180 });
  assert.match(output, /All models.*使用71%.*Fable.*使用63%/);
  assert.doesNotMatch(output, /5h|使用24%/);
  result.accounts[0].limits.pop();
  assert.match(renderUsageGauge(result, { width: 180 }), /Fable 未提供/);
});

const account = (used = 9, name = 'default') => ({ name, provider: 'codex', limits: [{ id: 'codex', primary: { usedPercent: used, windowDurationMins: 10080, resetsAt: 1760000000 } }] });

test('renders usage fill, used percentage and exact local reset time', () => {
  const output = renderUsageGauge({ accounts: [account()] }, { now: 1759481600000 });
  assert.match(output, /Codex/);
  assert.match(output, /━/);
  assert.match(output, /使用9%/);
  const reset = new Date(1760000000 * 1000);
  const expected = `↻${reset.getMonth() + 1}/${reset.getDate()} ${String(reset.getHours()).padStart(2, '0')}:${String(reset.getMinutes()).padStart(2, '0')}`;
  assert.ok(output.includes(expected));
});

test('zero, full and unknown usage remain distinguishable', () => {
  assert.match(renderUsageGauge({ accounts: [account(0)] }), /使用0%/);
  assert.match(renderUsageGauge({ accounts: [account(100)] }), /使用100%/);
  assert.match(renderUsageGauge({ accounts: [{ name: 'default', limits: [{ id: 'codex', primary: {} }] }] }), /使用\?/);
  assert.match(renderUsageGauge({ accounts: [{ name: 'default', limits: [{ id: 'codex', primary: {} }] }] }), /不明/);
});

test('shows multiple accounts and both windows when space permits', () => {
  const two = account(40, 'work');
  two.limits[0].secondary = { usedPercent: 60, resetsAt: 1760000000 };
  const output = renderUsageGauge({ accounts: [account(20), two] }, { width: 100, now: 1759481600000 });
  assert.match(output, /Codex/);
  assert.match(output, /Codex work/);
  assert.match(output, /使用20%/);
  assert.match(output, /使用60%/);
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

test('renders Claude labels and selects the Claude limit bucket', () => {
  const output = renderUsageGauge({ accounts: [{ name: 'default', provider: 'claude', limits: [
    { id: 'codex', primary: { usedPercent: 99, windowDurationMins: 60 } },
    { id: 'claude', secondary: { usedPercent: 20, windowDurationMins: 10080 } },
  ] }] }, { now: 1759481600000 });
  assert.match(output, /Claude/);
  assert.match(output, /使用20%/);
  assert.doesNotMatch(output, /使用99%/);
});

test('Claude errors remain labeled Claude when quota is unknown', () => {
  const output = renderUsageGauge({ accounts: [{ name: 'default', provider: 'claude', error: 'failed' }] });
  assert.match(output, /Claude/);
  assert.doesNotMatch(output, /Codex/);
});

import test from 'node:test';
import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { Readable, Writable } from 'node:stream';
import { readCodexUsage } from '../src/codex-usage.mjs';

function fakeCodex(replies, { serverRequest = false } = {}) {
  const stdout = new Readable({ read() {} });
  const writes = [];
  let child;
  const stdin = new Writable({ write(data, encoding, done) {
    const message = JSON.parse(data.toString()); writes.push(message);
    const response = typeof replies[message.method] === 'function' ? replies[message.method](message) : replies[message.method];
    if (response) setImmediate(() => stdout.push(JSON.stringify({ id: message.id, ...response }) + '\n'));
    done();
  } });
  child = new EventEmitter(); child.stdin = stdin; child.stdout = stdout; child.writes = writes;
  child.kill = () => { child.killed = true; stdout.push(null); child.emit('exit', 0); };
  if (serverRequest) setImmediate(() => stdout.push(JSON.stringify({ method: 'window/show', id: 'secret' }) + '\n'));
  return child;
}

test('uses the account protocol in order and normalizes multi-account limits', async () => {
  const child = fakeCodex({
    initialize: { result: {} },
    'account/read': { result: { account: { type: 'chatgpt', email: 'a@example.test', planType: 'pro', accessToken: 'secret' } } },
    'account/rateLimits/read': { result: { rateLimits: { limitId: 'old', primary: { usedPercent: 9 } }, rateLimitsByLimitId: {
      codex: { limitId: 'codex', limitName: 'Main', primary: { usedPercent: 25, windowDurationMins: 15, resetsAt: 100 } },
      other: { limitId: 'other', secondary: { usedPercent: 3, windowDurationMins: 60, resetsAt: 200 } },
    } } },
    'account/usage/read': { result: { summary: { lifetimeTokens: 123, peakDailyTokens: 9, longestRunningTurnSec: 99 }, dailyUsageBuckets: [{ startDate: '2026-01-01', tokens: 7 }] } },
  });
  const result = await readCodexUsage({ codexHome: '/tmp/profile', spawnImpl: () => child });
  assert.deepEqual(child.writes.map(x => x.method), ['initialize', 'initialized', 'account/read', 'account/rateLimits/read', 'account/usage/read', 'account/read']);
  assert.deepEqual(result.identity, { type: 'chatgpt', email: 'a@example.test', planType: 'pro' });
  assert.equal(result.limits[0].primary.usedPercent, 25);
  assert.equal(result.limits[1].secondary.windowDurationMins, 60);
  assert.deepEqual(result.summary, { lifetimeTokens: 123, peakDailyTokens: 9 });
  assert.deepEqual(result.dailyUsageBuckets, [{ startDate: '2026-01-01', tokens: 7 }]);
  assert.equal(result.usage, undefined);
  assert.equal(child.killed, true);
});

test('silent server times out and is terminated', async () => {
  const child = fakeCodex({});
  await assert.rejects(readCodexUsage({ codexHome: '/tmp/profile', timeoutMs: 20, spawnImpl: () => child }), /timed out/);
  assert.equal(child.killed, true);
});

test('oversized server output is rejected without exposing it', async () => {
  const child = fakeCodex({});
  const promise = readCodexUsage({ codexHome: '/tmp/profile', spawnImpl: () => child });
  child.stdout.push('x'.repeat(1024 * 1024 + 1));
  await assert.rejects(promise, /app-server exited/);
  assert.equal(child.killed, true);
});

test('stdin failure is handled and no credentials appear in errors', async () => {
  const child = fakeCodex({});
  const promise = readCodexUsage({ codexHome: '/tmp/profile', spawnImpl: () => child });
  child.stdin.emit('error', new Error('sensitive detail'));
  await assert.rejects(promise, error => !error.message.includes('sensitive detail'));
  assert.equal(child.killed, true);
});

test('falls back to the single rateLimits response and reports unsupported usage', async () => {
  const child = fakeCodex({
    initialize: { result: {} },
    'account/read': { result: { account: { type: 'apiKey' } } },
    'account/rateLimits/read': { result: { rateLimits: { limitId: 'codex', limitName: null, primary: null, secondary: null } } },
    'account/usage/read': { error: { code: -32601, message: 'unsupported' } },
  });
  const result = await readCodexUsage({ codexHome: '/tmp/profile', spawnImpl: () => child });
  assert.deepEqual(result.limits, [{ id: 'codex', name: null, primary: null, secondary: null }]);
  assert.equal(result.summary, null); assert.equal(result.dailyUsageBuckets, null);
  assert.deepEqual(result.warnings, ['Token usage unavailable']);
});

test('rejects account changes and never includes protocol error details', async () => {
  let reads = 0;
  const child = fakeCodex({ initialize: { result: {} }, 'account/read': () => ({ result: { account: reads++ ? { type: 'chatgpt', email: 'second@test' } : { type: 'chatgpt', email: 'first@test' } } }),
    'account/rateLimits/read': { result: {} }, 'account/usage/read': { result: {} } });
  await assert.rejects(readCodexUsage({ codexHome: '/tmp/profile', spawnImpl: () => child }), error => {
    assert.equal(error.message, 'Account changed while reading Codex usage'); return true;
  });
  assert.equal(child.killed, true);
});

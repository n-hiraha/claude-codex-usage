import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, readdir, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { cachedUsage } from '../src/usage-cache.mjs';

const accounts = [{ name: 'test', provider: 'codex', codexHome: '/tmp/example' }];
const data = { scannedAt: new Date(1000).toISOString(), accounts: [{ name: 'test', provider: 'codex', identity: { email: 'private@example.com' }, summary: { lifetimeTokens: 1000 }, limits: [{ id: 'codex', primary: { usedPercent: 9 } }] }] };

test('quota cache reuses fresh values, refreshes expired values and omits identity', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'monitor-cache-'));
  let calls = 0;
  const fetchUsage = async () => { calls++; return data; };
  try {
    await cachedUsage(accounts, { directory, now: 1000, fetchUsage });
    await cachedUsage(accounts, { directory, now: 2000, fetchUsage });
    assert.equal(calls, 1);
    const file = (await readdir(directory)).find(file => file.endsWith('.json'));
    const cache = await readFile(join(directory, file), 'utf8');
    assert.doesNotMatch(cache, /private@example|lifetimeTokens/);
    await cachedUsage(accounts, { directory, now: 62000, fetchUsage });
    assert.equal(calls, 2);
  } finally { await rm(directory, { recursive: true, force: true }); }
});

test('concurrent clients share a refresh lock and cold cache says loading', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'monitor-cache-'));
  let release;
  let started;
  const entered = new Promise(resolve => { started = resolve; });
  const fetchUsage = () => new Promise(resolve => { release = () => resolve(data); started(); });
  try {
    const first = cachedUsage(accounts, { directory, fetchUsage });
    await entered;
    const second = await cachedUsage(accounts, { directory, fetchUsage });
    assert.equal(second.loading, true);
    release();
    await first;
  } finally { await rm(directory, { recursive: true, force: true }); }
});

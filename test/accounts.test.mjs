import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, writeFile, rm, realpath } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { collectUsage, loadAccounts, renderUsage } from '../src/accounts.mjs';

test('accounts reject duplicates and relative homes before executing providers', async () => {
  const dir = await mkdtemp(join(tmpdir(), 'monitor-accounts-'));
  const file = join(dir, 'accounts.json');
  try {
    await writeFile(file, JSON.stringify({ accounts: [{ name: 'one', provider: 'codex', codexHome: 'relative' }] }));
    await assert.rejects(loadAccounts(file), /絶対パス/);
    await writeFile(file, JSON.stringify({ accounts: ['one', 'two'].map(name => ({ name, provider: 'codex', codexHome: dir })) }));
    await assert.rejects(loadAccounts(file), /重複/);
  } finally { await rm(dir, { recursive: true, force: true }); }
});

test('identity mismatch discards quota and per-profile failures do not stop other accounts', async () => {
  const result = await collectUsage([
    { name: 'wrong', provider: 'codex', expectedEmail: 'expected@example.com' },
    { name: 'offline', provider: 'codex', codexHome: 'offline' },
    { name: 'right', provider: 'codex', expectedEmail: 'actual@example.com' },
  ], { read: async ({ codexHome }) => {
    if (codexHome === 'offline') throw new Error('private error detail');
    return { identity: { email: 'actual@example.com' }, limits: [{ id: 'codex' }] };
  } });
  assert.ok(result.accounts[0].error);
  assert.equal(result.accounts[0].limits, undefined);
  assert.ok(result.accounts[1].error);
  assert.ok(!JSON.stringify(result).includes('private error detail'));
  assert.equal(result.accounts[2].limits[0].id, 'codex');
});

test('duplicate identities are warned and missing usage is not zero', async () => {
  const result = await collectUsage([{ name: 'one' }, { name: 'two' }], { read: async () => ({ identity: { email: 'same@example.com' }, limits: [] }) });
  assert.equal(result.warnings.length, 1);
  assert.doesNotMatch(renderUsage(result), /0%/);
  assert.match(renderUsage(result), /取得できません/);
});

test('loads Claude accounts and permits the same home across providers', async () => {
  const dir = await mkdtemp(join(tmpdir(), 'monitor-accounts-'));
  const file = join(dir, 'accounts.json');
  try {
    await writeFile(file, JSON.stringify({ accounts: [
      { name: 'codex', provider: 'codex', codexHome: dir },
      { name: 'claude', provider: 'claude', claudeHome: dir },
    ] }));
    const accounts = await loadAccounts(file);
    assert.deepEqual(accounts.map(({ name, provider }) => ({ name, provider })), [
      { name: 'codex', provider: 'codex' }, { name: 'claude', provider: 'claude' },
    ]);
    assert.equal(accounts[1].claudeHome, await realpath(dir));
  } finally { await rm(dir, { recursive: true, force: true }); }
});

test('routes Claude accounts to the injected Claude reader and keeps provider identity distinct', async () => {
  const calls = [];
  const result = await collectUsage([
    { name: 'codex', provider: 'codex', codexHome: '/codex' },
    { name: 'claude', provider: 'claude', claudeHome: '/claude' },
  ], {
    read: async args => { calls.push(['codex', args]); return { identity: { email: 'same@example.com' }, limits: [{ id: 'codex' }] }; },
    readClaude: async args => { calls.push(['claude', args]); return { identity: { email: 'same@example.com' }, limits: [{ id: 'claude' }] }; },
  });
  assert.deepEqual(calls, [['codex', { codexHome: '/codex' }], ['claude', { claudeHome: '/claude' }]]);
  assert.equal(result.warnings.length, 0);
  assert.deepEqual(result.accounts.map(account => account.provider), ['codex', 'claude']);
});

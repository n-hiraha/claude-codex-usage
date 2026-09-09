import test from 'node:test';
import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
const exec = promisify(execFile);
const cli = (...args) => exec(process.execPath, ['bin/tmux-ai-monitor.mjs', ...args], { timeout: 3000 });

test('CLI filters JSON and watch without a TTY prints once', async () => {
  const { stdout } = await cli('status', '--demo', '--json', '--agent', 'codex');
  assert.deepEqual(JSON.parse(stdout).sessions.map(s => s.agent), ['codex']);
  const result = await cli('watch', '--demo', '--attention');
  assert.match(result.stdout, /api-server/);
  assert.doesNotMatch(result.stdout, /web-app|\x1b/);
});

test('CLI rejects invalid agent and inappropriate bell option', async () => {
  await assert.rejects(cli('status', '--agent', 'other'), error => error.code === 1 && error.stderr.includes('--agent'));
  await assert.rejects(cli('status', '--bell'), error => error.code === 1 && error.stderr.includes('--bell'));
});

test('usage demo shows separate accounts without real credentials', async () => {
  const { stdout } = await cli('usage', '--demo', '--json');
  const data = JSON.parse(stdout);
  assert.equal(data.accounts.length, 2);
  assert.deepEqual(data.accounts.map(a => a.name), ['personal', 'work']);
});

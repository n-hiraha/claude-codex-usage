import test from 'node:test';
import assert from 'node:assert/strict';
import { demoScan, renderStatus, statusline } from '../src/display.mjs';
import { jumpToPane, tmuxConfig } from '../src/integration.mjs';

test('terminal and tmux output cannot inject control sequences or format commands', () => {
  const result = demoScan();
  result.sessions[0].project = '\x1b[2J#(touch /tmp/unwanted)#[fg=red]%H\n';
  assert.ok(!renderStatus(result).includes('\x1b'));
  const bar = statusline(result.sessions);
  assert.ok(!bar.includes('#(touch'));
  assert.ok(!bar.includes('#[fg=red]'));
  assert.ok(!bar.includes('%H'));
  assert.ok(!bar.includes('\n'));
});

test('attention view omits sessions generating responses', () => {
  const view = renderStatus(demoScan(), { attention: true });
  assert.match(view, /api-server/);
  assert.match(view, /docs/);
  assert.doesNotMatch(view, /web-app/);
});

test('jump uses a stable pane ID and explicit client without shell execution', async () => {
  const calls = [];
  await jumpToPane('%123', { client: '/dev/ttys001', socket: 'test', run: async (...args) => calls.push(args) });
  assert.deepEqual(calls, [[['switch-client', '-c', '/dev/ttys001', '-t', '%123'], 'test']]);
  await assert.rejects(jumpToPane('%1; touch x'), /ペインID/);
});

test('generated config includes absolute executable paths and popup', () => {
  const config = tmuxConfig('/tmp/a b/monitor.mjs', '/usr/local/bin/node');
  assert.match(config, /status-format\[1\]/);
  assert.match(config, /usage-statusline/);
  assert.doesNotMatch(config, /set -g status-right/);
  assert.match(config, /display-popup/);
  assert.match(config, /'\/tmp\/a b\/monitor.mjs'/);
});

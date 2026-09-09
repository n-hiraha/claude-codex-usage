import test from 'node:test';
import assert from 'node:assert/strict';
import { execFile, spawn } from 'node:child_process';
import { promisify } from 'node:util';
import { mkdtemp, writeFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';
import { scan } from '../src/scanner.mjs';
import { jumpToPane, shellQuote, tmuxConfig } from '../src/integration.mjs';

test('real isolated tmux: detect agent, source config, switch client to pane', { skip: !process.env.RUN_TMUX_TESTS, timeout: 15000 }, async () => {
  const exec = promisify(execFile);
  const directory = await mkdtemp(join(tmpdir(), 'ai-monitor-'));
  const socket = `ai-monitor-test-${process.pid}`;
  const run = async args => (await exec('tmux', ['-L', socket, ...args], { timeout: 3000 })).stdout;
  let client;
  try {
    const binary = join(directory, 'claude');
    await writeFile(binary, "console.log('Allow this command? (y/n)'); setTimeout(() => {}, 60000);\n");
    const pane = (await run(['-f', '/dev/null', 'new-session', '-d', '-P', '-F', '#{pane_id}', '-s', 'agent', '-x', '100', '-y', '30', `exec ${shellQuote(process.execPath)} ${shellQuote(binary)}`])).trim();
    await delay(200);
    const result = await scan({ socket });
    assert.deepEqual(result.warnings, []);
    assert.equal(result.sessions.length, 1);
    assert.equal(result.sessions[0].paneId, pane);
    assert.equal(result.sessions[0].agent, 'claude');
    assert.equal(result.sessions[0].phase, 'permission');

    const configPath = join(directory, 'monitor.conf');
    await writeFile(configPath, tmuxConfig(resolve('bin/tmux-ai-monitor.mjs'), process.execPath, socket).replace('usage-statusline', 'usage-statusline --demo'));
    await run(['source-file', configPath]);
    assert.match(await run(['show-option', '-gv', 'status-format[1]']), /usage-statusline/);
    assert.match(await run(['list-keys', '-T', 'prefix', 'a']), /display-popup/);

    await run(['new-session', '-d', '-s', 'other']);
    client = spawn('tmux', ['-L', socket, '-C', 'attach-session', '-t', 'other'], { stdio: ['pipe', 'pipe', 'pipe'] });
    client.stdout.resume();
    client.stderr.resume();
    let clientName = '';
    for (let i = 0; i < 20; i++) {
      clientName = (await run(['list-clients', '-F', '#{client_name}'])).trim();
      if (clientName) break;
      await delay(50);
    }
    assert.ok(clientName, 'control client attached');
    await jumpToPane(pane, { socket, client: clientName });
    assert.equal((await run(['list-clients', '-F', '#{session_name}'])).trim(), 'agent');
  } finally {
    client?.stdin.end();
    client?.kill();
    await run(['kill-server']).catch(() => {});
    await rm(directory, { recursive: true, force: true });
  }
});

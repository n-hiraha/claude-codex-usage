import test from 'node:test';
import assert from 'node:assert/strict';
import { detectAgent, inferPhase, parsePanes, parseProcesses, rankSessions, scan } from '../src/scanner.mjs';

test('macOS truncated comm is recovered from executable argv, not prompt text', () => {
  assert.equal(detectAgent({ comm: '/Users/apple/.lo', args: '/Users/apple/.local/bin/node /tmp/claude' }), 'claude');
  assert.equal(detectAgent({ comm: '/Users/apple/.lo', args: '/Users/apple/.local/bin/codex --help' }), 'codex');
  assert.equal(detectAgent({ comm: '/Users/apple/.lo', args: '/Users/apple/.local/bin/other codex' }), null);
});

test('parseProcesses preserves command arguments and numeric metrics', () => {
  const [process] = parseProcesses('  42  10  3.5  2048 node /opt/claude/index.js --verbose\n');
  assert.deepEqual(process, { pid: 42, ppid: 10, cpu: 3.5, rss: 2048, comm: 'node', args: '/opt/claude/index.js --verbose' });
});

test('detectAgent accepts native and node CLIs but ignores shell text', () => {
  assert.equal(detectAgent({ comm: 'claude', args: 'claude' }), 'claude');
  assert.equal(detectAgent({ comm: 'node', args: 'node /usr/local/bin/codex-cli --x' }), 'codex');
  assert.equal(detectAgent({ comm: 'node', args: 'node /opt/@google/gemini-cli/dist/index.js' }), 'gemini');
  assert.equal(detectAgent({ comm: 'node', args: 'node /projects/codex/server.js' }), null);
  assert.equal(detectAgent({ comm: 'bash', args: 'bash -c claude' }), null);
  assert.equal(detectAgent({ comm: 'zsh', args: 'zsh prompt: codex' }), null);
});

test('detectAgent does not treat node option values or eval text as CLI scripts', () => {
  assert.equal(detectAgent({ comm: 'node', args: 'node --eval "require(\'claude\')"' }), null);
  assert.equal(detectAgent({ comm: 'node', args: 'node -p claude' }), null);
  assert.equal(detectAgent({ comm: 'node', args: 'node --require /tmp/claude' }), null);
  assert.equal(detectAgent({ comm: 'node', args: 'node --loader /tmp/codex /projects/app.js' }), null);
  assert.equal(detectAgent({ comm: 'node', args: 'node --require /tmp/register.js /opt/@openai/codex/bin/codex.js' }), 'codex');
  assert.equal(detectAgent({ comm: 'node', args: 'node /opt/@openai/codex/bin/codex.js.backup' }), null);
});

test('parsePanes retains empty tab fields', () => {
  const [pane] = parsePanes('%1\t123\t$0\twork\t2\t0\t\tclaude\n');
  assert.equal(pane.paneId, '%1');
  assert.equal(pane.cwd, '');
  assert.equal(pane.currentCommand, 'claude');
});

test('inferPhase uses recent UI-like status patterns and stays conservative', () => {
  assert.equal(inferPhase('old prose: permission was discussed\nAllow this command? (y/n)'), 'permission');
  assert.equal(inferPhase('⏺ Running command\n'), 'tool');
  assert.equal(inferPhase('Working (2s • esc to interrupt)\n'), 'thinking');
  assert.equal(inferPhase('Approval: Allow this command? (y/n)\n\nRunning command\n'), 'tool');
  assert.equal(inferPhase('❯\n'), 'done');
  assert.equal(inferPhase('completed\n'), 'done');
  assert.equal(inferPhase('The user wrote that the task is done in a paragraph.'), 'unknown');
});

test('inferPhase keeps active status ahead of stale permission and footer prompt', () => {
  assert.equal(inferPhase('Would you like to proceed?\n1. Yes\n2. No\nWorking (2s • esc to interrupt)\n❯'), 'thinking');
  assert.equal(inferPhase('Working (2s • esc to interrupt)\nWould you like to proceed?\n1. Yes\n2. No\n❯'), 'permission');
  assert.equal(inferPhase('\u001b[32mWorking (2s • esc to interrupt)\u001b[0m\n❯'), 'thinking');
  assert.equal(inferPhase('The transcript says: Allow this command in the example.'), 'unknown');
  assert.equal(inferPhase('The transcript says: Allow this command?'), 'unknown');
  assert.equal(inferPhase('Would you like to proceed?\nRunning command\nCompleted\n❯'), 'done');
  assert.equal(inferPhase('1. Yes\n2. No'), 'unknown');
});

test('scan chooses the topmost agent descendant and captures its pane', async () => {
  const calls = [];
  const runner = async (file, args) => {
    calls.push([file, args]);
    if (file === 'tmux' && args.includes('list-panes')) return { stdout: '%1\t100\t$0\twork\t0\t0\t/tmp/project\tbash\n', stderr: '' };
    if (file === 'ps') return { stdout: '100 1 0.1 100 bash bash\n101 100 2.5 4096 node node /opt/@openai/codex/bin/codex.js\n102 101 1.0 2048 claude claude\n', stderr: '' };
    if (file === 'tmux' && args.includes('capture-pane')) return { stdout: 'Allow this command? (y/n)\n', stderr: '' };
    throw new Error(`unexpected command ${file} ${args.join(' ')}`);
  };
  const result = await scan({ socket: 'demo', execFile: runner });
  assert.equal(result.warnings.length, 0);
  assert.equal(result.sessions.length, 1);
  assert.deepEqual(result.sessions[0], {
    id: '%1', paneId: '%1', pid: 101, agent: 'codex', project: 'project', cwd: '/tmp/project',
    sessionId: '$0', sessionName: 'work', windowIndex: 0, paneIndex: 0,
    phase: 'permission', cpu: 2.5, memoryMb: 4, phaseSource: 'pane heuristic',
  });
  assert.deepEqual(calls[0][1].slice(0, 2), ['-L', 'demo']);
});

test('scan deduplicates linked panes and limits capture concurrency', async () => {
  let captures = 0;
  let active = 0;
  let maxActive = 0;
  const runner = async (file, args) => {
    if (file === 'tmux' && args.includes('list-panes')) return { stdout: [
      '%1\t100\t$0\tone\t0\t0\t/tmp/one\tbash',
      '%1\t100\t$1\ttwo\t0\t0\t/tmp/one\tbash',
      '%2\t200\t$2\tthree\t0\t0\t/tmp/two\tbash',
      '%3\t300\t$3\tfour\t0\t0\t/tmp/three\tbash',
      '%4\t400\t$4\tfive\t0\t0\t/tmp/four\tbash',
      '%5\t500\t$5\tsix\t0\t0\t/tmp/five\tbash',
    ].join('\n') + '\n', stderr: '' };
    if (file === 'ps') return { stdout: [
      '100 1 0.1 100 bash bash', '101 100 1 1024 claude claude',
      '200 1 0.1 100 bash bash', '201 200 1 1024 codex codex',
      '300 1 0.1 100 bash bash', '301 300 1 1024 gemini gemini',
      '400 1 0.1 100 bash bash', '401 400 1 1024 claude claude',
      '500 1 0.1 100 bash bash', '501 500 1 1024 codex codex',
    ].join('\n'), stderr: '' };
    if (file === 'tmux' && args.includes('capture-pane')) {
      captures += 1;
      active += 1;
      maxActive = Math.max(maxActive, active);
      await new Promise((resolve) => setTimeout(resolve, 2));
      active -= 1;
      return { stdout: 'Working (2s)\n', stderr: '' };
    }
    throw new Error('unexpected command');
  };
  const result = await scan({ execFile: runner });
  assert.equal(result.sessions.length, 5);
  assert.equal(captures, 5);
  assert.ok(maxActive <= 4);
});

test('scan returns an actionable warning when tmux is unavailable', async () => {
  const result = await scan({ execFile: async () => {
    const error = new Error('spawn tmux ENOENT');
    error.code = 'ENOENT';
    throw error;
  } });
  assert.equal(result.sessions.length, 0);
  assert.match(result.warnings[0], /Unable to list tmux panes/);
  assert.match(result.warnings[0], /ENOENT/);
  assert.doesNotThrow(() => new Date(result.scannedAt));
});

test('rankSessions provides stable session, window, pane ordering', () => {
  const sessions = [{ phase: 'unknown', sessionName: 'z', windowIndex: 0, paneIndex: 0 }, { phase: 'permission', sessionName: 'z', windowIndex: 3, paneIndex: 0 }, { phase: 'thinking', sessionName: 'a', windowIndex: 2, paneIndex: 0 }, { phase: 'permission', sessionName: 'a', windowIndex: 1, paneIndex: 3 }];
  assert.deepEqual(rankSessions(sessions).map((item) => [item.phase, item.sessionName, item.windowIndex, item.paneIndex]), [['permission', 'a', 1, 3], ['permission', 'z', 3, 0], ['thinking', 'a', 2, 0], ['unknown', 'z', 0, 0]]);
});

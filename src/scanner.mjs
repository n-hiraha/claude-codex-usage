import { basename } from 'node:path';
import { execFile as execFileCallback } from 'node:child_process';
import { promisify, stripVTControlCharacters } from 'node:util';

const execFile = promisify(execFileCallback);
const PANE_FORMAT = [
  '#{pane_id}', '#{pane_pid}', '#{session_id}', '#{session_name}',
  '#{window_index}', '#{pane_index}', '#{pane_current_path}',
  '#{pane_current_command}',
].join('\t');

const fields = ['paneId', 'panePid', 'sessionId', 'sessionName', 'windowIndex',
  'paneIndex', 'cwd', 'currentCommand'];

/** Parse the fixed-width ps output used by scan(). */
export function parseProcesses(output = '') {
  const processes = [];
  for (const line of String(output).split('\n')) {
    const match = line.match(/^\s*(\d+)\s+(\d+)\s+([\d.]+)\s+(\d+)\s+(\S+)(?:\s+(.*?))?\s*$/);
    if (!match) continue;
    const [, pid, ppid, cpu, rss, comm, args = ''] = match;
    processes.push({ pid: Number(pid), ppid: Number(ppid), cpu: Number(cpu), rss: Number(rss), comm, args });
  }
  return processes;
}

// Only inspect the executable (or a node script path), so `sh -c "claude"`
// and arbitrary prompt text do not make a pane look like an agent.
export function detectAgent(process) {
  if (!process) return null;
  const args = String(process.args || '').trim();
  let comm = basename(String(process.comm || '')).toLowerCase();
  // macOS truncates the non-final comm column (often to 16 characters).
  // Recover only from argv[0], never from prompt text or later arguments.
  if (String(process.comm).includes('/') && !agentFromName(comm) && comm !== 'node') {
    comm = basename(args.split(/\s+/)[0] || '').toLowerCase();
  }
  if (['bash', 'zsh', 'sh', 'fish', 'tmux', 'sshd', 'login', 'node', 'npm', 'yarn', 'pnpm'].includes(comm)) {
    if (comm !== 'node') return null;
    const script = nodeScriptPath(args);
    return agentFromName(script);
  }
  return agentFromName(comm);
}

function agentFromName(value) {
  const raw = String(value).toLowerCase();
  const name = basename(raw).replace(/\.(?:js|mjs|cjs|bin)$/, '');
  // Keep this allowlist narrow: a project called `codex` must not match.
  if (/(?:^|[\\/])(?:claude|claude-code)(?:[\\/]cli)?(?:\.(?:js|mjs|cjs))?$/.test(raw)
    || /\/@anthropic-ai\/claude-code\/cli(?:\.(?:js|mjs|cjs))?$/.test(raw)) return 'claude';
  if (/(?:^|[\\/])(?:codex|codex-cli)(?:[\\/]bin)?(?:\.(?:js|mjs|cjs))?$/.test(raw)
    || /\/@openai\/codex\/bin\/codex(?:\.(?:js|mjs|cjs))?$/.test(raw)) return 'codex';
  if (/(?:^|[\\/])(?:gemini|gemini-cli)(?:[\\/]dist[\\/]index)?(?:\.(?:js|mjs|cjs))?$/.test(raw)
    || /\/@google\/gemini-cli\/dist\/index(?:\.(?:js|mjs|cjs))?$/.test(raw)) return 'gemini';
  if (name === 'claude' || name === 'claude-code' || name === 'claude_cli') return 'claude';
  if (name === 'codex' || name === 'codex-cli') return 'codex';
  if (name === 'gemini' || name === 'gemini-cli') return 'gemini';
  return null;
}

function nodeScriptPath(args) {
  const argv = String(args).trim().split(/\s+/).filter(Boolean);
  // ps args generally includes argv[0] (`node`) before the script path. Node's
  // eval/print modes have no script path, and these options consume a value.
  for (let index = 1; index < argv.length; index += 1) {
    const arg = argv[index];
    if (/^(?:--(?:eval|print)(?:=|$)|-[ep])/.test(arg)) return '';
    if (['-r', '--require', '--loader', '--experimental-loader', '--import'].includes(arg)) {
      index += 1;
      continue;
    }
    if (arg.startsWith('-')) continue;
    return arg;
  }
  return '';
}

/** Parse tmux list-panes output while retaining empty fields. */
export function parsePanes(output = '') {
  return String(output).split('\n').filter((line) => line.length > 0).map((line) => {
    const values = line.split('\t');
    return Object.fromEntries(fields.map((field, index) => [field, values[index] ?? '']));
  });
}

export function inferPhase(capture = '') {
  const lines = stripVTControlCharacters(String(capture)).split('\n').filter((line) => line.trim()).slice(-12);
  const active = [];
  for (const [index, line] of lines.entries()) {
    if (/^\s*(?:thinking|reasoning|working)(?:\s*\([^\n]*\))?[.… ]*$/i.test(line)) active.push({ index, phase: 'thinking' });
    if (/(?:^|\s)(?:[⏺●▶▸]|[-*])\s*(?:tool|running|executing)\b|\b(?:running|executing)\s+(?:command|tool)\b/i.test(line)) active.push({ index, phase: 'tool' });
    // Require a question-shaped approval request or explicit numbered choices.
    if (/^\s*(?:approval:\s*)?(?:would you like to proceed\?|(?:do you want|would you like) to (?:run|execute|allow)\b.*\?|allow (?:this|the)\b[^\n]*\?|approve (?:this|the)\b[^\n]*\?)/i.test(line)) active.push({ index, phase: 'permission' });
    if (/^\s*(?:done|finished|completed|idle)\s*[.!]?\s*$/i.test(line)) active.push({ index, phase: 'done' });
  }
  if (active.length) return active.sort((a, b) => b.index - a.index)[0].phase;
  for (const line of lines.slice().reverse()) {
    if (/^\s*(?:done|finished|completed|idle)\s*[.!]?\s*$/i.test(line)
      || /^\s*[❯›>]\s*$/.test(line)) return 'done';
  }
  return 'unknown';
}

export function rankSessions(sessions = []) {
  const phaseRank = { permission: 0, thinking: 1, tool: 2, done: 3, unknown: 4 };
  return [...sessions].sort((a, b) => (phaseRank[a.phase] ?? 4) - (phaseRank[b.phase] ?? 4)
    || String(a.sessionName).localeCompare(String(b.sessionName))
    || Number(a.windowIndex) - Number(b.windowIndex)
    || Number(a.paneIndex) - Number(b.paneIndex));
}

function descendants(rootPid, processes) {
  const byParent = new Map();
  for (const process of processes) {
    const list = byParent.get(process.ppid) || [];
    list.push(process);
    byParent.set(process.ppid, list);
  }
  const result = [];
  const queue = [{ pid: Number(rootPid), depth: 0 }];
  const seen = new Set();
  while (queue.length) {
    const current = queue.shift();
    if (seen.has(current.pid)) continue;
    seen.add(current.pid);
    const process = processes.find((item) => item.pid === current.pid);
    if (process) result.push({ process, depth: current.depth });
    for (const child of byParent.get(current.pid) || []) queue.push({ pid: child.pid, depth: current.depth + 1 });
  }
  return result;
}

export async function scan({ socket, execFile: runner = execFile } = {}) {
  const warnings = [];
  const tmuxArgs = socket ? ['-L', socket] : [];
  let paneOutput;
  try {
    ({ stdout: paneOutput } = await runner('tmux', [...tmuxArgs, 'list-panes', '-a', '-F', PANE_FORMAT], { timeout: 5000, maxBuffer: 1024 * 1024 }));
  } catch (error) {
    const detail = error?.stderr?.trim() || error?.message || 'unknown error';
    warnings.push(`Unable to list tmux panes: ${detail}`);
    return { sessions: [], warnings, scannedAt: new Date().toISOString() };
  }
  let processOutput = '';
  try {
    ({ stdout: processOutput } = await runner('ps', ['-ww', '-axo', 'pid=,ppid=,pcpu=,rss=,comm=,args='], { timeout: 5000, maxBuffer: 1024 * 1024 }));
  } catch (error) {
    warnings.push(`Unable to inspect processes: ${error?.stderr?.trim() || error?.message || 'unknown error'}`);
  }
  const processes = parseProcesses(processOutput);
  const panes = [...new Map(parsePanes(paneOutput).map((pane) => [pane.paneId, pane])).values()];
  const sessions = [];
  const inspectPane = async (pane) => {
    const candidates = descendants(pane.panePid, processes)
      .map(({ process, depth }) => ({ process, depth, agent: detectAgent(process) }))
      .filter((item) => item.agent)
      .sort((a, b) => a.depth - b.depth || a.process.pid - b.process.pid);
    const selected = candidates[0];
    if (!selected) return null;
    let capture = '';
    try {
      ({ stdout: capture } = await runner('tmux', [...tmuxArgs, 'capture-pane', '-p', '-t', pane.paneId, '-S', '-30'], { timeout: 5000, maxBuffer: 1024 * 1024 }));
    } catch (error) {
      warnings.push(`Unable to capture pane ${pane.paneId}: ${error?.stderr?.trim() || error?.message || 'unknown error'}`);
    }
    return {
      id: pane.paneId, paneId: pane.paneId, pid: selected.process.pid, agent: selected.agent,
      project: basename(pane.cwd) || pane.sessionName, cwd: pane.cwd,
      sessionId: pane.sessionId, sessionName: pane.sessionName,
      windowIndex: Number(pane.windowIndex), paneIndex: Number(pane.paneIndex),
      phase: inferPhase(capture), cpu: selected.process.cpu,
      memoryMb: selected.process.rss / 1024, phaseSource: 'pane heuristic',
    };
  };
  // Capture at most four panes concurrently so a large tmux server remains responsive.
  let next = 0;
  const worker = async () => {
    const result = [];
    while (next < panes.length) {
      const pane = panes[next++];
      const session = await inspectPane(pane);
      if (session) result.push(session);
    }
    return result;
  };
  const results = await Promise.all(Array.from({ length: Math.min(4, panes.length) }, worker));
  sessions.push(...results.flat());
  return { sessions: rankSessions(sessions), warnings, scannedAt: new Date().toISOString() };
}

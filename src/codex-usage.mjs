import { spawn } from 'node:child_process';

const MAX_BUFFER = 1024 * 1024;
const MAX_LINE = 256 * 1024;

const numberOrNull = value => typeof value === 'number' && Number.isFinite(value) && value >= 0 ? value : null;
const textOrNull = value => typeof value === 'string' ? value : null;

function identityOf(account) {
  if (!account || typeof account !== 'object') return { type: null, email: null, planType: null };
  return { type: textOrNull(account.type), email: textOrNull(account.email), planType: textOrNull(account.planType) };
}

function bucketOf(bucket) {
  if (!bucket || typeof bucket !== 'object') return null;
  return {
    usedPercent: numberOrNull(bucket.usedPercent),
    windowDurationMins: numberOrNull(bucket.windowDurationMins),
    resetsAt: numberOrNull(bucket.resetsAt),
  };
}

function limitsOf(result) {
  const source = result?.rateLimitsByLimitId && typeof result.rateLimitsByLimitId === 'object'
    ? Object.entries(result.rateLimitsByLimitId)
    : result?.rateLimits && typeof result.rateLimits === 'object'
      ? [[result.rateLimits.limitId ?? 'default', result.rateLimits]] : [];
  return source.map(([id, item]) => ({
    id: textOrNull(item?.limitId) ?? String(id),
    name: textOrNull(item?.limitName),
    primary: bucketOf(item?.primary),
    secondary: bucketOf(item?.secondary),
  }));
}

function usageOf(result) {
  if (!result || typeof result !== 'object') return null;
  const summary = result.summary && typeof result.summary === 'object' ? {
    lifetimeTokens: numberOrNull(result.summary.lifetimeTokens),
    peakDailyTokens: numberOrNull(result.summary.peakDailyTokens),
  } : null;
  const daily = Array.isArray(result.dailyUsageBuckets)
    ? result.dailyUsageBuckets.filter(item => item && typeof item === 'object'
      && typeof item.startDate === 'string' && /^\d{4}-\d{2}-\d{2}$/.test(item.startDate) && numberOrNull(item.tokens) !== null).map(item => ({
        startDate: item.startDate, tokens: numberOrNull(item.tokens),
      })).sort((a, b) => a.startDate.localeCompare(b.startDate)) : null;
  return { summary, dailyUsageBuckets: daily };
}

function safeError(message) {
  const error = new Error(message);
  error.code = 'CODEX_USAGE_ERROR';
  return error;
}

/** Read one isolated Codex account's quota data through the app-server protocol. */
export async function readCodexUsage({ codexHome, executable = 'codex', timeoutMs = 10000, spawnImpl = spawn } = {}) {
  if (typeof codexHome !== 'string' || !codexHome) throw safeError('CODEX_HOME is required');
  let child;
  try {
    child = spawnImpl(executable, ['app-server', '--listen', 'stdio://'], {
      env: { ...process.env, CODEX_HOME: codexHome }, stdio: ['pipe', 'pipe', 'pipe'], shell: false,
    });
  } catch { throw safeError('Unable to start Codex app-server'); }
  if (!child?.stdout || !child?.stdin) {
    child?.kill?.();
    throw safeError('Codex app-server has no stdio transport');
  }

  let closed = false;
  let cleaned = false;
  let buffered = '';
  let total = 0;
  let nextId = 1;
  const deadline = Date.now() + Math.max(1, Number(timeoutMs) || 10000);
  const pending = new Map();
  const warnings = [];
  let rejectClosed;
  const processClosed = new Promise((_, reject) => { rejectClosed = reject; });
  const fail = () => { if (!closed) { closed = true; rejectClosed(safeError('Codex app-server exited')); } };
  const onData = chunk => {
    if (closed) return;
    total += Buffer.byteLength(chunk);
    if (total > MAX_BUFFER) { fail(); return; }
    buffered += String(chunk);
    if (buffered.length > MAX_LINE * 2) { fail(); return; }
    let index;
    while ((index = buffered.indexOf('\n')) >= 0) {
      const line = buffered.slice(0, index).trim(); buffered = buffered.slice(index + 1);
      if (!line) continue;
      if (line.length > MAX_LINE) { fail(); return; }
      let message;
      try { message = JSON.parse(line); } catch {
        if (!warnings.includes('Ignored malformed app-server message')) warnings.push('Ignored malformed app-server message');
        continue;
      }
      if (message && typeof message.method === 'string' && message.id !== undefined) {
        // The server must never be allowed to make an interactive request.
        try { child.stdin.write(JSON.stringify({ id: message.id, error: { code: -32601, message: 'Method not supported' } }) + '\n'); } catch { fail(); }
        continue;
      }
      if (message?.id !== undefined && pending.has(message.id)) {
        const done = pending.get(message.id); pending.delete(message.id); done(message);
      }
    }
  };
  child.stdout.on('data', onData);
  child.stdout.setEncoding?.('utf8');
  child.stderr?.resume?.();
  child.stdout.on('error', fail);
  child.stdin.on?.('error', fail);
  child.on?.('error', fail);
  child.on?.('exit', fail);

  const cleanup = async () => {
    if (cleaned) return;
    cleaned = true;
    closed = true;
    for (const item of pending.values()) item({ error: { message: 'closed' } });
    pending.clear();
    if (child.exitCode != null || child.signalCode != null) return;
    await new Promise(resolve => {
      const timer = setTimeout(() => {
        try { child.kill?.('SIGKILL'); } catch {}
        child.stdin.destroy?.(); child.stdout.destroy?.(); child.stderr?.destroy?.();
        resolve();
      }, 500);
      child.once?.('exit', () => { clearTimeout(timer); resolve(); });
      try { child.stdin.end(); } catch {}
      try { child.kill?.(); } catch { clearTimeout(timer); resolve(); }
    });
  };
  const request = (method, params) => new Promise((resolve, reject) => {
    if (closed) return reject(safeError('Codex app-server closed'));
    const id = nextId++;
    const remaining = Math.max(1, deadline - Date.now());
    const timer = setTimeout(() => { pending.delete(id); reject(safeError(`Codex request timed out (${method})`)); }, remaining);
    pending.set(id, message => { clearTimeout(timer); if (message.error) reject(safeError(`Codex request failed (${method})`)); else resolve(message.result ?? {}); });
    try { child.stdin.write(JSON.stringify({ method, id, params }) + '\n'); } catch { clearTimeout(timer); pending.delete(id); reject(safeError(`Codex request failed (${method})`)); }
  });
  const notify = method => { try { child.stdin.write(JSON.stringify({ method, params: {} }) + '\n'); } catch { throw safeError('Codex app-server communication failed'); } };
  try {
    await Promise.race([request('initialize', { clientInfo: { name: 'tmux-ai-monitor', title: 'tmux AI Monitor', version: '0.2.0' }, capabilities: null }), processClosed]);
    notify('initialized');
    const first = await Promise.race([request('account/read', { refreshToken: false }), processClosed]);
    const before = identityOf(first.account);
    if (before.type === 'chatgpt' && before.email === null) warnings.push('Account email unavailable; identity changes cannot be fully verified');
    let limits = [];
    let usage = null;
    if (before.type === null && before.email === null && before.planType === null) {
      warnings.push('Codex account unavailable');
    } else {
      try { limits = limitsOf(await Promise.race([request('account/rateLimits/read', {}), processClosed])); }
      catch { warnings.push('Rate limits unavailable'); }
      try { usage = usageOf(await Promise.race([request('account/usage/read', {}), processClosed])); }
      catch { warnings.push('Token usage unavailable'); }
    }
    const last = await Promise.race([request('account/read', { refreshToken: false }), processClosed]);
    const after = identityOf(last.account);
    if (before.type !== after.type || before.email !== after.email) throw safeError('Account changed while reading Codex usage');
    return {
      identity: before,
      limits,
      summary: usage?.summary ?? null,
      dailyUsageBuckets: usage?.dailyUsageBuckets ?? null,
      warnings,
    };
  } finally { await cleanup(); }
}

export default readCodexUsage;

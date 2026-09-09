import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { readFile } from 'node:fs/promises';
import { homedir, userInfo } from 'node:os';
import { resolve, join } from 'node:path';
import { createHash } from 'node:crypto';

const exec = promisify(execFile);
const endpoint = 'https://api.anthropic.com/api/oauth/usage';
const failure = message => Object.assign(new Error(message), { code: 'CLAUDE_USAGE_ERROR' });

export function claudeKeychainService(claudeHome) {
  const home = resolve(claudeHome).normalize('NFC');
  const suffix = home === resolve(homedir(), '.claude') ? '' : `-${createHash('sha256').update(home).digest('hex').slice(0, 8)}`;
  return `Claude Code-credentials${suffix}`;
}

function windowOf(value, minutes) {
  if (!value || typeof value !== 'object') return null;
  const used = value.utilization;
  const reset = typeof value.resets_at === 'string' ? Date.parse(value.resets_at) / 1000 : null;
  return {
    usedPercent: typeof used === 'number' && Number.isFinite(used) && used >= 0 ? used : null,
    windowDurationMins: minutes,
    resetsAt: Number.isFinite(reset) ? reset : null,
  };
}

export function normalizeClaudeLimits(data) {
  return [{ id: 'claude', name: 'Claude', primary: windowOf(data?.five_hour, 300), secondary: windowOf(data?.seven_day, 10080) }];
}

// Access only the chosen Claude profile's credential, and send it only to
// Anthropic's fixed HTTPS usage endpoint. Never return or persist the token.
export async function readClaudeUsage({ claudeHome, runner = exec, read = readFile, fetchImpl = fetch, platform = process.platform } = {}) {
  if (typeof claudeHome !== 'string' || !claudeHome) throw failure('Claude home is required');
  const home = resolve(claudeHome);
  const env = { ...process.env };
  delete env.CLAUDE_SECURESTORAGE_CONFIG_DIR;
  if (home === resolve(homedir(), '.claude')) delete env.CLAUDE_CONFIG_DIR;
  else env.CLAUDE_CONFIG_DIR = home;
  // An API key or gateway in the invoking shell must not override this profile.
  for (const key of ['ANTHROPIC_API_KEY', 'ANTHROPIC_AUTH_TOKEN', 'CLAUDE_CODE_OAUTH_TOKEN', 'CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR', 'ANTHROPIC_BASE_URL']) delete env[key];
  const auth = async () => {
    try {
      const { stdout } = await runner('claude', ['auth', 'status', '--json'], { env, timeout: 5000, maxBuffer: 65536 });
      const data = JSON.parse(stdout);
      if (!data.loggedIn || data.authMethod !== 'claude.ai' || !data.email) throw failure('not signed in');
      return { email: data.email, orgId: data.orgId ?? null, planType: data.subscriptionType ?? null };
    } catch { throw failure('Claude subscription login unavailable'); }
  };
  const credential = async () => {
    let raw;
    if (platform === 'darwin') {
      try {
        const account = /^[a-zA-Z0-9._-]+$/.test(process.env.USER || '') ? process.env.USER : userInfo().username;
        ({ stdout: raw } = await runner('/usr/bin/security', ['find-generic-password', '-a', account, '-s', claudeKeychainService(home), '-w'], { timeout: 5000, maxBuffer: 65536 }));
      } catch { /* The selected profile may use file storage instead. */ }
    }
    try {
      if (!raw) raw = await read(join(home, '.credentials.json'), 'utf8');
      if (raw.length > 65536) throw failure('oversized credentials');
      const value = JSON.parse(raw).claudeAiOauth;
      if (typeof value?.accessToken !== 'string' || !value.accessToken) throw failure('missing token');
      return value.accessToken;
    } catch { throw failure('Claude credential unavailable'); }
  };
  try {
    const before = await auth();
    const token = await credential();
    const response = await fetchImpl(endpoint, {
      method: 'GET', redirect: 'error', signal: AbortSignal.timeout(10000),
      headers: { Authorization: `Bearer ${token}`, 'anthropic-beta': 'oauth-2025-04-20', Accept: 'application/json', 'User-Agent': 'tmux-ai-monitor/0.3.0' },
    });
    if (!response.ok) throw failure(response.status === 429 ? 'Claude usage rate limited; retry later' : 'Claude usage unavailable');
    // Keep malformed or unexpected server output out of terminal/log messages.
    const reader = response.body.getReader();
    let body = '';
    const decoder = new TextDecoder();
    let bytes = 0;
    try {
      while (true) {
        const { done, value } = await reader.read();
        if (done) break;
        bytes += value.byteLength;
        if (bytes > 65536) throw failure('Claude usage response too large');
        body += decoder.decode(value, { stream: true });
      }
      body += decoder.decode();
    } finally { await reader.cancel().catch(() => {}); }
    const data = JSON.parse(body);
    const after = await auth();
    if (before.email !== after.email || before.orgId !== after.orgId || token !== await credential()) throw failure('Claude account changed during usage read');
    const limits = normalizeClaudeLimits(data);
    return {
      identity: { type: 'claude.ai', email: before.email, planType: before.planType }, limits,
      summary: null, dailyUsageBuckets: null,
      warnings: limits[0].primary || limits[0].secondary ? [] : ['Claude rate limit windows unavailable'],
    };
  } catch (error) {
    if (error.code === 'CLAUDE_USAGE_ERROR') throw error;
    throw failure('Claude usage request failed');
  }
}

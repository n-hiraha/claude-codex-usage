import { mkdir, readFile, writeFile, rename, rm, stat } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import { homedir } from 'node:os';
import { join } from 'node:path';
import { collectUsage } from './accounts.mjs';

export const defaultCacheDirectory = () => join(process.env.XDG_CACHE_HOME || join(homedir(), '.cache'), 'tmux-ai-monitor');

// Cache quota windows only, never identities, emails, credentials or token history.
function quotaOnly(result) {
  return { scannedAt: result.scannedAt, accounts: result.accounts.map(account => ({
    name: account.name, provider: account.provider,
    ...(account.error ? { error: 'usage unavailable' } : { limits: account.limits ?? [] }),
  })) };
}

export async function cachedUsage(accounts, { directory = defaultCacheDirectory(), now = Date.now(), ttlMs = 60000, fetchUsage = collectUsage } = {}) {
  await mkdir(directory, { recursive: true, mode: 0o700 });
  const key = createHash('sha256').update(JSON.stringify(accounts)).digest('hex').slice(0, 24);
  const path = join(directory, `${key}.json`);
  const lock = join(directory, `${key}.lock`);
  let cached;
  try {
    const value = JSON.parse(await readFile(path, 'utf8'));
    if (Number.isFinite(value.savedAt) && Array.isArray(value.result?.accounts)) cached = value;
  } catch { /* First run or interrupted previous write. */ }
  if (cached && now >= cached.savedAt && now - cached.savedAt < ttlMs) return { ...cached.result, stale: false };
  let locked = false;
  try {
    await mkdir(lock, { mode: 0o700 });
    locked = true;
  } catch (error) {
    if (error.code !== 'EEXIST') throw error;
    // A crashed process cannot prevent future refreshes indefinitely.
    // Normal reads finish within 20 profiles * 10 seconds plus cleanup.
    const age = now - (await stat(lock).catch(() => ({ mtimeMs: now }))).mtimeMs;
    if (age > 300000) await rm(lock, { recursive: true, force: true });
    return cached ? { ...cached.result, stale: true } : {
      scannedAt: new Date(now).toISOString(), loading: true, stale: false,
      accounts: accounts.map(account => ({ name: account.name, provider: account.provider, error: 'loading' })),
    };
  }
  const temporary = `${path}.${process.pid}.tmp`;
  try {
    const result = quotaOnly(await fetchUsage(accounts));
    await writeFile(temporary, JSON.stringify({ savedAt: now, result }), { mode: 0o600 });
    await rename(temporary, path);
    return { ...result, stale: false };
  } finally {
    await rm(temporary, { force: true });
    if (locked) await rm(lock, { recursive: true, force: true });
  }
}

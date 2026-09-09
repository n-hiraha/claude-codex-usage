import { readFile, realpath, stat } from 'node:fs/promises';
import { homedir } from 'node:os';
import { isAbsolute, resolve } from 'node:path';
import { readCodexUsage } from './codex-usage.mjs';
import { clean } from './display.mjs';

export async function loadAccounts(file, env = process.env) {
  if (!file) return [{ name: 'default', provider: 'codex', codexHome: env.CODEX_HOME || resolve(homedir(), '.codex') }];
  if ((await stat(file)).size > 64 * 1024) throw new Error('アカウント設定が大きすぎます（上限64KB）。');
  let config;
  try { config = JSON.parse(await readFile(file, 'utf8')); }
  catch { throw new Error('アカウント設定をJSONとして読み込めません。'); }
  if (!Array.isArray(config?.accounts) || !config.accounts.length || config.accounts.length > 20) throw new Error('accountsには1〜20件の設定が必要です。');
  const names = new Set();
  const homes = new Set();
  const result = [];
  for (const account of config.accounts) {
    if (!account || typeof account.name !== 'string' || !account.name.trim() || account.name.length > 80 || names.has(account.name)) throw new Error('アカウント名は重複しない1〜80文字で指定してください。');
    if (account.provider !== 'codex') throw new Error('アカウント別usageは現在 provider: codex に対応しています。');
    if (typeof account.codexHome !== 'string' || !isAbsolute(account.codexHome)) throw new Error('codexHomeは絶対パスで指定してください。');
    if (account.expectedEmail !== undefined && (typeof account.expectedEmail !== 'string' || !account.expectedEmail.trim())) throw new Error('expectedEmailには空でない文字列を指定してください。');
    const home = await realpath(account.codexHome).catch(() => resolve(account.codexHome));
    if (homes.has(home)) throw new Error('同じcodexHomeが重複しています。アカウントごとに別のディレクトリを指定してください。');
    names.add(account.name);
    homes.add(home);
    result.push({ name: account.name, provider: 'codex', codexHome: home, ...(account.expectedEmail ? { expectedEmail: account.expectedEmail } : {}) });
  }
  return result;
}

export async function collectUsage(accounts, { read = readCodexUsage } = {}) {
  const results = [];
  // Serial on purpose: avoid concurrent credential refreshes in shared stores.
  for (const account of accounts) {
    try {
      const data = await read({ codexHome: account.codexHome });
      if (account.expectedEmail && account.expectedEmail.toLowerCase() !== data.identity?.email?.toLowerCase()) {
        results.push({ name: account.name, provider: account.provider, error: 'ログイン中のメールアドレスがexpectedEmailと一致しません。usageを表示しません。' });
      } else results.push({ name: account.name, provider: account.provider, ...data });
    } catch {
      results.push({ name: account.name, provider: account.provider, error: '取得できません。Codex CLI・指定ホームのログイン状態・ネットワークを確認してください。' });
    }
  }
  const warnings = [];
  const emails = results.map(item => item.identity?.email?.toLowerCase()).filter(Boolean);
  if (new Set(emails).size !== emails.length) warnings.push('同じメールアドレスのログインが複数あります。別枠のusageとは限らないため合算しません。');
  return { scannedAt: new Date().toISOString(), accounts: results, warnings };
}

function windowLabel(minutes) {
  if (!Number.isFinite(minutes) || minutes <= 0) return '期間不明';
  if (minutes % 1440 === 0) return `${minutes / 1440}日枠`;
  if (minutes % 60 === 0) return `${minutes / 60}時間枠`;
  return `${minutes}分枠`;
}

export function renderUsage(result) {
  const lines = ['ACCOUNT USAGE', `${new Date(result.scannedAt).toLocaleString('ja-JP')} 時点`, ''];
  for (const account of result.accounts) {
    lines.push(`${clean(account.name)} · ${clean(account.provider)}`);
    if (account.error) { lines.push(`  ${clean(account.error)}`, ''); continue; }
    lines.push(`  ${clean(account.identity?.email ?? 'メール情報なし')} · ${clean(account.identity?.planType ?? account.identity?.type ?? '不明')}`);
    for (const limit of account.limits ?? []) {
      lines.push(`  ${clean(limit.name || limit.id || '利用枠')}`);
      for (const window of [limit.primary, limit.secondary].filter(Boolean)) {
        const used = window.usedPercent;
        const percentage = Number.isFinite(used) ? `使用 ${used}% / 残り ${Math.max(0, 100 - used)}%` : '使用率不明';
        const reset = Number.isFinite(window.resetsAt) ? new Date(window.resetsAt * 1000).toLocaleString('ja-JP') : '不明';
        lines.push(`    ${windowLabel(window.windowDurationMins)}: ${percentage} · リセット ${reset}`);
      }
      if (!limit.primary && !limit.secondary) lines.push('    利用枠の値は提供されていません。');
    }
    if (!account.limits?.length) lines.push('  利用枠は取得できませんでした。');
    if (Number.isFinite(account.summary?.lifetimeTokens)) lines.push(`  累計トークン: ${account.summary.lifetimeTokens.toLocaleString('ja-JP')}`);
    if (account.dailyUsageBuckets?.length) {
      for (const day of account.dailyUsageBuckets.slice(-7)) lines.push(`  ${clean(day.startDate)}: ${day.tokens.toLocaleString('ja-JP')} tokens`);
    }
    for (const warning of account.warnings ?? []) lines.push(`  注意: ${clean(warning)}`);
    lines.push('');
  }
  for (const warning of result.warnings ?? []) lines.push(`注意: ${clean(warning)}`);
  return lines.join('\n');
}

export function demoUsage() {
  const now = Date.now();
  return { scannedAt: new Date(now).toISOString(), warnings: ['デモデータです。実際のアカウント使用量ではありません。'], accounts: ['personal', 'work'].map((name, i) => ({
    name, provider: 'codex', identity: { type: 'chatgpt', email: `${name}@example.com`, planType: 'pro' },
    limits: [{ id: 'codex', primary: { usedPercent: 24 + i * 40, windowDurationMins: 300, resetsAt: Math.floor(now / 1000) + 7200 }, secondary: { usedPercent: 38 + i * 20, windowDurationMins: 10080, resetsAt: Math.floor(now / 1000) + 172800 } }],
    summary: { lifetimeTokens: 1234567 + i * 100000 }, warnings: [],
  })) };
}

import { tmuxText } from './display.mjs';

const COLORS = { good: '#166534', warn: '#a16207', high: '#b91c1c', unknown: '#a16207' };

function visibleWidth(value) {
  // Gauge text is deliberately ASCII/one-cell Unicode. Account names are
  // measured conservatively so wide CJK names still fit in a status line.
  let width = 0;
  for (const char of String(value)) width += /[\u1100-\u115f\u2329\u232a\u2e80-\u303e\u3040-\u30ff\u3400-\ua4cf\uac00-\ud7a3\uf900-\ufaff\ufe10-\ufe19\ufe30-\ufe6f\uff00-\uff60\uffe0-\uffe6]/u.test(char) ? 2 : 1;
  return width;
}

function shorten(value, width) {
  const text = String(value);
  if (width <= 0) return '';
  let out = '';
  for (const char of text) {
    const next = visibleWidth(out + char);
    if (next > width) break;
    out += char;
  }
  return out === text ? out : `${out.slice(0, Math.max(0, out.length - 1))}…`;
}

function bucket(account) {
  const limits = account?.limits ?? [];
  const id = account?.provider === 'claude' ? 'claude' : 'codex';
  return limits.find(limit => limit?.id === id) ?? limits.find(limit => !/spark/i.test(String(limit?.id ?? ''))) ?? null;
}

function durationLabel(minutes) {
  if (!Number.isFinite(minutes) || minutes <= 0) return '';
  if (minutes % 1440 === 0) return `${minutes / 1440}d`;
  if (minutes % 60 === 0) return `${minutes / 60}h`;
  return `${minutes}m`;
}

function countdown(resetsAt, now) {
  if (!Number.isFinite(resetsAt)) return '↻?';
  const date = new Date(resetsAt * 1000);
  if (!Number.isFinite(date.getTime())) return '↻?';
  const clock = `${String(date.getHours()).padStart(2, '0')}:${String(date.getMinutes()).padStart(2, '0')}`;
  return `↻${date.getMonth() + 1}/${date.getDate()} ${clock}${resetsAt * 1000 <= now ? ' 更新待ち' : ''}`;
}

function windowText(window, now, size = 10, reset = true) {
  if (!window) return null;
  const used = Number.isFinite(window.usedPercent) ? Math.max(0, Math.min(100, window.usedPercent)) : null;
  const fill = used === null ? 0 : Math.round(used / 100 * size);
  const gauge = used === null ? '[不明]' : `[${'━'.repeat(fill)}${'·'.repeat(size - fill)}]`;
  const usage = used === null ? '使用?' : `使用${Math.round(used)}%`;
  const duration = durationLabel(window.windowDurationMins);
  return `${duration ? `${duration} ` : ''}${gauge} ${usage}${reset ? ` ${countdown(window.resetsAt, now)}` : ''}`;
}

function colorFor(account, windows) {
  if (account?.error || !windows.length || windows.every(window => !Number.isFinite(window?.usedPercent))) return COLORS.unknown;
  const used = Math.max(...windows.map(window => Number.isFinite(window.usedPercent) ? window.usedPercent : 0));
  return used > 80 ? COLORS.high : used > 50 ? COLORS.warn : COLORS.good;
}

function accountLabel(account) {
  const provider = account?.provider === 'claude' ? 'Claude' : 'Codex';
  const name = shorten(tmuxText(account?.name || 'default'), 12);
  return name === 'default' || name.toLowerCase() === provider.toLowerCase() ? provider : `${provider} ${name}`;
}

function renderAccount(account, { now }) {
  const limit = bucket(account);
  const isClaude = account?.provider === 'claude';
  const fable = isClaude ? account?.limits?.find(item => item.id === 'claude_fable') : null;
  const groups = isClaude ? [{ name: 'All models', limit: { secondary: limit?.secondary } }, { name: 'Fable', limit: { secondary: fable?.secondary } }] : [{ name: '', limit }];
  const windows = groups.flatMap(group => [group.limit?.primary, group.limit?.secondary].filter(Boolean));
  const label = accountLabel(account);
  if (account?.error || !windows.length) return { variants: [`${label} 利用枠不明`, `${account?.provider === 'claude' ? 'Claude' : 'Codex'} 不明`], color: COLORS.unknown };
  const variants = [];
  for (const [size, reset] of [[10, true], [6, true], [6, false], [4, false]]) {
    const text = groups.map(group => {
      const values = [group.limit?.primary, group.limit?.secondary].filter(Boolean);
      return `${group.name ? `${group.name} ` : ''}${values.length ? values.map(window => windowText(window, now, size, reset)).join(' / ') : '未提供'}`;
    }).join(' | ');
    variants.push(`${label} ${text}`);
  }
  if (windows.length > 1) variants.push(`${label} ${isClaude ? 'All models ' : ''}${windowText(windows[0], now, 6, true)} +${windows.length - 1}枠`);
  variants.push(`${label} ${windowText(windows[0], now, 4, false)}`);
  const provider = account?.provider === 'claude' ? 'Claude' : 'Codex';
  variants.push(`${provider} ${windowText(windows[0], now, 4, false)}`);
  variants.push(`${provider} 幅不足`);
  return { variants, color: colorFor(account, windows) };
}

function colored(item) { return `#[fg=${item.color}]${item.text}#[default]`; }

export function renderUsageGauge(result, { width = 100, now = Date.now(), stale = false } = {}) {
  const accounts = Array.isArray(result?.accounts) ? result.accounts : [];
  const targetWidth = Math.max(1, Number.isFinite(width) ? Math.floor(width) : 100);
  const items = accounts.map(account => renderAccount(account, { multiple: accounts.length > 1, now: now instanceof Date ? now.getTime() : now }));
  if (!items.length) items.push({ variants: ['Codex 利用枠不明'], color: COLORS.unknown });
  if (stale) items[0].variants = items[0].variants.map(text => `stale ${text}`);
  const separator = '  ·  ';
  const shown = [];
  let used = 0;
  for (let index = 0; index < items.length; index++) {
    const extra = shown.length ? separator : '';
    const remainingCount = items.length - index - 1;
    const suffix = remainingCount ? ` +${remainingCount}` : '';
    const available = targetWidth - used - visibleWidth(extra) - visibleWidth(suffix);
    if (available < 24 && shown.length) break;
    const text = items[index].variants.find(text => visibleWidth(text) <= available);
    if (!text) break;
    const item = { color: items[index].color, text };
    shown.push(item);
    used += visibleWidth(item.text) + (shown.length > 1 ? visibleWidth(separator) : 0);
    if (used >= targetWidth) break;
  }
  const omitted = items.length - shown.length;
  let text = shown.map(colored).join(separator);
  if (omitted) text += ` +${omitted}`;
  // Keep the output a single tmux status-line field and never allow user data
  // to become a tmux format directive (tmuxText handles # and %).
  return text.replace(/[\r\n]/g, ' ');
}

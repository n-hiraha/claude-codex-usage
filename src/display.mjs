export const phases = {
  permission: { label: '承認待ち', icon: '!', color: 'yellow' },
  thinking: { label: '応答中', icon: '~', color: 'cyan' },
  tool: { label: 'ツール実行', icon: '>', color: 'blue' },
  done: { label: '入力待ち', icon: '+', color: 'green' },
  unknown: { label: '不明', icon: '?', color: 'white' },
};
const names = { claude: 'Claude', codex: 'Codex', gemini: 'Gemini' };
export function clean(value) {
  return stripVTControlCharacters(String(value ?? '')).replace(/[\x00-\x1f\x7f-\x9f]/g, '');
}
export function tmuxText(value) {
  return clean(value).replaceAll('#', '＃').replaceAll('%', '％');
}
export function statusline(sessions, { compact = false } = {}) {
  if (!sessions.length) return 'AI · 0 sessions';
  const counts = Object.entries(names).filter(([key]) => sessions.some(s => s.agent === key))
    .map(([key, name]) => `${name} ${sessions.filter(s => s.agent === key).length}`).join(' · ');
  if (compact) return `AI ${counts}`;
  const pills = sessions.slice(0, 5).map(s => {
    const phase = phases[s.phase] ?? phases.unknown;
    return `#[fg=${phase.color}]${phase.icon} ${tmuxText(s.paneId)} ${tmuxText(s.project).slice(0, 24)}#[default]`;
  });
  return `AI ${counts} │ ${pills.join('  ')}`;
}
export function renderStatus(result, { attention = false, agent } = {}) {
  const sessions = filterSessions(result.sessions, { attention, agent });
  const count = attention || agent ? `${sessions.length}/${result.sessions.length}` : result.sessions.length;
  const lines = ['TMUX AI MONITOR', `${count} sessions · ${new Date(result.scannedAt).toLocaleTimeString('ja-JP')} · 状態は画面からの推定`, ''];
  sessions.forEach((s, index) => {
    const phase = phases[s.phase] ?? phases.unknown;
    const age = Number.isFinite(s.phaseAgeSeconds) ? ` (${formatAge(s.phaseAgeSeconds)})` : '';
    lines.push(`${index + 1}. ${clean(s.paneId)}  ${names[s.agent] ?? clean(s.agent)}  ${phase.icon} ${phase.label}${age}  ${clean(s.project)}`);
    lines.push(`    ${clean(s.sessionName)}:${s.windowIndex}.${s.paneIndex}  CPU ${Number(s.cpu || 0).toFixed(1)}%  MEM ${Number(s.memoryMb || 0).toFixed(0)} MB`);
    lines.push(`    ${clean(s.cwd)}`);
  });
  if (!sessions.length) lines.push(attention || agent ? '条件に一致するセッションはありません。' : 'tmux内にAIセッションが見つかりません。');
  for (const warning of result.warnings ?? []) lines.push(`\n注意: ${clean(warning)}`);
  return lines.join('\n');
}
export function formatAge(seconds) {
  if (seconds < 60) return `${seconds}s`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ${seconds % 60}s`;
  return `${Math.floor(seconds / 3600)}h ${Math.floor(seconds % 3600 / 60)}m`;
}
export function demoScan() {
  return { scannedAt: new Date().toISOString(), warnings: ['デモデータです。実際のセッションではありません。'], sessions: [
    { paneId: '%1', agent: 'claude', phase: 'permission', project: 'api-server', cpu: 0.2, memoryMb: 240 },
    { paneId: '%2', agent: 'codex', phase: 'thinking', project: 'web-app', cpu: 12.8, memoryMb: 180 },
    { paneId: '%3', agent: 'gemini', phase: 'done', project: 'docs', cpu: 0, memoryMb: 135 },
  ].map((s, i) => ({ ...s, id: s.paneId, pid: 1000 + i, cwd: `/demo/${s.project}`, sessionId: '$0', sessionName: 'work', windowIndex: i, paneIndex: 0, phaseSource: 'pane heuristic' })) };
}
import { stripVTControlCharacters } from 'node:util';
import { filterSessions } from './tracker.mjs';

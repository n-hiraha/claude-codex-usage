#!/usr/bin/env node
import { fileURLToPath } from 'node:url';
import { scan } from '../src/scanner.mjs';
import { clean, demoScan, renderStatus, statusline } from '../src/display.mjs';
import { tmuxConfig, jumpToPane } from '../src/integration.mjs';
import { SessionTracker, filterSessions } from '../src/tracker.mjs';

const help = `tmux-ai-monitor — ローカルのAIセッションを見渡す

  status [--json]       一覧を表示
  attention [--json]    承認待ち・入力待ちを表示
  watch                2秒ごとに更新（1〜9で移動、qで終了）
  statusline           tmuxのstatus-right用の1行
  usage-statusline     tmux用の使用量ゲージ（利用枠キャッシュ）
  usage [--accounts FILE] アカウント別の利用枠・トークン使用量
  jump %ID             指定ペインに移動（tmux内で実行）
  setup tmux           設定を標準出力へ生成

  --demo               サンプルデータで表示
  --socket NAME        tmux -L NAME を使用
  --client TTY         jumpの対象クライアントを指定
  --interval SECONDS   watchの更新間隔（1〜60秒）
  --agent NAME         claude / codex / gemini で絞り込み
  --attention          承認待ち・入力待ちだけ表示
  --bell               watchで新しい承認待ち・完了をベル通知

追加パッケージ・APIキー不要。状態判定は画面からの推定です。`;

async function main() {
  const args = process.argv.slice(2);
  const command = args.shift() ?? 'status';
  const options = {};
  const positional = [];
  for (let i = 0; i < args.length; i++) {
    const arg = args[i];
    if (['--demo', '--json', '--attention', '--bell', '--compact'].includes(arg)) options[arg.slice(2)] = true;
    else if (['--socket', '--client', '--interval', '--agent', '--accounts', '--width'].includes(arg)) {
      if (!args[i + 1] || args[i + 1].startsWith('--')) throw new Error(`${arg} の値が必要です。`);
      options[arg.slice(2)] = args[++i];
    } else if (arg.startsWith('-')) throw new Error(`不明なオプション: ${arg}`);
    else positional.push(arg);
  }
  if (['help', '--help', '-h'].includes(command)) return console.log(help);
  if (options.agent && !['claude', 'codex', 'gemini'].includes(options.agent)) throw new Error('--agent は claude / codex / gemini を指定してください。');
  if (options.bell && command !== 'watch') throw new Error('--bell は watch で使用してください。');
  let attention = command === 'attention' || Boolean(options.attention);
  if (command === 'usage-statusline') {
    const width = Number(options.width ?? 100);
    if (!Number.isInteger(width) || width < 20 || width > 10000) throw new Error('--widthは20〜10000の整数で指定してください。');
    try {
      const { loadAccounts, demoUsage } = await import('../src/accounts.mjs');
      const { cachedUsage } = await import('../src/usage-cache.mjs');
      const { renderUsageGauge } = await import('../src/usage-gauge.mjs');
      const result = options.demo ? demoUsage() : await cachedUsage(await loadAccounts(options.accounts));
      console.log(result.loading ? 'AI usage 取得中…' : renderUsageGauge(result, { width, stale: result.stale }));
    } catch { console.log('AI usage 取得不可 · usage コマンドで確認'); }
    return;
  }
  if (command === 'usage') {
    if (positional.length) throw new Error('使い方: usage [--accounts FILE] [--json] [--demo]');
    const { loadAccounts, collectUsage, demoUsage, renderUsage } = await import('../src/accounts.mjs');
    const result = options.demo ? demoUsage() : await collectUsage(await loadAccounts(options.accounts));
    console.log(options.json ? JSON.stringify(result, null, 2) : renderUsage(result));
    if (result.accounts.every(account => account.error)) process.exitCode = 1;
    return;
  }
  if (command === 'setup') {
    if (positional.length !== 1 || positional[0] !== 'tmux') throw new Error('使い方: setup tmux');
    return process.stdout.write(tmuxConfig(fileURLToPath(import.meta.url), process.execPath, options.socket));
  }
  if (command === 'jump') {
    if (options.demo) throw new Error('デモから実際のペインには移動できません。');
    if (positional.length !== 1) throw new Error('使い方: jump %ID');
    if (!process.env.TMUX && !options.client) throw new Error('tmux内から実行するか --client TTY を指定してください。');
    return jumpToPane(positional[0], options);
  }
  if (!['status', 'attention', 'watch', 'statusline'].includes(command)) throw new Error(`不明なコマンド: ${command}`);
  if (positional.length) throw new Error('余分な引数があります。help を参照してください。');
  const read = () => options.demo ? Promise.resolve(demoScan()) : scan({ socket: options.socket });
  const selectedSessions = result => filterSessions(result.sessions, { agent: options.agent, attention });
  const output = result => options.json
    ? JSON.stringify({ ...result, sessions: selectedSessions(result) }, null, 2)
    : command === 'statusline' ? (result.warnings.length && !result.sessions.length && !options.demo ? 'AI · unavailable' : statusline(selectedSessions(result), { compact: options.compact }))
    : renderStatus(result, { attention, agent: options.agent });
  if (command !== 'watch') return console.log(output(await read()));
  const seconds = Number(options.interval ?? 2);
  if (!Number.isFinite(seconds) || seconds < 1 || seconds > 60) throw new Error('--interval は1〜60秒です。');
  if (!process.stdout.isTTY || !process.stdin.isTTY || options.json) return console.log(output(await read()));
  let stopped = false;
  let visibleSessions = [];
  let jumping = false;
  let latestResult;
  let lastEvent;
  const tracker = new SessionTracker();
  const draw = () => {
    if (!latestResult || stopped) return;
    visibleSessions = selectedSessions(latestResult);
    const notice = lastEvent ? `\n最新の変化: ${clean(lastEvent.paneId)} ${clean(lastEvent.project)} → ${lastEvent.type === 'permission' ? '承認待ち' : '入力待ち'} (${new Date(lastEvent.at).toLocaleTimeString('ja-JP')})\n` : '';
    process.stdout.write(`\x1b[H\x1b[2J${output(latestResult)}\n${notice}\nq 終了 · a ${attention ? '全状態へ' : '承認待ち・入力待ちへ'} · ${options.demo ? 'デモ表示（移動・ベル無効）' : '1〜9 で移動'}${options.bell ? ' · ベルON' : ''}\n`);
  };
  let timer;
  let wake;
  const stop = () => { stopped = true; clearTimeout(timer); wake?.(); };
  const key = async data => {
    if (data.includes('q') || data.includes('\x03') || data.includes('\x04')) return stop();
    if (data.toString() === 'a') { attention = !attention; draw(); return; }
    if (!/^[1-9]$/.test(data.toString()) || jumping) return;
    const selected = visibleSessions[Number(data.toString()) - 1];
    if (!selected) return;
    if (options.demo) return;
    jumping = true;
    try {
      await jumpToPane(selected.paneId, options);
      stop();
    } catch {
      process.stdout.write('\n移動できませんでした。tmux内から開き、対象ペインを確認してください。\n');
    } finally { jumping = false; }
  };
  const wasRaw = process.stdin.isRaw;
  process.stdin.setRawMode(true);
  process.stdin.resume();
  process.stdin.on('data', key);
  process.on('SIGINT', stop);
  process.on('SIGTERM', stop);
  process.stdout.write('\x1b[?1049h\x1b[?25l');
  try {
    while (!stopped) {
      const result = tracker.update(await read());
      if (stopped) break;
      latestResult = result;
      const events = result.events.filter(event => !options.agent || event.agent === options.agent);
      if (events.length) {
        lastEvent = events.at(-1);
        if (options.bell && !options.demo) process.stdout.write('\x07');
      }
      draw();
      await new Promise(resolve => { wake = resolve; timer = setTimeout(resolve, seconds * 1000); });
    }
  } finally {
    clearTimeout(timer);
    process.stdout.write('\x1b[?25h\x1b[?1049l');
    process.stdin.setRawMode(wasRaw ?? false);
    process.stdin.removeListener('data', key);
    process.stdin.pause();
    process.removeListener('SIGINT', stop);
    process.removeListener('SIGTERM', stop);
  }
}
main().catch(error => {
  console.error(`tmux-ai-monitor: ${error.message}`);
  process.exitCode = 1;
});

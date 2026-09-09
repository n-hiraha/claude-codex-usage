import test from 'node:test';
import assert from 'node:assert/strict';
import { homedir } from 'node:os';
import { join } from 'node:path';
import { readClaudeUsage, normalizeClaudeLimits, claudeKeychainService } from '../src/claude-usage.mjs';

const token = JSON.stringify({ claudeAiOauth: { accessToken: 'synthetic-token' } });
const account = { loggedIn: true, authMethod: 'claude.ai', email: 'test@example.com', orgId: 'test-org', subscriptionType: 'max' };
const fixture = { five_hour: { utilization: 20, resets_at: '2026-09-09T10:00:00Z' }, seven_day: { utilization: 45, resets_at: '2026-09-15T10:00:00Z' } };

test('Claude windows preserve missing values instead of inventing zero', () => {
  const limits = normalizeClaudeLimits({ five_hour: { utilization: null, resets_at: 'invalid' } });
  assert.equal(limits[0].primary.usedPercent, null);
  assert.equal(limits[0].primary.resetsAt, null);
  assert.equal(limits[0].secondary, null);
});

test('credential stays in memory and only reaches the fixed Anthropic endpoint', async () => {
  const result = await readClaudeUsage({ claudeHome: '/tmp/claude-test', platform: 'linux', read: async () => token,
    runner: async () => ({ stdout: JSON.stringify(account) }),
    fetchImpl: async (url, options) => {
      assert.equal(url, 'https://api.anthropic.com/api/oauth/usage');
      assert.equal(options.redirect, 'error');
      assert.equal(options.headers.Authorization, 'Bearer synthetic-token');
      return new Response(JSON.stringify(fixture));
    },
  });
  assert.equal(result.limits[0].primary.usedPercent, 20);
  assert.equal(result.limits[0].secondary.windowDurationMins, 10080);
  assert.doesNotMatch(JSON.stringify(result), /synthetic-token/);
});

test('macOS reads only the selected credential service and detects login changes', async () => {
  assert.equal(claudeKeychainService(join(homedir(), '.claude')), 'Claude Code-credentials');
  assert.match(claudeKeychainService('/tmp/other-claude'), /^Claude Code-credentials-[a-f0-9]{8}$/);
  let calls = 0;
  await assert.rejects(readClaudeUsage({ claudeHome: '/tmp/other-claude', platform: 'darwin', runner: async (file, args) => {
    if (file === '/usr/bin/security') {
      assert.ok(args.includes(claudeKeychainService('/tmp/other-claude')));
      return { stdout: token };
    }
    return { stdout: JSON.stringify({ ...account, email: calls++ ? 'other@example.com' : account.email }) };
  }, fetchImpl: async () => new Response(JSON.stringify(fixture)) }), /account changed/);
});

test('HTTP and parsing errors never echo sensitive response bodies', async () => {
  for (const response of [new Response('sensitive-response', { status: 429 }), new Response('sensitive-response')]) {
    await assert.rejects(readClaudeUsage({ claudeHome: '/tmp/test', platform: 'linux', read: async () => token,
      runner: async () => ({ stdout: JSON.stringify(account) }), fetchImpl: async () => response,
    }), error => !error.message.includes('sensitive-response'));
  }
});

import test from 'node:test';
import assert from 'node:assert/strict';
import { SessionTracker, filterSessions } from '../src/tracker.mjs';

const snapshot = (phase, pid = 1) => ({ sessions: [{ paneId: '%1', pid, agent: 'claude', phase, project: 'app' }], warnings: [] });

test('only new permission and active-to-done transitions produce alerts', () => {
  const tracker = new SessionTracker();
  assert.deepEqual(tracker.update(snapshot('permission'), 0).events, []);
  assert.deepEqual(tracker.update(snapshot('permission'), 1000).events, []);
  tracker.update(snapshot('thinking'), 2000);
  assert.equal(tracker.update(snapshot('permission'), 3000).events[0].type, 'permission');
  assert.deepEqual(tracker.update(snapshot('done'), 4000).events, []);
  tracker.update(snapshot('tool'), 5000);
  assert.equal(tracker.update(snapshot('done'), 6000).events[0].type, 'done');
  assert.deepEqual(tracker.update(snapshot('done'), 7000).events, []);
});

test('unknown phase and failed scans do not repeat permission alerts', () => {
  const tracker = new SessionTracker();
  tracker.update(snapshot('permission'), 0);
  tracker.update(snapshot('unknown'), 1000);
  tracker.update({ sessions: [], warnings: ['capture failed'] }, 2000);
  assert.deepEqual(tracker.update(snapshot('permission'), 3000).events, []);
  tracker.update(snapshot('thinking'), 4000);
  tracker.update(snapshot('unknown'), 5000);
  assert.equal(tracker.update(snapshot('done'), 6000).events[0].type, 'done');
});

test('phase age resets on change and process replacement starts a fresh baseline', () => {
  const tracker = new SessionTracker();
  tracker.update(snapshot('thinking'), 1000);
  assert.equal(tracker.update(snapshot('thinking'), 11000).sessions[0].phaseAgeSeconds, 10);
  const replaced = tracker.update(snapshot('permission', 2), 12000);
  assert.equal(replaced.sessions[0].phaseAgeSeconds, 0);
  assert.deepEqual(replaced.events, []);
  tracker.update({ sessions: [], warnings: [] }, 13000);
  assert.deepEqual(tracker.update(snapshot('permission', 2), 14000).events, []);
});

test('filtering retains displayed order for numeric jumps', () => {
  const sessions = [
    { paneId: '%1', agent: 'claude', phase: 'thinking' },
    { paneId: '%2', agent: 'codex', phase: 'permission' },
    { paneId: '%3', agent: 'claude', phase: 'done' },
  ];
  assert.deepEqual(filterSessions(sessions, { agent: 'claude', attention: true }).map(s => s.paneId), ['%3']);
  assert.deepEqual(filterSessions(sessions, { attention: true }).map(s => s.paneId), ['%2', '%3']);
});

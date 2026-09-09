const active = new Set(['thinking', 'tool']);
const known = new Set(['permission', 'thinking', 'tool', 'done']);

// State belongs to one watch process. Never store pane text or conversation data.
export class SessionTracker {
  #states = new Map();
  update(result, now = Date.now()) {
    const events = [];
    const seen = new Set();
    const sessions = result.sessions.map(session => {
      const key = `${session.paneId}:${session.pid}:${session.agent}`;
      seen.add(key);
      const previous = this.#states.get(key);
      const lastKnown = previous?.lastKnown;
      if (previous && known.has(session.phase) && session.phase !== lastKnown) {
        const type = session.phase === 'permission' ? 'permission'
          : session.phase === 'done' && active.has(lastKnown) ? 'done' : null;
        if (type) events.push({ type, paneId: session.paneId, agent: session.agent, project: session.project, at: now });
      }
      const state = {
        phase: session.phase,
        phaseSince: previous?.phase === session.phase ? previous.phaseSince : now,
        lastKnown: known.has(session.phase) ? session.phase : lastKnown,
      };
      this.#states.set(key, state);
      return { ...session, phaseSince: new Date(state.phaseSince).toISOString(), phaseAgeSeconds: Math.max(0, Math.floor((now - state.phaseSince) / 1000)) };
    });
    // A failed scan is not evidence that processes have exited.
    if (!result.warnings?.length) {
      for (const key of this.#states.keys()) if (!seen.has(key)) this.#states.delete(key);
    }
    return { ...result, sessions, events };
  }
}

export function filterSessions(sessions, { agent, attention = false } = {}) {
  return sessions.filter(session => (!agent || session.agent === agent)
    && (!attention || ['permission', 'done'].includes(session.phase)));
}

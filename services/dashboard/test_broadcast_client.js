// Run with `node --test services/dashboard/test_broadcast_client.js`.
// No browser, network, npm dependencies or live service are used.
const assert = require('node:assert/strict');
const test = require('node:test');
const view = require('./static/broadcast.js');

function deferred() {
  let resolve, reject;
  const promise = new Promise((yes, no) => { resolve = yes; reject = no; });
  return {promise, resolve, reject};
}

async function flush() {
  await Promise.resolve();
  await Promise.resolve();
}

function pollingFixture(t) {
  const requests = [], data = [], errors = [], timers = new Map();
  let nextID = 0;
  const poller = view.createPoller({
    load(signal) {
      const request = {...deferred(), signal};
      requests.push(request);
      return request.promise;
    },
    onData: value => data.push(value),
    onError: error => errors.push(error),
    interval: 3000,
    setTimer(fn, delay) {
      assert.equal(delay, 3000);
      const id = ++nextID;
      timers.set(id, fn);
      return id;
    },
    clearTimer: id => timers.delete(id),
    controllerFactory: () => new AbortController(),
  });
  t.after(() => poller.stop());
  return {poller, requests, data, errors, timers,
    fireTimer() {
      assert.equal(timers.size, 1);
      const [id, fn] = timers.entries().next().value;
      timers.delete(id);
      fn();
    },
  };
}

test('broadcast separates unknown values from zero and keeps cost provenance', () => {
  for (const missing of [undefined, null, '', '12', NaN, Infinity, -1]) {
    assert.equal(view.formatNumber(missing), '—');
  }
  assert.equal(view.formatNumber(0), '0');
  assert.equal(view.formatNumber(12345), '12,345');
  assert.equal(view.formatNumber(12345, true), '12.3K');
  assert.equal(view.formatCost(null), '未计价');
  assert.equal(view.formatCost({cost_usd: 0, cost_source: 'unknown'}), '未计价');
  assert.equal(view.formatCost({cost_usd: null, cost_source: 'reported'}), '未计价');
  assert.equal(view.formatCost({cost_usd: 0, cost_source: 'reported'}), '$0.00');
  assert.equal(view.formatCost({cost_usd: 1.2, cost_source: 'estimated'}), '约 $1.20');
  assert.equal(view.formatCost({cost_usd: 1.2, cost_source: 'partial'}), '已计价小计 $1.20');
  assert.equal(view.formatCost({cost_usd: 0.0049, cost_source: 'reported'}), '$0.0049');
  assert.equal(view.formatCost({cost_usd: 0.00001, cost_source: 'estimated'}), '约 <$0.0001');
});

test('broadcast never treats cycles or formal plans as accepted prose', () => {
  const snapshot = {status: 'running', stage: 'project-all',
    chapter: {current: 2, completed: 0, total: 3, words: 0},
    planning: {cycle: 23, completed_cycles: 22, planned_chapters: 1},
    execution: {phase: 'assessing'},
    characters: [{state: 'submitted', submitted: true}],
  };
  const current = view.buildView(snapshot);
  assert.equal(current.acceptedPercent, 0);
  assert.equal(current.chapter.completed, 0);
  assert.equal(current.planning.completed_cycles, 22);
  assert.equal(current.rail, 'planning');
  assert.equal(current.hub, '章节收束评估');
  assert.equal(view.buildView({...snapshot, stage: 'rehearse-arc'}).rail, 'rehearsal');
  assert.equal(view.buildView({...snapshot, status: 'error'}).isRunning, false);
  assert.equal(view.buildView({...snapshot, status: 'error'}).hub, '运行需要处理');
  assert.equal(view.buildView({chapter: {completed: 4, total: 3}}).acceptedPercent, 100);
  assert.equal(view.stageLabel('<script>bad()</script>'), '等待阶段信息');
});

test('broadcast does not animate or describe stopped characters as actively deciding', () => {
  const markup = view.renderSnapshot({status: 'error', stage: 'project-all',
    characters: [{name: '林澄', state: 'active', submitted: false, cycle: 26}],
    events: [{id: 'safe', kind: 'stage_failed', stage: 'project-all', time: '2026-09-13T12:30:53Z'}],
  });
  assert.match(markup.cards, /等待恢复执行/);
  assert.doesNotMatch(markup.cards, /is-active|独立观察与决策/);
  assert.match(markup.rail, /halted/);
  assert.match(markup.events, /执行阶段需要处理/);
  assert.match(markup.events, /角色独立细推/);
  assert.equal(view.buildView({status: 'complete'}).isRunning, false);
  assert.equal(view.formatCost({cost_usd: 115.49, cost_source: 'partial', estimated_calls: 163}), '估算小计 $115.49');
});

test('broadcast escapes hostile names and identifiers without rendering private fields', () => {
  const hostile = `"><img src=x onerror='bad()'>&`;
  assert.equal(view.escapeHTML(hostile), '&quot;&gt;&lt;img src=x onerror=&#39;bad()&#39;&gt;&amp;');
  const markup = view.renderSnapshot({status: 'running', stage: 'project-all',
    characters: [{name: hostile, tier: hostile, state: 'revising', round: 2, memory_chapter: 0,
      memory: 'PRIVATE_MEMORY_SHOULD_NOT_RENDER', intended_action: 'PRIVATE_INTENT_SHOULD_NOT_RENDER'}],
    events: [{id: hostile, kind: hostile, time: 'invalid-date', chapter: 2, cycle: 22, round: 1,
      message: 'RAW_LOG_SHOULD_NOT_RENDER', error_text: 'RAW_ERROR_SHOULD_NOT_RENDER'}],
  });
  const html = Object.values(markup).join('\n');
  assert.doesNotMatch(html, /<img|<script|PRIVATE_MEMORY|PRIVATE_INTENT|RAW_LOG|RAW_ERROR/);
  assert.match(markup.cards, /&lt;img/);
  assert.match(markup.cards, /正式记忆 0 章/);
  assert.match(markup.events, /data-event-id="&quot;&gt;&lt;img/);
  assert.match(markup.events, /运行记录已更新/);
  assert.match(markup.events, /时间待确认/);
  assert.match(view.renderSnapshot(null).cards, /角色注册后/);
});

test('broadcast polling never overlaps refreshes within one active generation', async t => {
  const f = pollingFixture(t);
  f.poller.start();
  f.poller.start();
  f.poller.refresh();
  f.poller.refresh();
  assert.equal(f.requests.length, 1);
  assert.equal(f.timers.size, 0);
  f.requests[0].resolve('first');
  await flush();
  assert.deepEqual(f.data, ['first']);
  assert.equal(f.timers.size, 1);
  f.poller.refresh();
  assert.equal(f.requests.length, 2);
  assert.equal(f.timers.size, 0);
  f.poller.refresh();
  assert.equal(f.requests.length, 2);
  f.requests[1].resolve('second');
  await flush();
  assert.deepEqual(f.data, ['first', 'second']);
  assert.equal(f.timers.size, 1);
});

test('stopping cancels polling and ignores a late success or error', async t => {
  for (const rejected of [false, true]) {
    const f = pollingFixture(t);
    f.poller.start();
    f.poller.stop();
    assert.equal(f.poller.isRunning(), false);
    assert.equal(f.requests[0].signal.aborted, true);
    if (rejected) f.requests[0].reject(new Error('old request'));
    else f.requests[0].resolve('old snapshot');
    await flush();
    assert.deepEqual(f.data, []);
    assert.deepEqual(f.errors, []);
    assert.equal(f.timers.size, 0);
    f.poller.refresh();
    assert.equal(f.requests.length, 1);
  }
});

test('a previous book reply and finally cannot release or overwrite the new request', async t => {
  const f = pollingFixture(t);
  f.poller.start();
  f.poller.stop();
  f.poller.start();
  assert.equal(f.requests.length, 2);
  assert.equal(f.requests[0].signal.aborted, true);
  f.requests[0].resolve('previous book');
  await flush();
  assert.deepEqual(f.data, []);
  assert.equal(f.timers.size, 0);
  f.poller.refresh();
  assert.equal(f.requests.length, 2, 'old finally cleared the active replacement request');
  f.requests[1].resolve('selected book');
  await flush();
  assert.deepEqual(f.data, ['selected book']);
  assert.equal(f.timers.size, 1);
});

test('a failed current request reports once and retries through the scheduled timer', async t => {
  const f = pollingFixture(t);
  f.poller.start();
  const failure = new Error('temporary network error');
  f.requests[0].reject(failure);
  await flush();
  assert.deepEqual(f.errors, [failure]);
  assert.deepEqual(f.data, []);
  assert.equal(f.poller.isRunning(), true);
  f.fireTimer();
  assert.equal(f.requests.length, 2);
  f.requests[1].resolve('recovered snapshot');
  await flush();
  assert.deepEqual(f.data, ['recovered snapshot']);
  assert.equal(f.timers.size, 1);
  f.poller.stop();
  assert.equal(f.timers.size, 0);
});

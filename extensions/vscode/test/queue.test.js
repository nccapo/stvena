'use strict';
const { test } = require('node:test');
const assert = require('node:assert/strict');
const { createRequestQueue } = require('../src/queue');

function harness(timeoutMs = 200) {
  const writes = [];
  const acked = new Set();
  let failNext = false;
  const send = createRequestQueue({
    timeoutMs,
    write: async (root, request) => {
      if (failNext) {
        failNext = false;
        throw new Error('not active');
      }
      const value = { id: `${root}-${writes.length + 1}`, ...request };
      writes.push({ root, value });
      return value;
    },
    acknowledged: (_root, id) => acked.has(id),
    delay: ms => new Promise(resolve => setTimeout(resolve, Math.min(ms, 5))),
  });
  return { send, writes, acked, failNext: () => { failNext = true; } };
}

const tick = () => new Promise(resolve => setTimeout(resolve, 20));

test('a second request waits until Stvena acknowledges the first', async () => {
  const h = harness(5000);
  const first = h.send('/repo', { action: 'accept' });
  const second = h.send('/repo', { action: 'reject' });
  const firstValue = await first;
  await tick();
  assert.deepEqual(h.writes.map(w => w.value.action), ['accept'], 'the second overwrote the first');
  h.acked.add(firstValue.id);
  assert.equal((await second).action, 'reject');
  assert.deepEqual(h.writes.map(w => w.value.action), ['accept', 'reject']);
});

test('an unacknowledged request stops blocking after the timeout', async () => {
  const h = harness(60);
  await h.send('/repo', { action: 'review' });
  const started = Date.now();
  await h.send('/repo', { action: 'accept' });
  assert.ok(Date.now() - started >= 50, 'did not wait for the first request at all');
  assert.equal(h.writes.length, 2);
});

test('projects do not wait for each other', async () => {
  const h = harness(5000);
  await h.send('/one', { action: 'accept' });
  await h.send('/two', { action: 'accept' });
  assert.deepEqual(h.writes.map(w => w.root), ['/one', '/two']);
});

test('a failed write rejects its caller and does not block the next', async () => {
  const h = harness(5000);
  h.failNext();
  await assert.rejects(h.send('/repo', { action: 'accept' }), /not active/);
  assert.equal((await h.send('/repo', { action: 'reject' })).action, 'reject');
});

'use strict';

// Stvena reads one request file per project, about every 700 ms, and a new
// request replaces the file. Two requests written between two reads would lose
// the first one silently, which a quick run of keyboard decisions easily does.
// The queue therefore writes one request per project at a time and waits until
// Stvena acknowledges it before writing the next. Not every request is
// acknowledged, so waiting is bounded.
const ackTimeoutMs = 3000;
const checkEveryMs = 50;

// createRequestQueue serialises write(root, request) per project. acknowledged
// reports whether Stvena has published the outcome of a written request id.
function createRequestQueue({ write, acknowledged, timeoutMs = ackTimeoutMs, delay = wait }) {
  const tails = new Map(); // root -> promise that settles once its last request may be replaced

  return function send(root, request) {
    const previous = tails.get(root) || Promise.resolve();
    const written = previous.then(() => write(root, request));
    const settled = written.then(async value => {
      const deadline = Date.now() + timeoutMs;
      while (Date.now() < deadline && !acknowledged(root, value.id)) await delay(checkEveryMs);
    }, () => {});
    tails.set(root, settled);
    // Drop the entry once nothing newer is waiting behind it.
    void settled.then(() => { if (tails.get(root) === settled) tails.delete(root); });
    return written;
  };
}

function wait(ms) {
  return new Promise(resolve => setTimeout(resolve, ms));
}

module.exports = { createRequestQueue, ackTimeoutMs };

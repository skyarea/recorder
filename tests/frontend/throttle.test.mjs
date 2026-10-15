// Tests for the throttle that paces progress writes while the reader scrolls.
// The contract the reader needs: the first call lands at once (so a page
// change is seen immediately) and, when a burst follows, one trailing call
// fires at the window's edge with the most recent arguments. A lone call is
// not followed by a trailing one — there is nothing pending to flush.
import { test } from "node:test";
import assert from "node:assert/strict";
import { setTimeout as sleep } from "node:timers/promises";
import { throttle } from "../../frontend/lib/throttle.mjs";

// A window long enough that the event loop cannot blur the leading and
// trailing edges together, short enough to keep the suite quick.
const WIN = 40;

test("throttle calls the first invocation immediately with its own arguments", async () => {
  const calls = [];
  const wrapped = throttle((...args) => calls.push(args), WIN);

  wrapped("a.pdf", 3);
  // Leading edge is synchronous: by the time this line runs, fn has fired.
  assert.deepEqual(calls, [["a.pdf", 3]]);

  await sleep(WIN * 3);
  // A lone call must not be trailed — nothing arrived during the window.
  assert.deepEqual(calls, [["a.pdf", 3]]);
});

test("throttle coalesces a burst into one trailing call with the last arguments", async () => {
  const calls = [];
  const wrapped = throttle((...args) => calls.push(args), WIN);

  wrapped("a.pdf", 1); // leading, fires now
  wrapped("a.pdf", 2); // within the window, held
  wrapped("a.pdf", 3); // replaces the held arguments
  assert.deepEqual(calls, [["a.pdf", 1]], "no extra call inside the window");

  await sleep(WIN * 2);
  assert.deepEqual(
    calls,
    [["a.pdf", 1], ["a.pdf", 3]],
    "the burst flushes once, with the newest arguments"
  );
});

test("throttle starts a fresh window so a later call leads again", async () => {
  const calls = [];
  const wrapped = throttle((...args) => calls.push(args), WIN);

  wrapped("x", 1);
  await sleep(WIN * 2); // window closed, no trailing (lone call)
  calls.length = 0;

  wrapped("x", 2);
  assert.deepEqual(calls, [["x", 2]], "the next call after idle is a leading call");
});

test("throttle keeps each throttle independent", async () => {
  const a = [];
  const b = [];
  const wa = throttle((v) => a.push(v), WIN);
  const wb = throttle((v) => b.push(v), WIN);

  wa(1);
  wb(2);
  assert.deepEqual(a, [1]);
  assert.deepEqual(b, [2]);
});

test("cancel drops a held trailing call that would land on a new document", async () => {
  const calls = [];
  const wrapped = throttle((...args) => calls.push(args), WIN);

  wrapped("a.pdf", 1); // leading
  wrapped("a.pdf", 2); // held for the trailing flush
  wrapped.cancel(); // the reader closed before the window ended

  await sleep(WIN * 2);
  assert.deepEqual(calls, [["a.pdf", 1]], "only the leading call ever fires");

  // After cancel the window is closed: the next call leads again.
  wrapped("b.pdf", 1);
  assert.deepEqual(calls, [["a.pdf", 1], ["b.pdf", 1]]);
});

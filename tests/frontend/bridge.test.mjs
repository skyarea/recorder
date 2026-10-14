// Tests for the host<->viewer postMessage bridge. The browser side is stubbed
// just enough to run under `node --test`: the protocol modules touch only
// `window`, `parent` and `PDFViewerApplication`.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";

const ORIGIN = "http://127.0.0.1:54321";

let caseCount = 0;

function installEnv() {
  const sent = [];
  const listeners = [];
  globalThis.window = {
    location: { origin: ORIGIN },
    addEventListener: (type, fn) => {
      if (type === "message") listeners.push(fn);
    },
  };
  globalThis.parent = {
    postMessage: (msg, origin) => sent.push({ msg, origin }),
  };
  return {
    sent,
    fire: (event) => listeners.forEach((fn) => fn(event)),
  };
}

// A stand-in for the viewer's own application object, recording what the
// bridge subscribes to so a test can emit viewer events into it. The event bus
// is handed out by start(), the way pdf.js only builds one partway through
// initialization.
function fakeApp() {
  const handlers = {};
  let pages = 0; // pages the viewer can show: none until pagesinit
  let shown = 1;
  const app = {
    pdfDocument: null,
    eventBus: null,
    initializedPromise: null,
    // pdf.js keeps its own page number and refuses to move before it has any
    // pages, so a page written too early is lost rather than queued. The fake
    // has to drop it the same way or a test cannot tell the two apart.
    get page() {
      return shown;
    },
    set page(val) {
      if (pages > 0) shown = val;
    },
    emit: (topic, detail) => {
      if (topic === "pagesinit") pages = Math.max(pages, 1);
      (handlers[topic] || []).forEach((fn) => fn(detail));
    },
    topics: () => Object.keys(handlers).sort(),
  };
  let settle, fail;
  app.initializedPromise = new Promise((res, rej) => {
    settle = res;
    fail = rej;
  });
  app.start = () => {
    app.eventBus = {
      on: (topic, fn) => {
        (handlers[topic] ||= []).push(fn);
      },
    };
    settle();
  };
  app.fail = () => fail(new Error("viewer initialization failed"));
  return app;
}

// The bridge module runs its handshake at import time, so each case needs a
// fresh copy of it; a query string busts the module cache. Callers install
// globalThis.PDFViewerApplication first.
async function importBridge() {
  return import(`../../frontend/viewer-bridge.mjs?case=${++caseCount}`);
}

// Let the microtasks a resolved initializedPromise releases run, so the bridge
// has reached the point where it subscribes.
const settled = () => new Promise((resolve) => setTimeout(resolve, 0));

// Import the bridge against a viewer that has not initialized yet, then finish
// initialization the way pdf.js does. Most cases want a live bridge.
async function importReady() {
  const app = fakeApp();
  globalThis.PDFViewerApplication = app;
  await importBridge();
  app.start();
  await settled();
  return app;
}

test("viewer to host messages name the viewer and target our own origin", async () => {
  const env = installEnv();
  const { Msg, VIEWER_SRC, postToHost } = await import("../../frontend/lib/bridge.mjs");

  postToHost(Msg.Pages, { total: 12 });
  assert.deepEqual(env.sent, [
    { msg: { src: VIEWER_SRC, type: "pages", total: 12 }, origin: ORIGIN },
  ]);
});

test("host to viewer messages reach the iframe's own window", async () => {
  const env = installEnv();
  const { HOST_SRC, Msg, postToViewer } = await import("../../frontend/lib/bridge.mjs");

  const received = [];
  postToViewer(
    { contentWindow: { postMessage: (msg, origin) => received.push({ msg, origin }) } },
    Msg.Goto,
    { page: 7 }
  );
  assert.deepEqual(received, [
    { msg: { src: HOST_SRC, type: "goto", page: 7 }, origin: ORIGIN },
  ]);

  // A frame that has no window yet must not throw.
  postToViewer({}, Msg.Goto, { page: 7 });
});

test("listener drops anything that is not a message from the expected side", async () => {
  const env = installEnv();
  const { HOST_SRC, Msg, VIEWER_SRC, listener } = await import("../../frontend/lib/bridge.mjs");

  const seen = [];
  const handle = listener(VIEWER_SRC, (data) => seen.push(data));

  handle({ origin: ORIGIN, data: { src: VIEWER_SRC, type: Msg.Page, page: 2 } });
  handle({ origin: "http://evil.example", data: { src: VIEWER_SRC, type: Msg.Page, page: 3 } });
  handle({ origin: ORIGIN, data: { src: HOST_SRC, type: Msg.Page, page: 4 } });
  handle({ origin: ORIGIN, data: { src: VIEWER_SRC, type: 42 } });
  handle({ origin: ORIGIN, data: null });
  handle({ origin: ORIGIN });

  assert.deepEqual(seen, [{ src: VIEWER_SRC, type: Msg.Page, page: 2 }]);
  assert.equal(seen.length, 1);
  assert.ok(HOST_SRC !== VIEWER_SRC);
});

// pdf.js publishes its event bus from an async stretch of initialize(), so
// subscribing at import time would depend on microtask timing. The bridge waits
// instead, and says nothing until the bus is really there.
test("the bridge says nothing until pdf.js has built its event bus", async () => {
  const env = installEnv();
  const app = fakeApp();
  globalThis.PDFViewerApplication = app;
  await importBridge();

  assert.deepEqual(env.sent, []);
  assert.deepEqual(app.topics(), []);

  app.start();
  await settled();
  assert.deepEqual(app.topics(), ["pagechanging", "pagesinit", "pagesloaded"]);
  assert.deepEqual(env.sent.map((s) => s.msg.type), ["ready"]);
});

test("a viewer that never finishes initialization leaves the bridge silent", async () => {
  const env = installEnv();
  const app = fakeApp();
  globalThis.PDFViewerApplication = app;
  const errors = [];
  const realError = console.error;
  console.error = (...args) => errors.push(args[0]);
  try {
    await importBridge();
    app.fail();
    await settled();
  } finally {
    console.error = realError;
  }

  assert.deepEqual(env.sent, []);
  assert.match(String(errors[0]), /bridge disabled/);
});

test("the bridge reports readiness, page count and the page on screen", async () => {
  const env = installEnv();
  const app = await importReady();

  app.emit("pagesloaded", { pagesCount: 7 });
  app.emit("pagechanging", { pageNumber: 3 });

  // Readiness is announced when the subscriptions exist, so it always leads.
  assert.deepEqual(env.sent.map((s) => s.msg.type), ["ready", "pages", "page"]);
  const byType = Object.fromEntries(env.sent.map((s) => [s.msg.type, s.msg]));
  assert.equal(byType.ready.src, "recorder");
  assert.equal(byType.pages.total, 7);
  assert.equal(byType.page.page, 3);
});

test("a goto lands at once once the viewer has pages", async () => {
  const env = installEnv();
  const app = await importReady();
  app.pdfDocument = {};
  app.emit("pagesinit");

  env.fire({ origin: ORIGIN, data: { src: "host", type: "goto", page: 5 } });
  assert.equal(app.page, 5);
});

// The document exists a while before the viewer knows its pages, and pdf.js
// throws away a page number written in that window. The bridge has to hold it.
test("a goto between the document opening and its pages is held, not lost", async () => {
  const env = installEnv();
  const app = await importReady();
  app.pdfDocument = {};

  env.fire({ origin: ORIGIN, data: { src: "host", type: "goto", page: 7 } });
  assert.equal(app.page, 1, "the viewer was moved before it had any pages");

  app.emit("pagesinit");
  assert.equal(app.page, 7);
});

test("a goto that arrives before the document is held until pagesinit", async () => {
  const env = installEnv();
  const app = fakeApp();
  globalThis.PDFViewerApplication = app;
  await importBridge();

  // The host restores the saved page as soon as the frame is alive, which is
  // before pdf.js has any pages to show.
  env.fire({ origin: ORIGIN, data: { src: "host", type: "goto", page: 9 } });
  assert.equal(app.page, 1);

  app.pdfDocument = {};
  app.start();
  await settled();
  app.emit("pagesinit");
  assert.equal(app.page, 9);
});

test("a nonsense goto is ignored rather than thrown at the viewer", async () => {
  const env = installEnv();
  const app = await importReady();
  app.pdfDocument = {};
  app.emit("pagesinit");

  for (const page of [0, -3, NaN, undefined, "abc", null]) {
    env.fire({ origin: ORIGIN, data: { src: "host", type: "goto", page } });
  }
  assert.equal(app.page, 1);
});

test("a foreign message never reaches the viewer bridge", async () => {
  const env = installEnv();
  const app = await importReady();
  app.pdfDocument = {};
  app.emit("pagesinit");

  env.fire({ origin: "http://evil.example", data: { src: "host", type: "goto", page: 4 } });
  env.fire({ origin: ORIGIN, data: { src: "someone-else", type: "goto", page: 4 } });
  env.fire({ origin: ORIGIN, data: { src: "host", type: "pages", total: 4 } });
  assert.equal(app.page, 1);
});

// The bridge is installed by one <script> tag inside the vendored viewer.html
// — the only local edit to third-party source. Its "../" depth is easy to get
// wrong and silent to break: the viewer still renders, but the host pager sits
// on "载入中…" forever.
test("the vendored viewer loads the bridge from a path that resolves", async () => {
  const viewerHTML = await readFile(
    fileURLToPath(new URL("../../frontend/vendor/pdfjs/web/viewer.html", import.meta.url)),
    "utf8"
  );
  const tag = viewerHTML.match(/<script src="([^"]*viewer-bridge\.mjs)"([^>]*)><\/script>/);
  assert.ok(tag, "viewer.html must load viewer-bridge.mjs");
  assert.match(tag[2], /\btype="module"/, "the bridge is an ES module");

  const served = new URL(tag[1], "http://host/vendor/pdfjs/web/viewer.html");
  assert.equal(served.pathname, "/viewer-bridge.mjs");

  const onDisk = new URL(`../../frontend${served.pathname}`, import.meta.url);
  await assert.doesNotReject(readFile(onDisk, "utf8"));
});

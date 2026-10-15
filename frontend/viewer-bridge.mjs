// Runs inside the vendored pdf.js viewer. It is loaded by the single
// <script type="module"> tag appended to vendor/pdfjs/web/viewer.html, and
// lives outside the vendor directory on purpose: upgrading pdf.js means
// replacing that directory wholesale and re-applying only that one tag.
import { HOST_SRC, Msg, listener, postToHost } from "./lib/bridge.mjs";
import { start as startAnnotations } from "./lib/annotate.mjs";

const app = globalThis.PDFViewerApplication;
if (!app) {
  console.error("recorder: PDFViewerApplication missing, viewer bridge disabled");
} else {
  connect(app);
}

// pdf.js builds its event bus partway through an async stretch of
// initialize(), so whether the bus exists while this module runs is decided by
// microtask timing we do not control. Waiting for initializedPromise removes
// the guesswork, and loses nothing: pages cannot move before a document is
// opened, which happens strictly after initialization.
async function connect(app) {
  // Moving to a page is dropped on the floor by pdf.js while the viewer has no
  // pages at all, which lasts from "app.pdfDocument exists" until pagesinit.
  // A goto arriving in that window is held instead.
  let pending = null;
  let havePages = false;

  window.addEventListener(
    "message",
    listener(HOST_SRC, (m) => {
      if (m.type !== Msg.Goto) return;
      const page = Math.trunc(m.page);
      if (!Number.isInteger(page) || page < 1) return;
      if (havePages) {
        app.page = page;
      } else {
        pending = page;
      }
    })
  );

  try {
    await app.initializedPromise;
  } catch (ex) {
    console.error("recorder: viewer never finished initialising, bridge disabled", ex);
    return;
  }

  app.eventBus.on("pagesinit", () => {
    havePages = true;
    if (pending !== null) {
      app.page = pending;
      pending = null;
    }
  });
  app.eventBus.on("pagesloaded", (e) => {
    postToHost(Msg.Pages, { total: e.pagesCount });
  });
  app.eventBus.on("pagechanging", (e) => {
    postToHost(Msg.Page, { page: e.pageNumber });
  });
  // The annotation overlay shares this bus and the same readiness gate.
  startAnnotations();
  postToHost(Msg.Ready);
}

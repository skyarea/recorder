// The annotation overlay, loaded by viewer-bridge.mjs inside the vendored
// pdf.js viewer.
//
// An SVG layer is added to every rendered page, positioned over it, and the
// annotations for that page are drawn into it as normalised shapes scaled by
// the page's own rectangle. Three tools share it: highlight (#10), freehand
// line (#11) and box (#12) — the geometry they need lives in
// frontend/lib/geometry.mjs, which is unit-tested; this file is the wiring
// that turns a drag or a text selection into that geometry and back.
import { Msg, postToHost } from "./bridge.mjs";
import { clientRectToNorm, hitTest, normRectToClient, pointsToNorm } from "./geometry.mjs";

const app = globalThis.PDFViewerApplication;

const SVG_NS = "http://www.w3.org/2000/svg";
const LAYER_CLASS = "recorder-annotations";

// The active tool, as the host last set it. null means the overlay ignores
// pointer input entirely.
let tool = null;
// Annotations by page number: {page: [{id, kind, data}]}.
let annotations = {};
// The drag in progress: {page, startX, startY, points} in client pixels.
let drag = null;

// --- layers -----------------------------------------------------------------

// The SVG layer over one page, created on first use. The page element is the
// positioning parent (pdf.js gives it position:relative and its own size), so
// the layer can cover it exactly at any zoom.
function layerFor(pageEl) {
  let layer = pageEl.querySelector(":scope > ." + LAYER_CLASS);
  if (!layer) {
    layer = document.createElementNS(SVG_NS, "svg");
    layer.setAttribute("class", LAYER_CLASS);
    // The layer must not swallow clicks meant for the page's text: it only
    // takes pointer events where a shape is drawn, and only in a tool.
    layer.style.position = "absolute";
    layer.style.inset = "0";
    layer.style.width = "100%";
    layer.style.height = "100%";
    layer.style.pointerEvents = "none";
    pageEl.append(layer);
  }
  return layer;
}

// The page element for a page number, if that page is currently rendered.
function pageElFor(pageNumber) {
  return app.pdfViewer.getPageView(pageNumber - 1)?.div ?? null;
}

function pageRectOf(pageEl) {
  // The viewport is the page's own rectangle in client coordinates, already
  // accounting for zoom and scroll: exactly the frame normalised coordinates
  // are relative to.
  const vp = app.pdfViewer.getPageView(
    Number(pageEl.dataset.pageNumber) - 1
  ).viewport;
  return { x: vp.transform[4], y: vp.transform[5], width: vp.width, height: vp.height };
}

// --- drawing ----------------------------------------------------------------

function drawShape(kind, data, pageEl) {
  const rect = pageRectOf(pageEl);
  const layer = layerFor(pageEl);

  if (kind === "highlight" || kind === "box") {
    const rects = kind === "box" ? [data.rect] : data.rects;
    for (const r of rects) {
      const c = normRectToClient(rect, r);
      const el = document.createElementNS(SVG_NS, "rect");
      el.setAttribute("x", c.x - rect.x);
      el.setAttribute("y", c.y - rect.y);
      el.setAttribute("width", c.width);
      el.setAttribute("height", c.height);
      el.setAttribute("class", "recorder-ann recorder-ann-" + kind);
      layer.append(el);
    }
    return;
  }

  if (kind === "line") {
    const el = document.createElementNS(SVG_NS, "polyline");
    const pts = data.points
      .map(([x, y]) => {
        const c = normRectToClient(rect, { x, y, w: 0, h: 0 });
        return `${c.x - rect.x},${c.y - rect.y}`;
      })
      .join(" ");
    el.setAttribute("points", pts);
    el.setAttribute("class", "recorder-ann recorder-ann-line");
    el.setAttribute("fill", "none");
    layer.append(el);
    return;
  }
}

// Redraw one page's layer from the annotations the host sent.
function renderPage(pageNumber) {
  const pageEl = pageElFor(pageNumber);
  if (!pageEl) return;
  const layer = layerFor(pageEl);
  layer.replaceChildren();
  for (const ann of annotations[pageNumber] || []) {
    drawShape(ann.kind, ann.data, pageEl);
  }
}

// --- pointer input ----------------------------------------------------------

// A drag starts only when a tool is active, and only on the page's own area.
function onPointerDown(e) {
  if (!tool) return;
  const pageEl = e.target.closest(".page");
  if (!pageEl) return;
  e.preventDefault();
  drag = {
    page: Number(pageEl.dataset.pageNumber),
    startX: e.clientX,
    startY: e.clientY,
    points: [[e.clientX, e.clientY]],
    preview: null,
  };
  window.addEventListener("pointermove", onPointerMove);
  window.addEventListener("pointerup", onPointerUp);
}

// While dragging, a preview shape follows the pointer. For the highlight tool
// the shape is not drawn by the drag at all: it comes from the text selection
// on pointer-up, and the drag only exists so the user can see a tool is live.
function onPointerMove(e) {
  if (!drag) return;
  if (tool === "line") {
    drag.points.push([e.clientX, e.clientY]);
    drawPreview();
  } else if (tool === "box") {
    drag.endX = e.clientX;
    drag.endY = e.clientY;
    drawPreview();
  }
}

function onPointerUp(e) {
  window.removeEventListener("pointermove", onPointerMove);
  window.removeEventListener("pointerup", onPointerUp);
  if (!drag) return;

  const pageEl = pageElFor(drag.page);
  if (pageEl) {
    const rect = pageRectOf(pageEl);
    if (tool === "line" && drag.points.length > 1) {
      postToHost(Msg.Add, {
        page: drag.page,
        kind: "line",
        data: { points: pointsToNorm(rect, drag.points) },
      });
    } else if (tool === "box") {
      const n = clientRectToNorm(rect, {
        x: Math.min(drag.startX, drag.endX ?? drag.startX),
        y: Math.min(drag.startY, drag.endY ?? drag.startY),
        width: Math.abs((drag.endX ?? drag.startX) - drag.startX),
        height: Math.abs((drag.endY ?? drag.startY) - drag.startY),
      });
      if (n.w > 0 && n.h > 0) {
        postToHost(Msg.Add, { page: drag.page, kind: "box", data: { rect: n } });
      }
    } else if (tool === "highlight") {
      const rects = selectionRects(pageEl, rect);
      if (rects.length) {
        postToHost(Msg.Add, { page: drag.page, kind: "highlight", data: { rects } });
      }
    } else if (tool === "erase") {
      const nx = (e.clientX - rect.x) / rect.width;
      const ny = (e.clientY - rect.y) / rect.height;
      for (const ann of annotations[drag.page] || []) {
        if (hitTest(ann, nx, ny)) {
          postToHost(Msg.Delete, { id: ann.id });
        }
      }
    }
  }

  clearPreview();
  drag = null;
}

// The normalised rects of the current text selection, one per selected line.
// The browser reports a selection as one or more client rects; each becomes
// its own stored rect so a selection that wraps lines is drawn as the
// separate bands it really is.
function selectionRects(pageEl, rect) {
  const sel = window.getSelection();
  if (!sel || sel.isCollapsed || !sel.rangeCount) return [];
  const out = [];
  for (const r of sel.getRangeAt(0).getClientRects()) {
    if (r.width < 1 || r.height < 1) continue;
    const n = clientRectToNorm(rect, { x: r.left, y: r.top, width: r.width, height: r.height });
    if (n.w > 0 && n.h > 0) out.push(n);
  }
  return out;
}

// The in-progress shape, drawn into the page's own layer and removed when the
// drag ends. It is never sent to the host.
function drawPreview() {
  if (!drag) return;
  const pageEl = pageElFor(drag.page);
  if (!pageEl) return;
  const rect = pageRectOf(pageEl);
  const layer = layerFor(pageEl);
  layer.querySelector(".recorder-preview")?.remove();

  let el = null;
  if (tool === "line") {
    el = document.createElementNS(SVG_NS, "polyline");
    el.setAttribute("points", drag.points
      .map(([x, y]) => `${x - rect.x},${y - rect.y}`).join(" "));
    el.setAttribute("fill", "none");
  } else if (tool === "box") {
    el = document.createElementNS(SVG_NS, "rect");
    const x = Math.min(drag.startX, drag.endX ?? drag.startX) - rect.x;
    const y = Math.min(drag.startY, drag.endY ?? drag.startY) - rect.y;
    el.setAttribute("x", x);
    el.setAttribute("y", y);
    el.setAttribute("width", Math.abs((drag.endX ?? drag.startX) - drag.startX));
    el.setAttribute("height", Math.abs((drag.endY ?? drag.startY) - drag.startY));
    el.setAttribute("fill", "none");
  }
  if (el) {
    el.setAttribute("class", "recorder-preview");
    layer.append(el);
  }
}

function clearPreview() {
  for (const el of document.querySelectorAll(".recorder-preview")) el.remove();
}

// --- host protocol ----------------------------------------------------------

function onMessage(e) {
  if (e.origin !== window.location.origin) return;
  const m = e.data;
  if (!m || m.src !== "host") return;

  if (m.type === Msg.Tool) {
    tool = m.tool || null;
    document.body.dataset.tool = tool || "";
    // A tool that draws takes the text selection away, so the browser's own
    // highlight stops fighting the annotation layer.
    document.body.classList.toggle("recorder-tool-active", !!tool);
    if (!tool) clearPreview();
    return;
  }
  if (m.type === Msg.Annotations) {
    annotations = m.annotations || {};
    for (const page of Object.keys(annotations)) renderPage(Number(page));
  }
}

// --- zoom and page lifecycle ------------------------------------------------

// A zoom re-renders every page, so the stored normalised shapes have to be
// redrawn at the new scale. pdf.js replaces the page elements, so this runs
// after the new ones exist.
function redrawAll() {
  for (const page of Object.keys(annotations)) renderPage(Number(page));
}

export function start() {
  installStyles();
  window.addEventListener("message", onMessage);
  app.eventBus.on("pagesinit", redrawAll);
  app.eventBus.on("scalechanging", () => setTimeout(redrawAll, 0));

  // The overlay listens on the viewer container rather than on each page, so
  // a page rendered later (scrolling down) is covered without re-wiring.
  app.pdfViewer.viewer?.addEventListener("pointerdown", onPointerDown);
}

// The overlay starts from the bridge rather than at import time: this
// module is also loaded by the host<->viewer bridge's own test, which runs
// under node with no DOM. Exporting start() keeps the side effect where a
// document actually exists.

// The overlay's styles are injected rather than linked, so the vendored
// viewer.html keeps exactly one local edit (the bridge script tag) and
// upgrading pdf.js stays a directory swap plus that one line.
function installStyles() {
  if (document.getElementById("recorder-annotate-styles")) return;
  const style = document.createElement("style");
  style.id = "recorder-annotate-styles";
  style.textContent = `
    .recorder-ann { pointer-events: none; }
    .recorder-ann-highlight { fill: rgba(250, 204, 21, 0.38); }
    .recorder-ann-box { stroke: #2563eb; stroke-width: 2; fill: none; }
    .recorder-ann-line { stroke: #dc2626; stroke-width: 2; stroke-linecap: round; stroke-linejoin: round; }
    .recorder-preview { pointer-events: none; opacity: .7; }
    /* A tool is live: the browser's own text selection would fight the
       overlay, and the crosshair says so before the user drags. */
    body.recorder-tool-active .pdfViewer { cursor: crosshair; }
    body.recorder-tool-active .pdfViewer .textLayer { user-select: none; -webkit-user-select: none; }
    /* Erase mode wants to hit shapes, so the layer takes the pointer. */
    body.recorder-tool-active[data-tool="erase"] .${LAYER_CLASS} { pointer-events: auto; }
  `;
  document.head.append(style);
}


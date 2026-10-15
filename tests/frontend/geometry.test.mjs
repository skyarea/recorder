// Tests for the geometry the annotation tools share. Normalised coordinates
// (0..1, origin at the page's top-left) are what gets stored, so these four
// functions are what keeps a highlight under the words it was drawn on when
// the window resizes or the reader zooms.
//
// A pageRect is {x, y, width, height} in client (CSS pixel) coordinates —
// the shape a DOMRect gives you.
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  clientRectToNorm,
  normRectToClient,
  pointsToNorm,
  hitTest,
  dragRect,
  thinPolyline,
} from "../../frontend/lib/geometry.mjs";

const page = { x: 100, y: 50, width: 400, height: 800 };

test("clientRectToNorm maps a client rect into 0..1", () => {
  // A quarter of the page from its top-left corner.
  assert.deepEqual(
    clientRectToNorm(page, { x: 100, y: 50, width: 100, height: 200 }),
    { x: 0, y: 0, w: 0.25, h: 0.25 }
  );
  // A rect offset from the origin: 3/4 across, half down.
  assert.deepEqual(
    clientRectToNorm(page, { x: 400, y: 450, width: 100, height: 400 }),
    { x: 0.75, y: 0.5, w: 0.25, h: 0.5 }
  );
});

test("clientRectToNorm clamps to the page", () => {
  // A selection that runs past the right and bottom edges (the last line of a
  // page, or a drag that leaves the frame) is stored as the part that is on
  // the page rather than as coordinates outside it.
  assert.deepEqual(
    clientRectToNorm(page, { x: 350, y: 700, width: 400, height: 400 }),
    { x: 0.625, y: 0.8125, w: 0.375, h: 0.1875 }
  );
  // A rect that straddles the left and top edges keeps only the part on
  // the page: x=50 is 50px left of the page's edge and y=20 is 30px above its
  // top, while its far corner (200, 120) is 100px in and 70px down.
  assert.deepEqual(
    clientRectToNorm(page, { x: 50, y: 20, width: 150, height: 100 }),
    { x: 0, y: 0, w: 0.25, h: 0.0875 }
  );
  // A rect entirely outside the page contributes nothing.
  assert.deepEqual(
    clientRectToNorm(page, { x: -50, y: -50, width: 100, height: 100 }),
    { x: 0, y: 0, w: 0, h: 0 }
  );
});

test("normRectToClient is the inverse of clientRectToNorm", () => {
  const client = { x: 250, y: 300, width: 120, height: 90 };
  const norm = clientRectToNorm(page, client);
  const back = normRectToClient(page, norm);
  assert.ok(Math.abs(back.x - client.x) < 1e-9);
  assert.ok(Math.abs(back.y - client.y) < 1e-9);
  assert.ok(Math.abs(back.width - client.width) < 1e-9);
  assert.ok(Math.abs(back.height - client.height) < 1e-9);
});

test("normRectToClient scales with the page", () => {
  // The same stored rect on a page twice as large is twice as many pixels,
  // which is what makes an annotation survive a zoom.
  const norm = { x: 0.25, y: 0.5, w: 0.5, h: 0.25 };
  const small = normRectToClient({ x: 0, y: 0, width: 400, height: 800 }, norm);
  const big = normRectToClient({ x: 0, y: 0, width: 800, height: 1600 }, norm);
  assert.deepEqual(small, { x: 100, y: 400, width: 200, height: 200 });
  assert.deepEqual(big, { x: 200, y: 800, width: 400, height: 400 });
  // A page that is also offset keeps the offset.
  assert.deepEqual(
    normRectToClient({ x: 30, y: 20, width: 400, height: 800 }, norm),
    { x: 130, y: 420, width: 200, height: 200 }
  );
});

test("pointsToNorm normalises a polyline relative to its own page", () => {
  const pts = pointsToNorm(page, [[100, 50], [300, 450], [500, 850]]);
  assert.deepEqual(pts, [[0, 0], [0.5, 0.5], [1, 1]]);
});

test("pointsToNorm keeps the polyline's shape", () => {
  // A diagonal line stays diagonal after normalising and coming back, which
  // is what a freehand stroke needs: the points must not be re-ordered or
  // collapsed.
  const pts = [[120, 80], [200, 300], [380, 620]];
  const norm = pointsToNorm(page, pts);
  assert.equal(norm.length, pts.length);
  const back = norm.map(([x, y]) => {
    const r = normRectToClient(page, { x, y, w: 0, h: 0 });
    return [r.x, r.y];
  });
  pts.forEach((p, i) => {
    assert.ok(Math.abs(back[i][0] - p[0]) < 1e-9, `x of point ${i}`);
    assert.ok(Math.abs(back[i][1] - p[1]) < 1e-9, `y of point ${i}`);
  });
});

// An annotation is what the API stores: {kind, data} where data is the
// normalised shape. hitTest answers "did the user click this one", which is
// what erase mode needs.
const highlight = { kind: "highlight", data: { rects: [{ x: 0.25, y: 0.5, w: 0.5, h: 0.25 }] } };

test("hitTest finds a point inside a highlight rect", () => {
  assert.equal(hitTest(highlight, 0.5, 0.6), true);
  assert.equal(hitTest(highlight, 0.26, 0.51), true);
  assert.equal(hitTest(highlight, 0.74, 0.74), true);
});

test("hitTest rejects a point outside the rect", () => {
  assert.equal(hitTest(highlight, 0.1, 0.6), false);
  assert.equal(hitTest(highlight, 0.9, 0.6), false);
  assert.equal(hitTest(highlight, 0.5, 0.2), false);
  assert.equal(hitTest(highlight, 0.5, 0.9), false);
});

test("hitTest's eps makes the edge clickable", () => {
  // The rect's own edges, exactly.
  assert.equal(hitTest(highlight, 0.25, 0.5), true);
  assert.equal(hitTest(highlight, 0.75, 0.75), true);
  // Just outside, but within eps (0.01) of the edge.
  assert.equal(hitTest(highlight, 0.24, 0.5), true);
  assert.equal(hitTest(highlight, 0.76, 0.75), true);
  // Well outside eps.
  assert.equal(hitTest(highlight, 0.23, 0.5), false);
});

test("hitTest checks every rect of a multi-rect highlight", () => {
  // A selection spanning two lines is stored as two rects; clicking either
  // one must find the annotation.
  const two = { kind: "highlight", data: { rects: [
    { x: 0.1, y: 0.1, w: 0.2, h: 0.05 },
    { x: 0.1, y: 0.3, w: 0.8, h: 0.05 },
  ] } };
  assert.equal(hitTest(two, 0.15, 0.12), true);
  assert.equal(hitTest(two, 0.7, 0.32), true);
  // Between them: neither rect covers it.
  assert.equal(hitTest(two, 0.5, 0.2), false);
});

const line = { kind: "line", data: { points: [[0.2, 0.2], [0.8, 0.8]] } };

test("hitTest finds a point on a line", () => {
  assert.equal(hitTest(line, 0.5, 0.5), true);
  // Near the middle, just off the line by less than eps.
  assert.equal(hitTest(line, 0.5, 0.505), true);
});

test("hitTest rejects a point off the line", () => {
  assert.equal(hitTest(line, 0.5, 0.2), false);
  assert.equal(hitTest(line, 0.2, 0.5), false);
});

test("hitTest does not extend past a line's ends", () => {
  // A point in line with the stroke but beyond its last vertex is not on it:
  // otherwise a long stroke would swallow clicks far away from it.
  assert.equal(hitTest(line, 0.95, 0.95), false);
  assert.equal(hitTest(line, 0.05, 0.05), false);
});

test("hitTest honours a custom eps", () => {
  // A fatter tolerance finds points the default misses.
  assert.equal(hitTest(line, 0.5, 0.52, 0.05), true);
  assert.equal(hitTest(line, 0.5, 0.52, 0.01), false);
});

test("hitTest reports false for an unknown shape", () => {
  assert.equal(hitTest({ kind: "weird", data: {} }, 0.5, 0.5), false);
  assert.equal(hitTest({ kind: "weird", data: null }, 0.5, 0.5), false);
});

test("hitTest accepts the stored annotation or its data alone", () => {
  // The caller in erase mode has the whole annotation; a caller redrawing one
  // shape has only its data. Both must work.
  const data = { rects: [{ x: 0, y: 0, w: 0.5, h: 0.5 }] };
  assert.equal(hitTest({ data }, 0.25, 0.25), true);
  assert.equal(hitTest(data, 0.25, 0.25), true);
  assert.equal(hitTest({ data }, 0.75, 0.75), false);
  assert.equal(hitTest(data, 0.75, 0.75), false);
});

test("hitTest does not guess at a bare rect", () => {
  // A single {x,y,w,h} is not a shape any tool stores: passing one must say
  // "no" rather than silently testing the wrong thing.
  const bare = { x: 0.1, y: 0.1, w: 0.5, h: 0.5 };
  assert.equal(hitTest(bare, 0.3, 0.3), false);
  assert.equal(hitTest({ data: bare }, 0.3, 0.3), false);
  // Wrapped the way a highlight really stores it, the same rectangle is hit.
  assert.equal(hitTest({ data: { rects: [bare] } }, 0.3, 0.3), true);
});

test("hitTest accepts a box's single rect", () => {
  // #12 stores {"rect": {...}}; the box branch routes it through the rect
  // logic so a box is clickable exactly like a highlight.
  const box = { kind: "box", data: { rect: { x: 0.2, y: 0.2, w: 0.4, h: 0.4 } } };
  assert.equal(hitTest(box, 0.4, 0.4), true);
  assert.equal(hitTest(box, 0.1, 0.4), false);
});

// dragRect turns the two corners of a box drag into the rectangle between
// them (#12). A drag may go any direction — right-to-left, bottom-to-top —
// and must produce the same rect.
test("dragRect builds the same rect whichever way the drag went", () => {
  assert.deepEqual(dragRect(10, 20, 110, 80), { x: 10, y: 20, width: 100, height: 60 });
  assert.deepEqual(dragRect(110, 80, 10, 20), { x: 10, y: 20, width: 100, height: 60 });
  assert.deepEqual(dragRect(110, 20, 10, 80), { x: 10, y: 20, width: 100, height: 60 });
  assert.deepEqual(dragRect(10, 80, 110, 20), { x: 10, y: 20, width: 100, height: 60 });
});

test("dragRect of a press without movement is empty", () => {
  // A click that never drags is not a box: zero width and height, which the
  // tool then refuses to store.
  assert.deepEqual(dragRect(50, 50, 50, 50), { x: 50, y: 50, width: 0, height: 0 });
});

// thinPolyline is the freehand sampler (#11): a stroke is stored with a
// point only where it has travelled at least minDist since the last kept
// point, so a held-steady or fast scribbling pointer cannot flood the
// annotation with near-duplicate points.
test("thinPolyline keeps points minDist apart along a dense path", () => {
  const pts = [];
  for (let i = 0; i <= 10; i++) pts.push([i, 0]);
  assert.deepEqual(thinPolyline(pts, 4), [[0, 0], [4, 0], [8, 0]]);
});

test("thinPolyline leaves an already-sparse stroke untouched", () => {
  const pts = [[0, 0], [10, 0], [20, 10]];
  assert.deepEqual(thinPolyline(pts, 4), pts);
});

test("thinPolyline drops sub-minDist jitter but keeps the shape", () => {
  // A wobble whose steps are all under minDist must not drag the stored
  // stroke along with it: the endpoints of the kept points carry the line.
  const pts = [[0, 0], [1, 1], [2, 0], [3, 1], [4, 0]];
  assert.deepEqual(thinPolyline(pts, 2), [[0, 0], [2, 0], [4, 0]]);
});

test("thinPolyline handles empty, single and duplicate points", () => {
  assert.deepEqual(thinPolyline([], 4), []);
  assert.deepEqual(thinPolyline([[5, 5]], 4), [[5, 5]]);
  assert.deepEqual(thinPolyline([[0, 0], [0, 0], [0, 0]], 4), [[0, 0]]);
});

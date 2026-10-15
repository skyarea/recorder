// The geometry the three annotation tools share.
//
// Everything an annotation stores is normalised: 0..1 with the page's top-left
// as the origin. That is what makes a highlight survive a zoom or a window
// resize — the pixels are recomputed from the page rectangle every time it is
// drawn, and only the normalised shape is kept.
//
// A "page rect" is {x, y, width, height} in client (CSS pixel) coordinates,
// the shape a DOMRect hands you. A "norm rect" is {x, y, w, h}, also 0..1.

// clamp keeps a value inside 0..1.
function clamp01(v) {
  if (!(v > 0)) return 0; // also catches NaN
  return v < 1 ? v : 1;
}

// clampAtLeast clamps into [min, 1]: the far edge of a rect may not come back
// past the near edge, however far off the page the rect started.
function clampAtLeast(v, min) {
  if (!(v > min)) return min; // also catches NaN
  return v < 1 ? v : 1;
}

/**
 * clientRectToNorm turns a client rectangle into a normalised one.
 *
 * A rectangle that runs past an edge is clamped to the page rather than
 * stored: the last line of a page and a drag that leaves the frame both
 * produce one, and a rect reaching outside 0..1 would draw outside the page.
 * A rectangle entirely outside the page collapses to a zero-size rect at the
 * nearest edge.
 *
 * Each edge is clamped on its own, then the width is taken from the clamped
 * pair. Clamping both edges to the page is not enough: a rect that starts off
 * the left edge and ends inside the page would have both edges clamped to 0
 * and lose its width.
 */
export function clientRectToNorm(page, rect) {
  const pw = page.width || 1;
  const ph = page.height || 1;
  const left = clamp01((rect.x - page.x) / pw);
  const top = clamp01((rect.y - page.y) / ph);
  const right = clampAtLeast((rect.x + rect.width - page.x) / pw, left);
  const bottom = clampAtLeast((rect.y + rect.height - page.y) / ph, top);
  return { x: left, y: top, w: right - left, h: bottom - top };
}

/** normRectToClient turns a normalised rectangle back into client pixels. */
export function normRectToClient(page, n) {
  return {
    x: page.x + n.x * page.width,
    y: page.y + n.y * page.height,
    width: n.w * page.width,
    height: n.h * page.height,
  };
}

/**
 * pointsToNorm normalises a polyline: [[x, y], ...] in client pixels to
 * [[x, y], ...] in 0..1. The point order is preserved, which is what a
 * freehand stroke needs.
 */
export function pointsToNorm(page, points) {
  const pw = page.width || 1;
  const ph = page.height || 1;
  return points.map(([x, y]) => [clamp01((x - page.x) / pw), clamp01((y - page.y) / ph)]);
}

/**
 * dragRect turns the two corners of a drag into the rectangle between them,
 * whichever directions the drag went: right-to-left and bottom-to-top drags
 * produce the same rect as their opposites. A press that never moved yields
 * a zero-size rect, which the box tool then refuses to store.
 */
export function dragRect(x0, y0, x1, y1) {
  return {
    x: Math.min(x0, x1),
    y: Math.min(y0, y1),
    width: Math.abs(x1 - x0),
    height: Math.abs(y1 - y0),
  };
}

/**
 * thinPolyline samples a freehand stroke: a point is kept only once the
 * stroke has travelled at least minDist (client pixels) since the last kept
 * point. Point order is preserved and the first point always stays, so the
 * stored stroke starts exactly where the drag began while a pointer that
 * hovers, wobbles or fires far more often than it moves cannot flood the
 * annotation with near-duplicate points.
 */
export function thinPolyline(points, minDist) {
  const out = [];
  for (const p of points) {
    const last = out[out.length - 1];
    if (!last || Math.hypot(p[0] - last[0], p[1] - last[1]) >= minDist) {
      out.push(p);
    }
  }
  return out;
}

/**
 * hitTest reports whether the normalised point (nx, ny) is on the annotation.
 *
 * It takes the stored annotation ({kind, data}) or its data alone, and the
 * data must be one of the shapes the tools actually store:
 *   - a highlight is {"rects": [{x, y, w, h}, ...]} — hit when the point
 *     falls inside any rect, grown by eps so the edge is clickable;
 *   - a line is {"points": [[x, y], ...]} — hit when the point comes within
 *     eps of any segment, measured perpendicular to it and never past its
 *     ends;
 *   - a box is {"rect": {x, y, w, h}} (#12).
 * A single rect ({"x", "y", "w", "h"}) is not a shape any tool stores, so
 * it is not guessed at: passing one returns false rather than silently
 * testing the wrong thing. The default eps is 0.01, one percent of the page.
 */
export function hitTest(ann, nx, ny, eps = 0.01) {
  const data = ann && ann.data ? ann.data : ann;
  if (!data || typeof data !== "object") return false;

  if (Array.isArray(data.rects)) {
    return data.rects.some((r) => (
      nx >= r.x - eps && nx <= r.x + r.w + eps &&
      ny >= r.y - eps && ny <= r.y + r.h + eps
    ));
  }

  if (Array.isArray(data.points)) {
    const pts = data.points;
    if (pts.length === 1) {
      // A single-point stroke (a dot) is hit only right on it.
      return Math.abs(pts[0][0] - nx) <= eps && Math.abs(pts[0][1] - ny) <= eps;
    }
    for (let i = 0; i < pts.length - 1; i++) {
      if (nearSegment(pts[i], pts[i + 1], nx, ny, eps)) return true;
    }
    return false;
  }

  if (data.rect) {
    return hitTest({ data: { rects: [data.rect] } }, nx, ny, eps);
  }

  return false;
}

// nearSegment reports how close (nx, ny) comes to the segment a-b, compared
// against eps. The distance is perpendicular for a point inside the segment's
// span and to the nearer endpoint otherwise, so clicking past the end of a
// stroke does not select it.
function nearSegment(a, b, nx, ny, eps) {
  const dx = b[0] - a[0];
  const dy = b[1] - a[1];
  const len2 = dx * dx + dy * dy;
  if (len2 === 0) {
    return Math.hypot(nx - a[0], ny - a[1]) <= eps;
  }
  // Project the point onto the segment, clamped to its ends.
  let t = ((nx - a[0]) * dx + (ny - a[1]) * dy) / len2;
  t = t < 0 ? 0 : t > 1 ? 1 : t;
  return Math.hypot(nx - (a[0] + t * dx), ny - (a[1] + t * dy)) <= eps;
}

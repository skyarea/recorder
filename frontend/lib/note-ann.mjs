// The pure logic behind the note↔annotation badges and pickers.
//
// A note carries `annIds` (the annotations it references); one annotation can
// be referenced by many notes. annLabel turns an annotation into the short
// Chinese text shown on a note's badge and in the "挂标注" picker; linkedNotes
// answers the reverse question the badge count and the picker need to show.
// Both are plain functions over the data the API already returns, so the
// browser shell and `node --test` share one implementation.

// The Chinese name of each tool, matching the toolbar in app.mjs.
export const ANN_LABELS = {
  highlight: "划词",
  line: "画线",
  box: "框选",
};

/**
 * annLabel renders one annotation for display: `p.3 · 划词`.
 *
 * The page prefix appears only for a real page number (a positive integer):
 * a missing or bogus page still yields the tool's name rather than the string
 * "p.undefined". An unknown kind keeps its raw name — the tools store only
 * highlight|line|box today, so this is a fallback, not a second vocabulary.
 * Anything that is not an object with a kind returns an empty string.
 */
export function annLabel(ann) {
  if (!ann || typeof ann !== "object") return "";
  const name = ANN_LABELS[ann.kind] || ann.kind || "";
  if (!name) return "";
  const page = ann.page;
  if (Number.isInteger(page) && page > 0) return `p.${page} · ${name}`;
  return name;
}

/**
 * linkedNotes returns the notes that reference annotationId.
 *
 * This is the reverse of `note.annIds` and what makes the link many-to-many
 * visible from the annotation's side: a note may list many annotations, and
 * the same annotation may be listed by many notes, all seen in one pass.
 * Notes missing their annIds are treated as linked to nothing.
 */
export function linkedNotes(notes, annotationId) {
  const list = notes || [];
  return list.filter((n) => Array.isArray(n.annIds) && n.annIds.includes(annotationId));
}

/**
 * findAnnotation looks one id up in the by-page map the host keeps for the
 * open document ({page: [{id, kind, data}]}) and returns {id, kind, page}.
 *
 * A note badge needs the shape's page to know where to jump, and the page is
 * the map's key rather than a field of the shape, so it is folded in here.
 * The page keys are object properties and therefore strings; the returned
 * page is always a number. Null when the id is not in the map — which is how
 * a note whose shape has since been deleted is rendered as a dead badge
 * rather than silently dropping the link the user can still un-hang.
 */
export function findAnnotation(byPage, annotationId) {
  for (const [page, list] of Object.entries(byPage || {})) {
    const hit = (list || []).find((a) => a.id === annotationId);
    if (hit) return { id: hit.id, kind: hit.kind, page: Number(page) };
  }
  return null;
}

/**
 * allAnnotations flattens the same by-page map into a list ordered by page,
 * which is the order the "挂标注" picker shows the shapes of a document in.
 */
export function allAnnotations(byPage) {
  return Object.entries(byPage || {})
    .flatMap(([page, list]) => (list || []).map((a) => ({ ...a, page: Number(page) })))
    .sort((a, b) => a.page - b.page);
}

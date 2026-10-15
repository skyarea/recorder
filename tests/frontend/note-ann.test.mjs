// Tests for the note↔annotation linking logic the panel and the in-viewer
// popup share. Two pure selectors, kept out of app.mjs so the browser shell
// and `node --test` exercise the same code:
//   - annLabel(ann): the display text for one annotation's badge / picker
//     row ("p.3 · 划词");
//   - linkedNotes(notes, annId): the notes that reference one annotation, the
//     reverse of a note's own annIds. This is what proves the M:N reads both
//     ways: a note can carry many annotations, and one annotation can be
//     named by many notes.
import { test } from "node:test";
import assert from "node:assert/strict";
import { annLabel, linkedNotes } from "../../frontend/lib/note-ann.mjs";

test("annLabel names a highlight by page and tool in Chinese", () => {
  assert.equal(annLabel({ page: 3, kind: "highlight" }), "p.3 · 划词");
  assert.equal(annLabel({ page: 12, kind: "line" }), "p.12 · 画线");
  assert.equal(annLabel({ page: 1, kind: "box" }), "p.1 · 框选");
});

test("annLabel tolerates the fields the popup does not always carry", () => {
  // A page missing or non-numeric still reads sensibly rather than "p.undefined".
  assert.equal(annLabel({ kind: "highlight" }), "划词");
  assert.equal(annLabel({ page: 0, kind: "box" }), "框选");
  // A kind the tools never store keeps its raw name instead of an empty label.
  assert.equal(annLabel({ page: 4, kind: "scribble" }), "p.4 · scribble");
  assert.equal(annLabel(null), "");
  assert.equal(annLabel({}), "");
});

test("linkedNotes returns every note that references the annotation", () => {
  const notes = [
    { id: "n1", annIds: ["a1", "a2"] },
    { id: "n2", annIds: [] },
    { id: "n3", annIds: ["a2"] },
  ];
  // One annotation held by two notes — the reverse direction of the M:N.
  assert.deepEqual(linkedNotes(notes, "a2").map((n) => n.id), ["n1", "n3"]);
  assert.deepEqual(linkedNotes(notes, "a1").map((n) => n.id), ["n1"]);
  assert.deepEqual(linkedNotes(notes, "a9"), []);
});

test("linkedNotes is robust to notes with no annIds field", () => {
  const notes = [{ id: "n1" }, { id: "n2", annIds: null }, { id: "n3", annIds: ["a1"] }];
  assert.deepEqual(linkedNotes(notes, "a1").map((n) => n.id), ["n3"]);
  assert.deepEqual(linkedNotes([], "a1"), []);
});

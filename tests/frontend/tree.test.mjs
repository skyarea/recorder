// Tests for the tree helpers the file list is built from. These are pure
// functions over plain data so they run under `node --test` with no DOM:
// the browser side imports the same modules.
import { test } from "node:test";
import assert from "node:assert/strict";
import { buildTree } from "../../frontend/lib/tree.mjs";

test("buildTree groups files under directory nodes", () => {
  const root = buildTree([
    "a/x.pdf",
    "a/y.pdf",
    "b/deep/z.pdf",
    "top.pdf",
  ]);

  assert.equal(root.name, "");
  assert.equal(root.path, "");

  const names = (node) => [...node.dirs.keys()].sort();
  const files = (node) => node.files.map((f) => f.path).sort();

  assert.deepEqual(names(root), ["a", "b"]);
  assert.deepEqual(files(root), ["top.pdf"]);

  const a = root.dirs.get("a");
  assert.deepEqual(files(a), ["a/x.pdf", "a/y.pdf"]);
  assert.deepEqual(names(a), []);

  const b = root.dirs.get("b");
  const deep = b.dirs.get("deep");
  assert.equal(deep.path, "b/deep");
  assert.deepEqual(files(deep), ["b/deep/z.pdf"]);
});

test("buildTree sorts directories and files naturally", () => {
  const root = buildTree([
    "p10.pdf",
    "p2.pdf",
    "p1.pdf",
    "d10/a.pdf",
    "d2/a.pdf",
  ]);

  // Directory order: d2 before d10, not the string order d10 before d2.
  assert.deepEqual([...root.dirs.keys()], ["d2", "d10"]);

  // Files in the root: p1, p2, p10.
  assert.deepEqual(
    root.files.map((f) => f.name),
    ["p1.pdf", "p2.pdf", "p10.pdf"]
  );

  const d2 = root.dirs.get("d2");
  assert.deepEqual(
    d2.files.map((f) => f.name),
    ["a.pdf"]
  );
});

import { hasMatch, match } from "../../frontend/lib/tree.mjs";

// A record as /api/state reports it: only the fields match() reads.
const rec = (over = {}) => ({
  status: "todo",
  page: 0,
  totalPages: 0,
  starred: false,
  notes: [],
  ...over,
});

test("match filters by status", () => {
  const reading = rec({ status: "reading" });
  assert.equal(match("a.pdf", reading, "all", ""), true);
  assert.equal(match("a.pdf", reading, "reading", ""), true);
  assert.equal(match("a.pdf", reading, "done", ""), false);
  assert.equal(match("a.pdf", reading, "todo", ""), false);
});

test("match filters by star", () => {
  const starred = rec({ starred: true });
  assert.equal(match("a.pdf", starred, "star", ""), true);
  assert.equal(match("a.pdf", rec(), "star", ""), false);
});

test("match query hits the path case-insensitively", () => {
  const r = rec();
  assert.equal(match("Books/Chapter.PDF", r, "all", "chapter"), true);
  assert.equal(match("Books/Chapter.PDF", r, "all", "CHAPTER"), true);
  assert.equal(match("Books/Chapter.PDF", r, "all", "appendix"), false);
});

test("match query hits note text case-insensitively", () => {
  const r = rec({ notes: [{ id: "n1", text: "Remember the Fermat margin" }] });
  assert.equal(match("a.pdf", r, "all", "fermat"), true);
  assert.equal(match("a.pdf", r, "all", "FERMAT"), true);
  assert.equal(match("a.pdf", r, "all", "euler"), false);
});

test("match combines filter and query", () => {
  const r = rec({ status: "done", notes: [{ id: "n1", text: "margin" }] });
  assert.equal(match("a.pdf", r, "done", "margin"), true);
  // A record that matches the filter but not the query is out.
  assert.equal(match("a.pdf", r, "done", "euler"), false);
  // And one that matches the query but not the filter is out too.
  assert.equal(match("a.pdf", r, "todo", "margin"), false);
});

test("hasMatch reports whether a subtree holds any match", () => {
  const records = {
    "a/keep.pdf": rec({ status: "done" }),
    "a/drop.pdf": rec(),
    "b/deep/keep.pdf": rec({ starred: true }),
  };
  const tree = buildTree(Object.keys(records));
  const matchFn = (p) => match(p, records[p], "all", "");

  // The root matches: something under it does.
  assert.equal(hasMatch(tree, matchFn), true);

  // An empty filter hides everything, so nothing matches.
  const nothing = (p) => match(p, records[p], "done", "zzz");
  assert.equal(hasMatch(tree, nothing), false);

  // A filter that only the nested file satisfies still reaches it.
  const starOnly = (p) => match(p, records[p], "star", "");
  assert.equal(hasMatch(tree.dirs.get("b"), starOnly), true);
  assert.equal(hasMatch(tree.dirs.get("a"), starOnly), false);
});

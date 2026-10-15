// The file list's tree: group root-relative paths into directory nodes, and
// order siblings the way a person reads them (p2 before p10, not the string
// order p10 before p2).
//
// Pure functions over plain data, so the browser shell and `node --test` can
// import the same module.

// naturalLess orders two names so a digit run compares by value: "p2" comes
// before "p10". Non-digit runs compare by code unit, so the order is stable
// and does not depend on the host's locale data.
export function naturalLess(a, b) {
  const re = /(\d+|\D+)/g;
  const as = String(a).match(re) || [];
  const bs = String(b).match(re) || [];
  const n = Math.min(as.length, bs.length);
  for (let i = 0; i < n; i++) {
    const x = as[i];
    const y = bs[i];
    const xd = /^\d/.test(x);
    const yd = /^\d/.test(y);
    if (xd && yd) {
      const nx = Number(x);
      const ny = Number(y);
      if (nx !== ny) return nx < ny;
      // Same value, different width ("01" vs "1"): keep them adjacent rather
      // than falling through to the next segment.
      if (x !== y) return x < y;
      continue;
    }
    if (x !== y) return x < y;
  }
  return as.length - bs.length;
}

// match answers whether one file belongs in the list for the current filter
// and query. `filter` is "all", a status key, or "star"; an empty `query`
// disables text search. Both the path and every note's text are searched,
// case-insensitively.
export function match(path, record, filter, query) {
  const r = record || {};
  const status = r.status || "todo";

  if (filter === "star") {
    if (!r.starred) return false;
  } else if (filter && filter !== "all" && status !== filter) {
    return false;
  }

  if (query) {
    const q = String(query).toLowerCase();
    if (String(path).toLowerCase().includes(q)) return true;
    const notes = r.notes || [];
    return notes.some((n) => String((n && n.text) || "").toLowerCase().includes(q));
  }
  return true;
}

// hasMatch reports whether anything in this subtree matches, so a directory
// holding no matching file anywhere under it can be dropped from the list
// instead of rendering as an empty folder.
export function hasMatch(node, matchFn) {
  if (node.files.some((f) => matchFn(f.path))) return true;
  for (const child of node.dirs.values()) {
    if (hasMatch(child, matchFn)) return true;
  }
  return false;
}

/**
 * buildTree groups a list of root-relative "/"-separated paths.
 *
 * Returns the root node: `{name, path, dirs: Map<string, node>, files: []}`,
 * where every directory node has the same shape and `path` is the directory's
 * own root-relative path. Each file is `{name, path}`. Directories appear in
 * the Map in natural order of their names, and so do the files, so a renderer
 * can walk both without sorting again.
 */
export function buildTree(paths) {
  const root = { name: "", path: "", dirs: new Map(), files: [] };

  for (const p of paths) {
    const parts = String(p).split("/");
    const fname = parts.pop();
    let node = root;
    for (const seg of parts) {
      if (!node.dirs.has(seg)) {
        node.dirs.set(seg, {
          name: seg,
          path: node.path ? node.path + "/" + seg : seg,
          dirs: new Map(),
          files: [],
        });
      }
      node = node.dirs.get(seg);
    }
    node.files.push({ name: fname, path: p });
  }

  sortNode(root);
  return root;
}

// sortNode orders this node's files and its directories (by their Map keys),
// then recurses. Called once, after the whole tree is built, so ordering
// never depends on the order paths arrived in.
//
// Array.sort wants a number, and naturalLess answers a question, so each
// comparison is turned into -1/1. Returning naturalLess's boolean directly
// would make every "not less" a 0, which sort reads as "equal" and leaves
// the input order untouched.
function byName(a, b) {
  return naturalLess(a, b) ? -1 : naturalLess(b, a) ? 1 : 0;
}

function sortNode(node) {
  node.files.sort((a, b) => byName(a.name, b.name));
  const entries = [...node.dirs.entries()].sort(([a], [b]) => byName(a, b));
  node.dirs = new Map(entries);
  for (const child of node.dirs.values()) sortNode(child);
}

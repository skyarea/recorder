// The note panel's Markdown renderer, ported from the reference
// implementation as a pure function: `mdToHtml(src)` in, HTML string out.
// No DOM, so the tests and the browser share one implementation.
//
// Escaping happens first, on the whole source, so nothing a note contains can
// become markup — including the ">" that a block quote is written with.

function escapeHtml(s) {
  return String(s).replace(/[&<>"']/g, (c) => (
    { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]
  ));
}

// Inline rules run on already-escaped text, so the patterns they emit
// ("<strong>") can never collide with anything from the source.
function mdInline(s) {
  s = s.replace(/`([^`]+)`/g, "<code>$1</code>");
  s = s.replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");
  s = s.replace(/(^|[^*])\*([^*\n]+)\*(?!\*)/g, "$1<em>$2</em>");
  s = s.replace(/\[([^\]]+)\]\((https?:[^)\s]+)\)/g,
    '<a href="$2" target="_blank" rel="noopener noreferrer">$1</a>');
  return s;
}

/**
 * mdToHtml renders a note's Markdown to an HTML string.
 *
 * Supported: ATX headings (`#`..`####`), unordered lists (`-`, `*`, `+`),
 * block quotes (`>`), paragraphs, and the inline rules in mdInline. Blank
 * lines end a list. Heading levels are shifted down by two so a note's
 * heading never outranks the app's own title.
 */
export function mdToHtml(src) {
  const lines = escapeHtml(src).split("\n");
  const out = [];
  let inList = false;
  const closeList = () => { if (inList) { out.push("</ul>"); inList = false; } };

  for (const raw of lines) {
    let m;
    if ((m = raw.match(/^(#{1,4})\s+(.*)$/))) {
      closeList();
      const lv = Math.min(m[1].length + 2, 6);
      out.push("<h" + lv + ">" + mdInline(m[2]) + "</h" + lv + ">");
      continue;
    }
    if ((m = raw.match(/^\s*[-*+]\s+(.*)$/))) {
      if (!inList) { out.push("<ul>"); inList = true; }
      out.push("<li>" + mdInline(m[1]) + "</li>");
      continue;
    }
    if ((m = raw.match(/^\s*&gt;\s?(.*)$/))) {
      closeList();
      out.push("<blockquote>" + mdInline(m[1]) + "</blockquote>");
      continue;
    }
    if (raw.trim() === "") { closeList(); continue; }
    closeList();
    out.push("<p>" + mdInline(raw) + "</p>");
  }
  closeList();
  return out.join("");
}

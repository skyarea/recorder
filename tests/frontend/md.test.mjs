// Tests for the note panel's Markdown renderer. The browser shell shows notes
// with this; the tests pin the shapes a note actually uses, and that HTML in
// the source cannot become markup.
import { test } from "node:test";
import assert from "node:assert/strict";
import { mdToHtml } from "../../frontend/lib/md.mjs";

test("mdToHtml renders headings, lists and paragraphs", () => {
  assert.equal(
    mdToHtml("# Title\n\nsome text\n\n- one\n- two"),
    "<h3>Title</h3><p>some text</p><ul><li>one</li><li>two</li></ul>"
  );
});

test("mdToHtml maps heading levels into a smaller range", () => {
  // The reference UI's own heading is h1, so note headings start at h3 and
  // the deepest level a note may use still fits under h6.
  assert.equal(mdToHtml("### deep"), "<h5>deep</h5>");
  assert.equal(mdToHtml("#### deeper"), "<h6>deeper</h6>");
});

test("mdToHtml renders inline bold, italic, code and links", () => {
  assert.equal(mdToHtml("a **b** c"), "<p>a <strong>b</strong> c</p>");
  assert.equal(mdToHtml("a *b* c"), "<p>a <em>b</em> c</p>");
  assert.equal(mdToHtml("a `b` c"), "<p>a <code>b</code> c</p>");
  assert.equal(
    mdToHtml("see [docs](https://example.com/a)"),
    '<p>see <a href="https://example.com/a" target="_blank" rel="noopener noreferrer">docs</a></p>'
  );
});

test("mdToHtml renders block quotes", () => {
  assert.equal(mdToHtml("> quoted"), "<blockquote>quoted</blockquote>");
  assert.equal(mdToHtml(">quoted"), "<blockquote>quoted</blockquote>");
});

test("mdToHtml escapes HTML in the source", () => {
  // A note is user input. The escaping happens before any inline rule runs, so
  // a "<script>" in a note is text, not markup.
  assert.equal(
    mdToHtml('<script>alert("x")</script>'),
    "<p>&lt;script&gt;alert(&quot;x&quot;)&lt;/script&gt;</p>"
  );
  assert.equal(mdToHtml("a < b & c > d"), "<p>a &lt; b &amp; c &gt; d</p>");
});

test("mdToHtml reads a quote written as a real '>'", () => {
  // The source is what the user typed, so a quote starts with a real '>'.
  // Escaping turns it into '&gt;', which is what the quote rule then
  // matches — the two steps have to happen in this order for the rule to
  // fire at all.
  assert.equal(mdToHtml("> not a quote"), "<blockquote>not a quote</blockquote>");
});

test("mdToHtml closes a list at the end of the input", () => {
  assert.equal(mdToHtml("- one\n- two"), "<ul><li>one</li><li>two</li></ul>");
  // A blank line ends the list, and a following paragraph opens outside it.
  assert.equal(
    mdToHtml("- one\n\nafter"),
    "<ul><li>one</li></ul><p>after</p>"
  );
});

test("mdToHtml returns empty for empty input", () => {
  assert.equal(mdToHtml(""), "");
  assert.equal(mdToHtml("\n\n"), "");
});

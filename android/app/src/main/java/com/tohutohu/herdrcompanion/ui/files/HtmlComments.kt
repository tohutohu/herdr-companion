package com.tohutohu.herdrcompanion.ui.files

import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json

/** An element tapped on the page, as the page script describes it. */
@Serializable
internal data class ElementPick(
    /** A CSS selector that finds the element again, from the nearest unique id or `body`. */
    val selector: String,
    /** The start tag with its id, class and a few telling attributes. */
    val tag: String,
    /** The start of its visible text (or alt text). */
    val text: String = "",
    val canParent: Boolean = false,
)

/** A comment on one element of one page. */
internal data class PageComment(val page: String, val element: ElementPick, val body: String)

private val pickJson = Json { ignoreUnknownKeys = true }

internal fun parseElementPick(json: String): ElementPick? =
    runCatching { pickJson.decodeFromString<ElementPick>(json) }.getOrNull()

/**
 * Comments not yet added to a message, per session and file. Kept for the life of the process,
 * so leaving the page by mistake (or rotating) does not throw them away. Main thread only.
 */
internal object PendingPageComments {
    private val byFile = mutableMapOf<Pair<String, String>, List<PageComment>>()

    operator fun get(sessionId: String, path: String): List<PageComment> = byFile[sessionId to path].orEmpty()

    operator fun set(sessionId: String, path: String, comments: List<PageComment>) {
        if (comments.isEmpty()) byFile.remove(sessionId to path) else byFile[sessionId to path] = comments
    }
}

/** The comments as one message for the agent: grouped by page, each tied to its element. */
internal fun formatPageComments(comments: List<PageComment>): String = buildString {
    append("Comments on the HTML page:")
    var n = 0
    comments.groupBy { it.page }.forEach { (page, onPage) ->
        append("\n\n").append(page)
        onPage.forEach { c ->
            n++
            append("\n").append(n).append(". `").append(c.element.selector).append("` ").append(c.element.tag)
            if (c.element.text.isNotBlank()) append(" \"").append(c.element.text).append('"')
            c.body.trim().lines().forEach { append("\n   ").append(it) }
        }
    }
}

/**
 * Injected into every page once it loads. It stays idle until the app turns comment mode on;
 * then a tap selects the element under it instead of following links or pressing buttons.
 * Boxes it draws live in one overlay that takes no taps.
 */
internal val HTML_COMMENT_SCRIPT = """
(function () {
  if (window.__herdrComment) return;
  var bridge = window.HerdrComment;
  var layer = null, selBox = null, selected = null, mode = false, marks = [];

  function ensureLayer() {
    if (layer && layer.isConnected) return;
    layer = document.createElement('div');
    layer.style.cssText = 'position:absolute;left:0;top:0;width:0;height:0;pointer-events:none;z-index:2147483647';
    document.documentElement.appendChild(layer);
  }
  function makeBox(color, fill) {
    ensureLayer();
    var d = document.createElement('div');
    d.style.cssText = 'position:absolute;pointer-events:none;box-sizing:border-box;border-radius:4px;' +
      'border:2px solid ' + color + ';background:' + fill;
    layer.appendChild(d);
    return d;
  }
  function place(d, el) {
    var r = el.getBoundingClientRect();
    d.style.display = (r.width || r.height) ? 'block' : 'none';
    d.style.left = (r.left + window.scrollX - 2) + 'px';
    d.style.top = (r.top + window.scrollY - 2) + 'px';
    d.style.width = (r.width + 4) + 'px';
    d.style.height = (r.height + 4) + 'px';
  }
  function cssPath(el) {
    var parts = [];
    while (el && el.nodeType === 1 && el !== document.documentElement) {
      if (el.id && document.querySelectorAll('#' + CSS.escape(el.id)).length === 1) {
        parts.unshift('#' + CSS.escape(el.id));
        break;
      }
      var tag = el.tagName.toLowerCase();
      if (tag === 'body') { parts.unshift('body'); break; }
      var p = el.parentElement, index = 1, same = 0;
      if (p) for (var c = p.firstElementChild; c; c = c.nextElementSibling) {
        if (c.tagName === el.tagName) { same++; if (c === el) index = same; }
      }
      parts.unshift(same > 1 ? tag + ':nth-of-type(' + index + ')' : tag);
      el = p;
    }
    return parts.join(' > ');
  }
  function startTag(el) {
    var s = '<' + el.tagName.toLowerCase();
    ['id', 'class', 'href', 'src', 'alt', 'name', 'type', 'role', 'aria-label'].forEach(function (a) {
      var v = el.getAttribute(a);
      if (v) s += ' ' + a + '="' + (v.length > 60 ? v.slice(0, 60) + '…' : v) + '"';
    });
    return s + '>';
  }
  function describe(el) {
    var text = (el.innerText || el.textContent || el.getAttribute('alt') || '').replace(/\s+/g, ' ').trim();
    return JSON.stringify({
      selector: cssPath(el),
      tag: startTag(el),
      text: text.length > 80 ? text.slice(0, 80) + '…' : text,
      canParent: !!el.parentElement && el.parentElement !== document.documentElement
    });
  }
  function select(el) {
    selected = el;
    if (!selBox) selBox = makeBox('#2563eb', 'rgba(37,99,235,0.12)');
    place(selBox, el);
    bridge.onSelect(describe(el));
  }
  function clearSelection() {
    selected = null;
    if (selBox) { selBox.remove(); selBox = null; }
  }
  function placeAll() {
    if (selected && selBox) place(selBox, selected);
    marks.forEach(function (m) { place(m.box, m.el); });
  }

  ['click', 'submit', 'dblclick', 'contextmenu'].forEach(function (type) {
    window.addEventListener(type, function (e) {
      if (!mode) return;
      e.preventDefault();
      e.stopPropagation();
      e.stopImmediatePropagation();
      if (type === 'click' && e.target && e.target.nodeType === 1) select(e.target);
    }, true);
  });
  // The keyboard opening for the comment shrinks the page; keep the picked element in sight.
  window.addEventListener('resize', function () {
    placeAll();
    if (selected) selected.scrollIntoView({ block: 'nearest' });
  });
  if (window.ResizeObserver) new ResizeObserver(placeAll).observe(document.documentElement);

  window.__herdrComment = {
    setMode: function (on) {
      mode = !!on;
      if (!mode) clearSelection();
    },
    parent: function () {
      if (selected && selected.parentElement && selected.parentElement !== document.documentElement) {
        select(selected.parentElement);
      }
    },
    clear: clearSelection,
    mark: function (list) {
      marks.forEach(function (m) { m.box.remove(); });
      marks = [];
      list.forEach(function (item) {
        var el;
        try { el = document.querySelector(item.s); } catch (err) { el = null; }
        if (!el) return;
        var box = makeBox('#ea580c', 'rgba(234,88,12,0.08)');
        box.style.borderStyle = 'dashed';
        var badge = document.createElement('div');
        badge.textContent = item.n;
        badge.style.cssText = 'position:absolute;left:-10px;top:-10px;min-width:20px;height:20px;padding:0 5px;' +
          'box-sizing:border-box;border-radius:10px;background:#ea580c;color:#fff;' +
          'font:bold 12px/20px system-ui,sans-serif;text-align:center';
        box.appendChild(badge);
        marks.push({ el: el, box: box });
        place(box, el);
      });
    }
  };
})();
""".trimIndent()

(function (global) {
  function esc(value) {
    return String(value == null ? "" : value).replace(/[&<>"']/g, function (ch) {
      return {
        "&": "&amp;",
        "<": "&lt;",
        ">": "&gt;",
        '"': "&quot;",
        "'": "&#39;"
      }[ch];
    });
  }

  // Colors JSON text. Keys, strings, numbers, booleans, and null each get a class.
  function highlightJSON(text) {
    var src = String(text == null ? "" : text);
    var re = /("(?:\\.|[^"\\])*")(\s*:)?|\b(true|false)\b|\bnull\b|-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?/g;
    var out = "";
    var last = 0;
    var match;
    while ((match = re.exec(src))) {
      out += esc(src.slice(last, match.index));
      if (match[1] && match[2]) {
        out += '<span class="j-key">' + esc(match[1]) + "</span>" + esc(match[2]);
      } else if (match[1]) {
        out += '<span class="j-str">' + esc(match[1]) + "</span>";
      } else if (match[3] === "true" || match[3] === "false") {
        out += '<span class="j-bool">' + match[3] + "</span>";
      } else if (match[0] === "null") {
        out += '<span class="j-null">null</span>';
      } else {
        out += '<span class="j-num">' + esc(match[0]) + "</span>";
      }
      last = re.lastIndex;
    }
    out += esc(src.slice(last));
    return out;
  }

  global.highlightJSON = highlightJSON;
})(window);

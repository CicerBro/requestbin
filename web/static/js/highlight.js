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

  function highlightHeaders(text) {
    var lines = String(text == null ? "" : text).split("\n");
    var out = [];
    for (var i = 0; i < lines.length; i++) {
      out.push(i === 0 ? highlightStartLine(lines[i]) : highlightHeaderLine(lines[i]));
    }
    return out.join("\n");
  }

  function highlightStartLine(line) {
    var status = /^(HTTP\/\d+(?:\.\d+)?)(\s+)(\d{3})(\s*)(.*)$/.exec(line);
    if (status) {
      return '<span class="h-ver">' + esc(status[1]) + "</span>" + esc(status[2]) +
        '<span class="h-code">' + esc(status[3]) + "</span>" + esc(status[4]) +
        '<span class="h-reason">' + esc(status[5]) + "</span>";
    }
    var req = /^([!#$%&'*+.^_`|~0-9A-Za-z-]+)(\s+)(\S+)(\s+)(HTTP\/\d+(?:\.\d+)?)$/.exec(line);
    if (req) {
      return '<span class="h-method">' + esc(req[1]) + "</span>" + esc(req[2]) +
        '<span class="h-target">' + esc(req[3]) + "</span>" + esc(req[4]) +
        '<span class="h-ver">' + esc(req[5]) + "</span>";
    }
    return esc(line);
  }

  function highlightHeaderLine(line) {
    if (!line) return "";
    var idx = line.indexOf(":");
    if (idx <= 0) return esc(line);
    return '<span class="h-name">' + esc(line.slice(0, idx)) + '</span><span class="h-colon">:</span><span class="h-val">' + esc(line.slice(idx + 1)) + "</span>";
  }

  function highlightXML(text) {
    var src = String(text == null ? "" : text);
    var out = "";
    var i = 0;
    while (i < src.length) {
      if (src.startsWith("<!--", i)) {
        var commentEnd = src.indexOf("-->", i + 4);
        var commentTo = commentEnd < 0 ? src.length : commentEnd + 3;
        out += '<span class="x-comment">' + esc(src.slice(i, commentTo)) + "</span>";
        i = commentTo;
        continue;
      }
      if (src.startsWith("<![CDATA[", i)) {
        var cdataEnd = src.indexOf("]]>", i + 9);
        var cdataTo = cdataEnd < 0 ? src.length : cdataEnd + 3;
        out += '<span class="x-cdata">' + esc(src.slice(i, cdataTo)) + "</span>";
        i = cdataTo;
        continue;
      }
      if (src.startsWith("<?", i)) {
        var piEnd = src.indexOf("?>", i + 2);
        var piTo = piEnd < 0 ? src.length : piEnd + 2;
        out += highlightPI(src.slice(i, piTo));
        i = piTo;
        continue;
      }
      if (src.charAt(i) === "<") {
        var tagTo = scanXMLTag(src, i);
        out += highlightTag(src.slice(i, tagTo));
        i = tagTo;
        continue;
      }
      var next = src.indexOf("<", i);
      if (next < 0) next = src.length;
      out += esc(src.slice(i, next));
      i = next;
    }
    return out;
  }

  function scanXMLTag(src, i) {
    var quote = "";
    for (var j = i + 1; j < src.length; j++) {
      var c = src.charAt(j);
      if (quote) {
        if (c === quote) quote = "";
        continue;
      }
      if (c === '"' || c === "'") {
        quote = c;
        continue;
      }
      if (c === ">") return j + 1;
    }
    return src.length;
  }

  function highlightPI(raw) {
    var m = /^(<\?)([^\s?]+)([\s\S]*?)(\?>)$/.exec(raw);
    if (!m) return '<span class="x-pi">' + esc(raw) + "</span>";
    return '<span class="x-punct">' + esc(m[1]) + '</span><span class="x-tag">' + esc(m[2]) + "</span>" +
      highlightAttrs(m[3]) + '<span class="x-punct">' + esc(m[4]) + "</span>";
  }

  function highlightTag(tag) {
    var m = /^(<\/?)([^\s/>]+)([\s\S]*?)(\/?>)$/.exec(tag);
    if (!m) return esc(tag);
    return '<span class="x-punct">' + esc(m[1]) + '</span><span class="x-tag">' + esc(m[2]) + "</span>" +
      highlightAttrs(m[3]) + '<span class="x-punct">' + esc(m[4]) + "</span>";
  }

  function highlightAttrs(s) {
    var out = "";
    var i = 0;
    while (i < s.length) {
      var c = s.charAt(i);
      if (c === " " || c === "\t" || c === "\n" || c === "\r") {
        var j = i + 1;
        while (j < s.length && " \t\n\r".indexOf(s.charAt(j)) !== -1) j++;
        out += esc(s.slice(i, j));
        i = j;
        continue;
      }
      var nameEnd = i;
      while (nameEnd < s.length && " \t\n\r=".indexOf(s.charAt(nameEnd)) === -1) nameEnd++;
      if (nameEnd === i) {
        out += esc(c);
        i++;
        continue;
      }
      out += '<span class="x-attr">' + esc(s.slice(i, nameEnd)) + "</span>";
      i = nameEnd;
      var eq = /^\s*=\s*/.exec(s.slice(i));
      if (!eq) continue;
      out += esc(eq[0]);
      i += eq[0].length;
      if (i >= s.length) break;
      var q = s.charAt(i);
      var stop = i;
      if (q === '"' || q === "'") {
        stop = s.indexOf(q, i + 1);
        stop = stop < 0 ? s.length : stop + 1;
      } else {
        while (stop < s.length && " \t\n\r".indexOf(s.charAt(stop)) === -1) stop++;
      }
      out += '<span class="x-val">' + esc(s.slice(i, stop)) + "</span>";
      i = stop;
    }
    return out;
  }

  global.highlightHeaders = highlightHeaders;
  global.highlightXML = highlightXML;
})(window);

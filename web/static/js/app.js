(function () {
  var METHODS = {
    GET: true,
    POST: true,
    PUT: true,
    PATCH: true,
    DELETE: true,
    HEAD: true,
    OPTIONS: true
  };

  function onReady(fn) {
    if (document.readyState === "loading") {
      document.addEventListener("DOMContentLoaded", fn);
    } else {
      fn();
    }
  }

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

  function pick(obj, keys) {
    if (!obj) return undefined;
    for (var i = 0; i < keys.length; i++) {
      if (Object.prototype.hasOwnProperty.call(obj, keys[i]) && obj[keys[i]] != null) {
        return obj[keys[i]];
      }
    }
    return undefined;
  }

  function pairs(list) {
    if (!Array.isArray(list)) return [];
    return list.map(function (item) {
      return {
        name: pick(item, ["Name", "name"]) || "",
        value: pick(item, ["Value", "value"]) || ""
      };
    });
  }

  function methodName(req) {
    return String(pick(req, ["Method", "method"]) || "");
  }

  var ICON_DOC = '<svg class="card-icon" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><path d="M14 2v6h6"/></svg>';
  var ICON_COPY = '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>';
  var HOOK_HEAD = "HTTP/1.1 200 OK\nContent-Type: application/json; charset=utf-8\n\n";

  function relTime(iso) {
    var d = new Date(iso);
    if (!iso || Number.isNaN(d.getTime())) return iso || "";
    var s = Math.max(0, (Date.now() - d.getTime()) / 1000);
    if (s < 45) return "just now";
    if (s < 3600) {
      var m = Math.max(1, Math.round(s / 60));
      return m + (m === 1 ? " minute ago" : " minutes ago");
    }
    if (s < 86400) {
      var h = Math.max(1, Math.round(s / 3600));
      return h + (h === 1 ? " hour ago" : " hours ago");
    }
    var days = Math.max(1, Math.round(s / 86400));
    return days + (days === 1 ? " day ago" : " days ago");
  }

  function stamp(iso) {
    var d = new Date(iso);
    if (!iso || Number.isNaN(d.getTime())) return iso || "";
    var months = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
    function pad(n) { return (n < 10 ? "0" : "") + n; }
    return months[d.getMonth()] + " " + pad(d.getDate()) + " · " + pad(d.getHours()) + ":" + pad(d.getMinutes()) + ":" + pad(d.getSeconds());
  }

  function utf8len(s) {
    if (typeof TextEncoder !== "undefined") return new TextEncoder().encode(s).length;
    var n = 0;
    for (var i = 0; i < s.length; i++) {
      var c = s.charCodeAt(i);
      if (c < 0x80) n += 1;
      else if (c < 0x800) n += 2;
      else if (c >= 0xD800 && c <= 0xDBFF) { n += 4; i++; }
      else n += 3;
    }
    return n;
  }

  function measure(text) {
    text = text || "";
    if (!text) return { lines: 0, bytes: 0 };
    var lines = text.split("\n").length;
    if (text.charAt(text.length - 1) === "\n") lines -= 1;
    if (lines < 1) lines = 1;
    return { lines: lines, bytes: utf8len(text) };
  }

  function statLabel(text) {
    var m = measure(text);
    return m.lines + " " + (m.lines === 1 ? "line" : "lines") + " · " + m.bytes + " " + (m.bytes === 1 ? "byte" : "bytes");
  }

  function looksLikeJSON(s) {
    var c = s.charAt(0);
    return c === "{" || c === "[" || c === '"' || c === "t" || c === "f" || c === "n" || c === "-" || (c >= "0" && c <= "9");
  }

  function goEscape(s) {
    return String(s).replace(/[<>&]/g, function (ch) {
      return "\\u00" + ch.charCodeAt(0).toString(16);
    });
  }

  function prettyJSONText(ctype, body) {
    var text = body == null ? "" : String(body);
    var trimmed = text.trim();
    if (!trimmed) return null;
    var ct = String(ctype || "").toLowerCase();
    var declared = ct.indexOf("/json") !== -1 || ct.indexOf("+json") !== -1;
    if (!declared && !looksLikeJSON(trimmed)) return null;
    try {
      return goEscape(JSON.stringify(JSON.parse(trimmed), null, 2));
    } catch (e) {
      return null;
    }
  }

  function headerBlock(method, path, headers) {
    var lines = [String(method || "") + " " + (path || "/") + " HTTP/1.1"];
    headers.forEach(function (h) {
      lines.push(String(h.name || "") + ": " + String(h.value || ""));
    });
    return lines.join("\n") + "\n\n";
  }

  function clientIP(addr) {
    addr = String(addr || "").trim();
    if (!addr) return "unknown";
    if (addr.charAt(0) === "[") {
      var end = addr.indexOf("]");
      return end > 1 ? addr.slice(1, end) : addr;
    }
    var i = addr.lastIndexOf(":");
    if (i > 0 && addr.indexOf(":") === i) return addr.slice(0, i);
    return addr;
  }

  function methodClass(method) {
    var upper = String(method || "").toUpperCase();
    return METHODS[upper] ? "method method-" + upper : "method method-OTHER";
  }

  function hookStatus(req) {
    var raw = pick(req, ["Status", "status", "StatusCode", "statusCode"]);
    if (raw == null || raw === "") return "200";
    return String(raw);
  }

  function hookBody(binId, reqId) {
    return goEscape(JSON.stringify({ ok: true, bin: binId, request: reqId })) + "\n";
  }

  function colorJSON(root) {
    if (typeof highlightJSON !== "function") return;
    (root || document).querySelectorAll("span.json").forEach(function (el) {
      if (el.getAttribute("data-colored") === "1") return;
      try {
        el.innerHTML = highlightJSON(el.textContent);
        el.setAttribute("data-colored", "1");
      } catch (e) { /* leave the plain text */ }
    });
  }

  function table(rows, emptyText) {
    if (!rows.length) return '<p class="none">' + esc(emptyText) + "</p>";
    var body = rows.map(function (row) {
      return "<tr><th>" + esc(row.name) + "</th><td>" + esc(row.value) + "</td></tr>";
    }).join("");
    return '<table class="kv"><tbody>' + body + "</tbody></table>";
  }

  function emptyList() {
    return (
      '<div class="list-empty">' +
      '<p class="list-empty-title">No requests yet</p>' +
      "<p>Send any HTTP request to this bin URL. It shows up here.</p></div>"
    );
  }

  function reqKey(req, index) {
    var id = pick(req, ["ID", "id"]);
    return id == null || id === "" ? "i" + index : String(id);
  }

  function renderRequests(inspector, reqs, selectedId, selectedView) {
    var list = document.getElementById("req-list");
    var slot = document.getElementById("detail-slot");
    var empty = document.getElementById("empty-detail");
    var count = document.getElementById("req-count");
    var filterCount = document.getElementById("filter-count");
    var footCount = document.getElementById("foot-count");
    var split = inspector.querySelector(".split");
    if (!list || !slot || !split) throw new Error("missing inspector nodes");
    if (selectedView !== "raw") selectedView = "pretty";

    inspector.querySelectorAll("input.sel").forEach(function (el) { el.remove(); });
    var oldStyle = document.getElementById("sel-style");
    if (oldStyle) oldStyle.remove();
    slot.querySelectorAll(".detail-body").forEach(function (el) { el.remove(); });

    var col = inspector.querySelector(".req-col");
    var n = reqs.length;
    var countLabel = n + " request" + (n === 1 ? "" : "s");
    if (count) count.textContent = countLabel;
    if (filterCount) filterCount.textContent = String(n);
    if (footCount) footCount.textContent = countLabel;
    if (col) col.classList.toggle("is-empty", !n);

    if (!n) {
      list.innerHTML = emptyList();
      if (empty) empty.hidden = false;
      return;
    }

    if (empty) empty.hidden = true;

    var selIndex = 0;
    reqs.forEach(function (req, i) {
      if (reqKey(req, i) === selectedId) selIndex = i;
    });

    var binId = inspector.getAttribute("data-bin-id") || "";
    var radios = [];
    var rules = [];
    var labels = [];
    var panes = [];

    reqs.forEach(function (req, i) {
      var method = methodName(req);
      var path = String(pick(req, ["Path", "path"]) || "") || "/";
      var ts = String(pick(req, ["Timestamp", "timestamp"]) || "");
      var addr = String(pick(req, ["RemoteAddr", "remoteAddr"]) || "");
      var ctype = String(pick(req, ["ContentType", "contentType"]) || "");
      var length = pick(req, ["ContentLength", "contentLength"]);
      var size = length == null || length === "" ? 0 : length;
      var headers = pairs(pick(req, ["Headers", "headers"]) || []);
      var query = pairs(pick(req, ["Query", "query"]) || []);
      var form = pairs(pick(req, ["Form", "form"]) || []);
      var body = pick(req, ["Body", "body"]);
      body = body == null ? "" : String(body);
      var reqId = String(pick(req, ["ID", "id"]) || "");
      var head = headerBlock(method, path, headers);
      var raw = head + body;
      var pretty = prettyJSONText(ctype, body);
      var showPretty = !!pretty && !(i === selIndex && selectedView === "raw");
      var respBody = hookBody(binId, reqId);
      var resp = HOOK_HEAD + respBody;

      radios.push(
        '<input class="sel" type="radio" name="selected-request" id="sel-' + i +
        '" data-id="' + esc(reqKey(req, i)) + '"' + (i === selIndex ? " checked" : "") + ">"
      );
      rules.push('#sel-' + i + ':checked ~ .split label[for="sel-' + i + '"]{background:#e7f0ff}');
      rules.push('#sel-' + i + ':focus-visible ~ .split label[for="sel-' + i + '"]{outline:2px solid #2563eb;outline-offset:-2px}');
      rules.push("#sel-" + i + ":checked ~ .split #pane-" + i + "{display:flex}");
      labels.push(
        '<label class="req-row" for="sel-' + i + '" title="' + esc(method + " " + path) + '">' +
        '<span class="http-pill">HTTP</span>' +
        '<span class="req-ip">' + esc(clientIP(addr)) + "</span>" +
        '<time class="req-time" datetime="' + esc(ts) + '">' + esc(relTime(ts)) + "</time></label>"
      );

      var viewInputs = '<input class="view-input" type="radio" name="view-' + i + '" id="view-' + i + '-raw" value="raw"' + (showPretty ? "" : " checked") + ">";
      var prettyControl = '<span class="is-disabled" title="Body is not JSON">Pretty JSON</span>';
      var prettyView = "";
      var prettyStat = "";
      if (pretty) {
        viewInputs += '<input class="view-input" type="radio" name="view-' + i + '" id="view-' + i + '-pretty" value="pretty"' + (showPretty ? " checked" : "") + ">";
        prettyControl = '<label for="view-' + i + '-pretty">Pretty JSON</label>';
        prettyStat = '<span class="msg-stat stat-pretty">' + esc(statLabel(head + pretty)) + "</span>";
        prettyView = '<pre class="msg view-pretty"><span class="msg-lead">' + esc(head) + '</span><span class="json">' + esc(pretty) + "</span></pre>";
      }

      panes.push(
        '<article class="detail-body" id="pane-' + i + '">' +
        '<header class="detail-head">' +
        '<a class="detail-back" href="#req-list">Back to list</a>' +
        '<div class="detail-line">' +
        '<span class="' + methodClass(method) + '">' + esc(method) + "</span>" +
        '<span class="detail-sep" aria-hidden="true">/</span>' +
        '<span class="status-pill" title="HTTP status">' + esc(hookStatus(req)) + "</span>" +
        '<span class="detail-size">' + esc(size) + " B</span>" +
        '<span class="detail-aside">' +
        '<span class="detail-ip">' + esc(clientIP(addr)) + "</span>" +
        '<time class="detail-when" datetime="' + esc(ts) + '">' + esc(stamp(ts)) + "</time></span></div></header>" +
        '<section class="req-card">' + viewInputs +
        '<header class="card-head"><h2>' + ICON_DOC + 'Request <span class="http-tag">HTTP</span></h2>' +
        '<span class="msg-stat stat-raw">' + esc(statLabel(raw)) + "</span>" + prettyStat +
        '<div class="view-toggle" role="group" aria-label="Request body format">' +
        '<label for="view-' + i + '-raw">Raw</label>' + prettyControl + "</div>" +
        '<button class="icon-btn" type="button" data-copy-visible aria-label="Copy request">' + ICON_COPY + "</button></header>" +
        '<pre class="msg view-raw">' + esc(raw) + "</pre>" + prettyView + "</section>" +
        '<section class="kv-card"><h3>Query <span class="kv-n">' + query.length + "</span></h3>" +
        table(query, "No query parameters.") +
        '<h3>Form <span class="kv-n">' + form.length + "</span></h3>" +
        table(form, "No form fields.") + "</section>" +
        '<section class="req-card"><header class="card-head"><h2>' + ICON_DOC + 'Response <span class="http-tag">HTTP</span></h2>' +
        '<span class="msg-stat">' + esc(statLabel(resp)) + "</span>" +
        '<button class="icon-btn" type="button" data-copy-target="resp-' + i + '" data-label="Copy response" aria-label="Copy response">' + ICON_COPY + "</button></header>" +
        '<pre class="msg" id="resp-' + i + '"><span class="msg-lead">' + esc(HOOK_HEAD) + '</span><span class="json">' + esc(respBody) + "</span></pre></section></article>"
      );
    });

    split.insertAdjacentHTML("beforebegin", radios.join(""));
    list.innerHTML = labels.join("");
    slot.insertAdjacentHTML("beforeend", panes.join(""));
    var style = document.createElement("style");
    style.id = "sel-style";
    style.textContent = rules.join("\n");
    inspector.appendChild(style);
    colorJSON(inspector);
  }

  function currentSelection(inspector) {
    var checked = inspector.querySelector("input.sel:checked");
    var id = checked ? checked.getAttribute("data-id") : "";
    var view = "pretty";
    if (checked) {
      var pane = document.getElementById(checked.id.replace("sel-", "pane-"));
      var viewInput = pane && pane.querySelector("input.view-input:checked");
      if (viewInput) view = viewInput.value;
    }
    return { id: id || "", view: view };
  }

  function markNav() {
    var path = location.pathname;
    document.querySelectorAll("[data-nav]").forEach(function (link) {
      var key = link.getAttribute("data-nav");
      var on = false;
      if (key === "tools") {
        on = path === "/tools" || path === "/httpbin";
      } else if (key === "bins") {
        on = path === "/bins" || path.indexOf("/bins/") === 0;
      }
      if (on) link.setAttribute("aria-current", "page");
      else link.removeAttribute("aria-current");
    });
  }

  var copyTimer;

  function markCopied(btn) {
    if (!btn) return;
    if (btn.querySelector("svg")) {
      var prev = btn.getAttribute("aria-label") || "Copy";
      btn.classList.add("is-copied");
      btn.setAttribute("aria-label", "Copied");
      clearTimeout(copyTimer);
      copyTimer = setTimeout(function () {
        btn.classList.remove("is-copied");
        btn.setAttribute("aria-label", prev);
      }, 1500);
      return;
    }
    var previous = btn.getAttribute("data-label") || btn.textContent || "Copy";
    if (!btn.getAttribute("data-label")) btn.setAttribute("data-label", previous);
    btn.textContent = "Copied";
    clearTimeout(copyTimer);
    copyTimer = setTimeout(function () { btn.textContent = previous; }, 1500);
  }

  function fallbackCopy(text, done) {
    var area = document.createElement("textarea");
    area.value = text;
    area.setAttribute("readonly", "");
    area.style.position = "fixed";
    area.style.left = "-9999px";
    document.body.appendChild(area);
    area.select();
    try {
      if (document.execCommand("copy")) done();
    } catch (err) { /* leave the text uncopied */ }
    area.remove();
  }

  function copyText(text, btn) {
    var finish = function () { markCopied(btn); };
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text).then(finish).catch(function () { fallbackCopy(text, finish); });
      return;
    }
    fallbackCopy(text, finish);
  }

  function bindCopy() {
    document.addEventListener("click", function (event) {
      var vis = event.target.closest && event.target.closest("[data-copy-visible]");
      if (vis) {
        var card = vis.closest(".req-card");
        var checked = card && card.querySelector("input.view-input:checked");
        var mode = checked ? checked.value : "raw";
        var node = card && card.querySelector(mode === "pretty" ? ".view-pretty" : ".view-raw");
        copyText(node ? node.textContent : "", vis);
        return;
      }
      var btn = event.target.closest && event.target.closest("[data-copy-target]");
      if (!btn) return;
      var el = document.getElementById(btn.getAttribute("data-copy-target"));
      if (!el) return;
      var text = el.tagName === "INPUT" || el.tagName === "TEXTAREA" ? (el.value || "") : (el.textContent || "");
      if (el.tagName !== "PRE") text = text.trim();
      copyText(text, btn);
    });
    document.addEventListener("submit", function (event) {
      var form = event.target;
      if (!form || !form.getAttribute) return;
      var msg = form.getAttribute("data-confirm");
      if (!msg) return;
      if (!window.confirm(msg)) event.preventDefault();
    });
  }

  var BIN_KEY = "requestbin.bins";
  var POLL_MS = 10000;

  function activeBinId() {
    var match = location.pathname.match(/^\/bins\/([^/]+)/);
    return match ? decodeURIComponent(match[1]) : "";
  }

  function binID(bin) {
    return String((bin && (bin.id || bin.ID)) || "");
  }

  function readStoredBins() {
    try {
      var raw = localStorage.getItem(BIN_KEY);
      if (raw == null) return { missing: true, ids: [] };
      var parsed = JSON.parse(raw);
      if (!Array.isArray(parsed)) return { missing: true, ids: [] };
      var ids = [];
      var seen = {};
      parsed.forEach(function (id) {
        id = String(id || "");
        if (!id || seen[id]) return;
        seen[id] = true;
        ids.push(id);
      });
      return { missing: false, ids: ids };
    } catch (e) {
      return { broken: true, missing: false, ids: [] };
    }
  }

  function sameIds(a, b) {
    if (a.length !== b.length) return false;
    for (var i = 0; i < a.length; i++) {
      if (a[i] !== b[i]) return false;
    }
    return true;
  }

  function writeStoredBins(ids) {
    var seen = {};
    var out = [];
    ids.forEach(function (id) {
      id = String(id || "");
      if (!id || seen[id]) return;
      seen[id] = true;
      out.push(id);
    });
    try {
      localStorage.setItem(BIN_KEY, JSON.stringify(out));
      return true;
    } catch (e) {
      return false;
    }
  }

  function rememberBin(id) {
    if (!id) return;
    var stored = readStoredBins();
    if (stored.missing || stored.broken || stored.ids.indexOf(id) !== -1) return;
    stored.ids.unshift(id);
    writeStoredBins(stored.ids);
  }

  function forgetBin(id) {
    var stored = readStoredBins();
    if (stored.missing || stored.broken) return;
    var next = stored.ids.filter(function (item) { return item !== id; });
    if (next.length === stored.ids.length) return;
    writeStoredBins(next);
  }

  // Bins this browser knows about that the server still has.
  // An empty localStorage on first visit is seeded from the server list.
  function binsForBrowser(serverBins) {
    var stored = readStoredBins();
    if (stored.broken) return serverBins;
    var ids = stored.ids.slice();
    if (stored.missing) {
      ids = serverBins.map(binID).filter(Boolean);
    }
    var active = activeBinId();
    if (active) {
      var onServer = serverBins.some(function (bin) { return binID(bin) === active; });
      if (onServer && ids.indexOf(active) === -1) ids.push(active);
    }
    var keep = {};
    ids.forEach(function (id) { keep[id] = true; });
    var visible = [];
    var kept = [];
    serverBins.forEach(function (bin) {
      var id = binID(bin);
      if (!id || !keep[id]) return;
      visible.push(bin);
      kept.push(id);
    });
    if (stored.missing || !sameIds(stored.ids, kept)) writeStoredBins(kept);
    return visible;
  }

  function retainKnownBinLinks() {
    var stored = readStoredBins();
    if (stored.missing || stored.broken) return;
    var list = document.getElementById("bin-list");
    if (!list) return;
    var known = {};
    stored.ids.forEach(function (id) { known[id] = true; });
    var active = activeBinId();
    if (active) known[active] = true;
    var links = Array.prototype.slice.call(list.querySelectorAll("a.bin-link"));
    if (!links.length) return;
    var kept = 0;
    links.forEach(function (link) {
      var idEl = link.querySelector(".bin-link-id");
      var id = idEl ? idEl.textContent : "";
      if (!known[id]) link.remove();
      else kept++;
    });
    if (!kept) list.innerHTML = '<p class="side-empty">No bins yet</p>';
  }

  function renderBinList(bins) {
    var list = document.getElementById("bin-list");
    if (!list) return;
    var active = activeBinId();
    if (!bins.length) {
      list.innerHTML = '<p class="side-empty">No bins yet</p>';
      return;
    }
    list.innerHTML = bins.map(function (bin) {
      var id = String(bin.id || bin.ID || "");
      var n = bin.requests != null ? bin.requests : (bin.Requests || 0);
      var cls = "bin-link" + (id === active ? " is-active" : "");
      var current = id === active ? ' aria-current="page"' : "";
      return '<a class="' + cls + '" href="/bins/' + esc(id) + '"' + current + ">" +
        '<span class="bin-link-id">' + esc(id) + "</span>" +
        '<span class="bin-link-meta">' + esc(n) + " request" + (Number(n) === 1 ? "" : "s") + "</span></a>";
    }).join("");
  }

  function startBinListPoll() {
    var list = document.getElementById("bin-list");
    if (!list) return;
    var lastSig = "";
    var inFlight = false;

    function tick() {
      if (inFlight) return;
      inFlight = true;
      fetch("/api/bins", { headers: { Accept: "application/json" }, cache: "no-store" })
        .then(function (res) {
          if (!res.ok) throw new Error("status " + res.status);
          return res.json();
        })
        .then(function (data) {
          var bins = data && Array.isArray(data.bins) ? data.bins : [];
          var visible = binsForBrowser(bins);
          var sig = JSON.stringify(visible) + "|" + activeBinId();
          if (sig === lastSig) return;
          renderBinList(visible);
          lastSig = sig;
        })
        .catch(function () { /* keep the server-rendered list */ })
        .then(function () { inFlight = false; });
    }

    tick();
    setInterval(tick, POLL_MS);
  }

  function startBinPoll() {
    var inspector = document.querySelector(".inspector[data-bin-id]");
    if (!inspector) return;
    var binId = inspector.getAttribute("data-bin-id");
    if (!binId || location.pathname.indexOf("/bins/") !== 0) return;
    document.title = "Bin " + binId + " · RequestBin";

    var lastSig = "";
    var lastCount = inspector.querySelectorAll("input.sel").length;
    var inFlight = false;
    var pendingForce = false;
    var live = document.getElementById("live-dot");

    function tick(force) {
      if (inFlight) {
        if (force) pendingForce = true;
        return;
      }
      inFlight = true;
      var forced = !!force;
      fetch("/api/bins/" + encodeURIComponent(binId) + "/requests", {
        headers: { Accept: "application/json" },
        cache: "no-store"
      }).then(function (res) {
        if (res.status === 404) {
          forgetBin(binId);
          if (activeBinId() === binId) location.assign("/bins");
          return null;
        }
        if (!res.ok) throw new Error("status " + res.status);
        return res.json();
      }).then(function (data) {
        if (!data) return;
        var reqs = data && Array.isArray(data.requests) ? data.requests : [];
        var sig = JSON.stringify(reqs);
        if (sig === lastSig && !forced) {
          if (live) live.hidden = false;
          return;
        }
        var prevCount = lastCount;
        var selected = currentSelection(inspector);
        try {
          renderRequests(inspector, reqs, selected.id, selected.view);
          lastSig = sig;
          lastCount = reqs.length;
          if (live) live.hidden = false;
        } catch (err) {
          if (reqs.length > prevCount) location.reload();
        }
      }).catch(function () {
        /* keep the server-rendered inspector if the poll fails */
      }).then(function () {
        inFlight = false;
        if (pendingForce) {
          pendingForce = false;
          tick(true);
        }
      });
    }

    var refresh = document.getElementById("refresh-bin");
    if (refresh) refresh.addEventListener("click", function () { tick(true); });
    tick();
    setInterval(function () { tick(false); }, POLL_MS);
  }

  onReady(function () {
    markNav();
    bindCopy();
    var active = activeBinId();
    var stored = readStoredBins();
    if (active && !stored.missing && !stored.broken) rememberBin(active);
    retainKnownBinLinks();
    colorJSON(document);
    startBinPoll();
    startBinListPoll();
  });
})();

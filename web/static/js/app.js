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

  function prettyHookBody(binId, reqId) {
    var pretty = prettyJSONText("application/json", JSON.stringify({ ok: true, bin: binId, request: reqId }));
    return pretty || hookBody(binId, reqId);
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

  function renderRequests(inspector, reqs, selectedId, selectedView, respView) {
    var list = document.getElementById("req-list");
    var slot = document.getElementById("detail-slot");
    var empty = document.getElementById("empty-detail");
    var count = document.getElementById("req-count");
    var filterCount = document.getElementById("filter-count");
    var footCount = document.getElementById("foot-count");
    var split = inspector.querySelector(".split");
    if (!list || !slot || !split) throw new Error("missing inspector nodes");
    if (selectedView !== "raw") selectedView = "pretty";
    if (respView !== "raw") respView = "pretty";

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
      var respPretty = prettyHookBody(binId, reqId);
      var resp = HOOK_HEAD + respBody;
      var respPrettyMsg = HOOK_HEAD + respPretty;
      var showRespPretty = !(i === selIndex && respView === "raw");

      radios.push(
        '<input class="sel" type="radio" name="selected-request" id="sel-' + i +
        '" data-id="' + esc(reqKey(req, i)) + '"' + (i === selIndex ? " checked" : "") + ">"
      );
      rules.push('#sel-' + i + ':checked ~ .split label[for="sel-' + i + '"]{background:var(--bg-selected)}');
      rules.push('#sel-' + i + ':focus-visible ~ .split label[for="sel-' + i + '"]{outline:2px solid var(--blue-600);outline-offset:-2px}');
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
        '<section class="req-card">' +
        '<input class="view-input" type="radio" name="resp-view-' + i + '" id="resp-view-' + i + '-raw" value="raw"' + (showRespPretty ? "" : " checked") + ">" +
        '<input class="view-input" type="radio" name="resp-view-' + i + '" id="resp-view-' + i + '-pretty" value="pretty"' + (showRespPretty ? " checked" : "") + ">" +
        '<header class="card-head"><h2>' + ICON_DOC + 'Response <span class="http-tag">HTTP</span></h2>' +
        '<span class="msg-stat stat-raw">' + esc(statLabel(resp)) + "</span>" +
        '<span class="msg-stat stat-pretty">' + esc(statLabel(respPrettyMsg)) + "</span>" +
        '<div class="view-toggle" role="group" aria-label="Response body format">' +
        '<label for="resp-view-' + i + '-raw">Raw</label>' +
        '<label for="resp-view-' + i + '-pretty">Pretty JSON</label></div>' +
        '<button class="icon-btn" type="button" data-copy-visible aria-label="Copy response">' + ICON_COPY + "</button></header>" +
        '<pre class="msg view-raw">' + esc(resp) + "</pre>" +
        '<pre class="msg view-pretty"><span class="msg-lead">' + esc(HOOK_HEAD) + '</span><span class="json">' + esc(respPretty) + "</span></pre></section></article>"
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
    var respView = "pretty";
    if (checked) {
      var pane = document.getElementById(checked.id.replace("sel-", "pane-"));
      var cards = pane ? pane.querySelectorAll(".req-card") : [];
      var reqInput = cards[0] && cards[0].querySelector("input.view-input:checked");
      var respInput = cards[1] && cards[1].querySelector("input.view-input:checked");
      if (reqInput) view = reqInput.value;
      if (respInput) respView = respInput.value;
    }
    return { id: id || "", view: view, respView: respView };
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
      var action = form.getAttribute("action") || "";
      var nameMatch = action.match(/^\/bins\/([^/]+)\/name$/);
      if (nameMatch) {
        event.preventDefault();
        saveBinName(form, decodeURIComponent(nameMatch[1]));
        return;
      }
      var msg = form.getAttribute("data-confirm");
      if (!msg) return;
      if (!window.confirm(msg)) {
        event.preventDefault();
        return;
      }
      var action = form.getAttribute("action") || "";
      var binMatch = action.match(/^\/bins\/([^/]+)\/(?:clear|delete)$/);
      if (!binMatch) return;
      var ownedKey = binKey(decodeURIComponent(binMatch[1]));
      if (!ownedKey) {
        event.preventDefault();
        return;
      }
      var input = form.querySelector('input[name="key"]');
      if (!input) {
        input = document.createElement("input");
        input.type = "hidden";
        input.name = "key";
        form.appendChild(input);
      }
      input.value = ownedKey;
    });
  }

  var KEYS_KEY = "requestbin.binKeys";
  var POLL_MS = 10000;

  function activeBinId() {
    var match = location.pathname.match(/^\/bins\/([^/]+)/);
    return match ? decodeURIComponent(match[1]) : "";
  }

  function binID(bin) {
    return String((bin && (bin.id || bin.ID)) || "");
  }

  function readBinKeys() {
    try {
      var raw = localStorage.getItem(KEYS_KEY);
      if (!raw) return {};
      var parsed = JSON.parse(raw);
      if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) return {};
      var out = {};
      Object.keys(parsed).forEach(function (id) {
        var key = String(parsed[id] || "");
        id = String(id || "");
        if (!id || !key) return;
        out[id] = key;
      });
      return out;
    } catch (e) {
      return {};
    }
  }

  function writeBinKeys(keys) {
    try {
      localStorage.setItem(KEYS_KEY, JSON.stringify(keys));
      localStorage.removeItem("requestbin.bins");
      return true;
    } catch (e) {
      return false;
    }
  }

  function binKey(id) {
    if (!id) return "";
    return readBinKeys()[id] || "";
  }

  function saveBinKey(id, key) {
    if (!id || !key) return;
    var keys = readBinKeys();
    if (keys[id] === key) return;
    keys[id] = key;
    writeBinKeys(keys);
  }

  function forgetBin(id) {
    var keys = readBinKeys();
    if (!keys[id]) return;
    delete keys[id];
    writeBinKeys(keys);
  }

  function captureBinKeyFromHash() {
    var id = activeBinId();
    var match = location.hash.match(/^#k=([0-9a-fA-F]{32})$/);
    if (!id || !match) return;
    saveBinKey(id, match[1]);
    history.replaceState(null, "", location.pathname + location.search);
  }

  function authHeaders(extra) {
    var headers = { Accept: "application/json" };
    var keys = readBinKeys();
    var ids = Object.keys(keys);
    if (ids.length) headers["X-Bin-Keys"] = JSON.stringify(keys);
    if (extra) {
      Object.keys(extra).forEach(function (name) { headers[name] = extra[name]; });
    }
    return headers;
  }

  var refreshBinList = function () {};

  function binName(bin) {
    return String((bin && (bin.name || bin.Name)) || "");
  }

  function applyVisibleName(id, name) {
    if (activeBinId() !== id) return;
    document.title = (name || ("Bin " + id)) + " · RequestBin";
    var crumb = document.getElementById("bin-crumb");
    if (crumb) crumb.textContent = name || id;
    var input = document.getElementById("bin-name");
    if (!input || document.activeElement === input) return;
    var unsaved = (input.value || "") !== (input.getAttribute("data-saved") || "");
    if (unsaved) return;
    input.value = name || "";
    input.setAttribute("data-saved", name || "");
  }

  function saveBinName(form, id) {
    var input = form.querySelector('input[name="name"]');
    if (!input) return;
    var next = input.value;
    var previous = input.getAttribute("data-saved") || "";
    if (next === previous) return;
    var key = binKey(id);
    if (!key) return;
    input.setAttribute("data-saved", next);
    var body = new URLSearchParams();
    body.set("key", key);
    body.set("name", next);
    fetch(form.getAttribute("action"), {
      method: "POST",
      headers: {
        Accept: "application/json",
        "Content-Type": "application/x-www-form-urlencoded"
      },
      body: body.toString()
    }).then(function (res) {
      if (!res.ok) throw new Error("status " + res.status);
      return res.json();
    }).then(function (data) {
      var name = data && data.name ? String(data.name) : "";
      input.value = name;
      input.setAttribute("data-saved", name);
      applyVisibleName(id, name);
      refreshBinList();
    }).catch(function () {
      input.setAttribute("data-saved", previous);
      if (document.activeElement !== input) input.value = previous;
    });
  }

  function bindBinName() {
    var input = document.getElementById("bin-name");
    if (!input || !input.form) return;
    input.addEventListener("keydown", function (ev) {
      if (ev.key !== "Escape") return;
      input.value = input.getAttribute("data-saved") || "";
      input.blur();
    });
    input.addEventListener("blur", function () {
      if ((input.value || "") === (input.getAttribute("data-saved") || "")) return;
      if (input.form.requestSubmit) input.form.requestSubmit();
    });
  }

  function renderBinList(bins) {
    document.documentElement.setAttribute("data-own-bins", bins && bins.length ? "1" : "0");
    var list = document.getElementById("bin-list");
    if (!list) return;
    var active = activeBinId();
    if (!bins.length) {
      list.innerHTML = '<p class="side-empty">No bins yet</p>';
      return;
    }
    list.innerHTML = bins.map(function (bin) {
      var id = String(bin.id || bin.ID || "");
      var name = binName(bin);
      var n = bin.requests != null ? bin.requests : (bin.Requests || 0);
      var cls = "bin-link" + (id === active ? " is-active" : "");
      var current = id === active ? ' aria-current="page"' : "";
      var titleClass = "bin-link-title" + (name ? "" : " is-id");
      var meta = (name ? '<span class="bin-link-id">' + esc(id) + "</span>" : "") +
        "<span>" + esc(n) + " request" + (Number(n) === 1 ? "" : "s") + "</span>";
      if (id === active) applyVisibleName(id, name);
      return '<a class="' + cls + '" href="/bins/' + esc(id) + '"' + current + ">" +
        '<span class="' + titleClass + '">' + esc(name || id) + "</span>" +
        '<span class="bin-link-meta">' + meta + "</span></a>";
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
      fetch("/api/bins", { headers: authHeaders(), cache: "no-store" })
        .then(function (res) {
          if (!res.ok) throw new Error("status " + res.status);
          return res.json();
        })
        .then(function (data) {
          var bins = data && Array.isArray(data.bins) ? data.bins : [];
          var kept = {};
          var known = readBinKeys();
          bins.forEach(function (bin) {
            var id = binID(bin);
            if (id && known[id]) kept[id] = known[id];
          });
          if (Object.keys(known).length !== Object.keys(kept).length) writeBinKeys(kept);
          var sig = JSON.stringify(bins) + "|" + activeBinId();
          if (sig === lastSig) return;
          renderBinList(bins);
          lastSig = sig;
        })
        .catch(function () { /* keep the server-rendered list */ })
        .then(function () { inFlight = false; });
    }

    refreshBinList = function () {
      lastSig = "";
      tick();
    };
    tick();
    setInterval(tick, POLL_MS);
  }

  function startBinPoll() {
    var inspector = document.querySelector(".inspector[data-bin-id]");
    if (!inspector) return;
    var binId = inspector.getAttribute("data-bin-id");
    if (!binId || location.pathname.indexOf("/bins/") !== 0) return;
    document.title = "Bin " + binId + " · RequestBin";
    var key = binKey(binId);
    if (!key) {
      document.documentElement.removeAttribute("data-owns-bin");
      inspector.setAttribute("data-locked", "true");
      return;
    }

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
        headers: authHeaders({ "X-Bin-Key": key }),
        cache: "no-store"
      }).then(function (res) {
        if (res.status === 404) {
          forgetBin(binId);
          if (activeBinId() === binId) location.assign("/bins");
          return null;
        }
        if (res.status === 401 || res.status === 403) {
          document.documentElement.removeAttribute("data-owns-bin");
          inspector.setAttribute("data-locked", "true");
          return null;
        }
        if (!res.ok) throw new Error("status " + res.status);
        inspector.removeAttribute("data-locked");
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
          renderRequests(inspector, reqs, selected.id, selected.view, selected.respView);
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

  var THEME_KEY = "requestbin-theme";

  function themeMode() {
    var mode = document.documentElement.getAttribute("data-theme") || "auto";
    if (mode !== "light" && mode !== "dark" && mode !== "auto") return "auto";
    return mode;
  }

  function applyTheme(mode) {
    if (mode !== "light" && mode !== "dark" && mode !== "auto") mode = "auto";
    document.documentElement.setAttribute("data-theme", mode);
    try { localStorage.setItem(THEME_KEY, mode); } catch (e) {}
    var buttons = document.querySelectorAll(".theme-switch [data-theme-value]");
    for (var i = 0; i < buttons.length; i++) {
      var on = buttons[i].getAttribute("data-theme-value") === mode;
      buttons[i].setAttribute("aria-checked", on ? "true" : "false");
    }
  }

  function bindTheme() {
    var group = document.querySelector(".theme-switch");
    if (!group) return;
    applyTheme(themeMode());
    group.addEventListener("click", function (ev) {
      var btn = ev.target.closest("[data-theme-value]");
      if (!btn || !group.contains(btn)) return;
      applyTheme(btn.getAttribute("data-theme-value"));
    });
    group.addEventListener("keydown", function (ev) {
      if (ev.key !== "ArrowLeft" && ev.key !== "ArrowRight" && ev.key !== "ArrowUp" && ev.key !== "ArrowDown") return;
      var buttons = Array.prototype.slice.call(group.querySelectorAll("[data-theme-value]"));
      var index = buttons.indexOf(document.activeElement);
      if (index < 0) return;
      ev.preventDefault();
      var dir = (ev.key === "ArrowRight" || ev.key === "ArrowDown") ? 1 : -1;
      var next = buttons[(index + dir + buttons.length) % buttons.length];
      next.focus();
      applyTheme(next.getAttribute("data-theme-value"));
    });
  }

  onReady(function () {
    bindTheme();
    markNav();
    bindCopy();
    captureBinKeyFromHash();
    bindBinName();
    colorJSON(document);
    startBinPoll();
    startBinListPoll();
  });
})();

/* Malachi Mail website: "Report a problem with the app" in the footer
   opens the page's <dialog class="report">, a form that goes to the
   admin's public endpoint (POST /api/v1/reports, multipart). Cloudflare
   Turnstile is loaded only when the form is first opened; a screenshot
   is scaled to at most 2560 px on its longest side and re-encoded (WebP,
   else JPEG) in a canvas, which drops its EXIF. Escape and a click beside
   the form close it. Without this script the link goes to the GitHub
   issues. An address ending in #report (Report an Issue in the apps'
   About window) opens the form when the page loads. */
(function () {
  var dialog = document.querySelector("dialog.report");
  if (!dialog || typeof dialog.showModal !== "function" || !window.fetch || !window.FormData) { return; }
  var form = dialog.querySelector("form"), status = dialog.querySelector(".rp-status");
  var send = dialog.querySelector("button[type=submit]"), close = dialog.querySelector(".rp-close");
  var file = form.elements.screenshot, preview = dialog.querySelector(".rp-preview");
  var box = dialog.querySelector(".rp-turnstile");

  var local = location.hostname === "localhost" || location.hostname === "127.0.0.1";
  // Site key of the Turnstile widget "Malachi report" (hostname malachimail.app) in the Cloudflare
  // dashboard; on localhost Cloudflare's test key, which always passes.
  var SITEKEY = local ? "1x00000000000000000000AA" : "0x4AAAAAAFxBMHDxSS8hmg2T";
  var ENDPOINT = local ? "http://localhost:8789/api/v1/reports" : "https://admin.malachimail.app/api/v1/reports";
  var TURNSTILE = "https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit";
  var MAX_SIDE = 2560, MAX_BYTES = 5 * 1024 * 1024, MAX_TEXT = 8000;

  var locale = (document.documentElement.lang || "en").toLowerCase().indexOf("cs") === 0 ? "cs" : "en";
  var T = {
    en: {
      sending: "Sending…",
      thanks: "Thank you, the report has arrived. Its number is ",
      invalid: "The report was not accepted. Check the description and the e-mail address, and try again.",
      tooLarge: "The report is too large: the description may have at most 8000 bytes and the screenshot 5 MiB.",
      unavailable: "Reports cannot be received right now. Please try again in a while.",
      network: "The report could not be sent: the connection failed. Please try again.",
      check: "The check by Cloudflare Turnstile has not finished yet. Please wait a moment.",
      loadFailed: "Cloudflare Turnstile could not be loaded, so the report cannot be sent. You can report the problem on GitHub instead.",
      image: "The screenshot could not be read. Choose a PNG, JPEG or WebP picture."
    },
    cs: {
      sending: "Odesílá se…",
      thanks: "Děkujeme, hlášení dorazilo. Jeho číslo je ",
      invalid: "Hlášení nebylo přijato. Zkontrolujte popis a e-mailovou adresu a zkuste to znovu.",
      tooLarge: "Hlášení je příliš velké: popis smí mít nejvýš 8000 bajtů a snímek 5 MiB.",
      unavailable: "Hlášení teď nelze přijmout. Zkuste to prosím za chvíli.",
      network: "Hlášení se nepodařilo odeslat: spojení selhalo. Zkuste to prosím znovu.",
      check: "Ověření službou Cloudflare Turnstile ještě neskončilo. Chvíli prosím počkejte.",
      loadFailed: "Cloudflare Turnstile se nepodařilo načíst, hlášení proto nelze odeslat. Problém můžete nahlásit na GitHubu.",
      image: "Snímek se nepodařilo přečíst. Vyberte obrázek PNG, JPEG nebo WebP."
    }
  }[locale];

  var loading = false, widget = null, shown = null, busy = false, downOnBackdrop = false;

  function say(text, kind) {
    status.textContent = text;
    status.className = "rp-status" + (kind ? " " + kind : "");
  }

  function render() {
    if (widget !== null || !window.turnstile) { return; }
    widget = window.turnstile.render(box, {
      sitekey: SITEKEY,
      action: "report",
      theme: "auto",
      language: locale,
      size: window.innerWidth < 360 ? "compact" : "flexible"
    });
  }
  function loadTurnstile() {
    if (loading) { return; }
    loading = true;
    var s = document.createElement("script");
    s.src = TURNSTILE;
    s.onload = render;
    s.onerror = function () { loading = false; say(T.loadFailed, "error"); };
    document.head.appendChild(s);
  }
  function resetTurnstile() { if (widget !== null && window.turnstile) { window.turnstile.reset(widget); } }

  // Every opening starts with an empty form, unless a report is still being sent.
  function open() {
    if (!busy) { form.reset(); clearPreview(); say(""); }
    if (!dialog.open) { dialog.showModal(); }
    loadTurnstile();
  }

  // The chosen picture, scaled and re-encoded; calls done(blob) or done(null).
  function encode(f, done) {
    var url = URL.createObjectURL(f), img = new Image();
    img.onerror = function () { URL.revokeObjectURL(url); done(null); };
    img.onload = function () {
      URL.revokeObjectURL(url);
      var w = img.naturalWidth, h = img.naturalHeight, k = Math.min(1, MAX_SIDE / Math.max(w, h));
      var c = document.createElement("canvas");
      c.width = Math.max(1, Math.round(w * k));
      c.height = Math.max(1, Math.round(h * k));
      var g = c.getContext("2d");
      g.fillStyle = "#fff"; // JPEG has no transparency
      g.fillRect(0, 0, c.width, c.height);
      g.drawImage(img, 0, 0, c.width, c.height);
      c.toBlob(function (b) {
        if (b && b.type === "image/webp") { done(b); return; }
        c.toBlob(function (j) { done(j); }, "image/jpeg", 0.88);
      }, "image/webp", 0.86);
    };
    img.src = url;
  }

  function failed(code) {
    busy = false;
    send.disabled = false;
    say(T[code] || T.invalid, "error");
    resetTurnstile();
  }

  function post(fd) {
    fetch(ENDPOINT, { method: "POST", body: fd }).then(function (r) {
      return r.json().catch(function () { return {}; }).then(function (j) {
        if (r.status === 201 && j.id) {
          busy = false;
          send.disabled = false;
          form.reset();
          clearPreview();
          resetTurnstile();
          say(T.thanks + j.id + ".", "ok");
          return;
        }
        var code = j.error;
        if (code !== "invalid" && code !== "tooLarge" && code !== "unavailable") {
          code = r.status === 413 ? "tooLarge" : r.status === 503 ? "unavailable" : "invalid";
        }
        failed(code);
      });
    }, function () { failed("network"); });
  }

  function clearPreview() {
    if (shown) { URL.revokeObjectURL(shown); shown = null; }
    preview.removeAttribute("src");
    preview.hidden = true;
  }

  document.addEventListener("click", function (e) {
    var a = e.target.closest ? e.target.closest("[data-report]") : null;
    if (!a || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey || e.button !== 0) { return; }
    e.preventDefault();
    open();
  });
  dialog.addEventListener("mousedown", function (e) { downOnBackdrop = e.target === dialog; });
  dialog.addEventListener("click", function (e) {
    if (e.target === dialog && downOnBackdrop) { dialog.close(); }
    downOnBackdrop = false;
  });
  close.addEventListener("click", function () { dialog.close(); });

  file.addEventListener("change", function () {
    clearPreview();
    var f = file.files && file.files[0];
    if (!f) { return; }
    shown = URL.createObjectURL(f);
    preview.src = shown;
    preview.hidden = false;
  });

  form.addEventListener("submit", function (e) {
    e.preventDefault();
    if (busy) { return; }
    var fd = new FormData(form);
    var text = String(fd.get("text") || "");
    if (!text.trim()) { say(T.invalid, "error"); return; }
    if (window.TextEncoder && new TextEncoder().encode(text).length > MAX_TEXT) { say(T.tooLarge, "error"); return; }
    var token = widget !== null && window.turnstile ? window.turnstile.getResponse(widget) : "";
    if (!token) { say(loading ? T.check : T.loadFailed, "error"); loadTurnstile(); return; }
    fd.set("cf-turnstile-response", token);
    fd.set("locale", locale);
    fd.set("page", location.pathname);
    fd["delete"]("screenshot");

    busy = true;
    send.disabled = true;
    say(T.sending);
    var f = file.files && file.files[0];
    if (!f) { post(fd); return; }
    encode(f, function (b) {
      if (!b) { busy = false; send.disabled = false; say(T.image, "error"); return; }
      if (b.size > MAX_BYTES) { busy = false; send.disabled = false; say(T.tooLarge, "error"); return; }
      fd.set("screenshot", b, b.type === "image/webp" ? "screenshot.webp" : "screenshot.jpg");
      post(fd);
    });
  });

  // #report in the address opens the form; the fragment is dropped, so a
  // reload or Back does not open it again.
  function fromHash() {
    if (location.hash !== "#report") { return; }
    history.replaceState(null, "", location.pathname + location.search);
    open();
  }
  window.addEventListener("hashchange", fromHash);
  fromHash();
})();

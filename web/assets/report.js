/* Malachi Mail website: the two forms in the footer, each a
   <dialog class="report" data-form="…"> over the page that goes to the
   admin's public endpoint (multipart):
   - "Report a problem with the app" ([data-report], data-form="report",
     POST /api/v1/reports) with an optional screenshot;
   - "Suggest a feature" ([data-idea], data-form="idea", POST
     /api/v1/ideas), text only.
   Cloudflare Turnstile is loaded only when a form is first opened, and
   each form has its own widget and action. A screenshot is scaled to at
   most 2560 px on its longest side and re-encoded (WebP, else JPEG) in a
   canvas, which drops its EXIF. Escape and a click beside the form close
   it. The links are href="#report" and href="#idea": a click opens the
   form, and so does an address ending in #report (Report an Issue in the
   apps' About window) or #idea when the page loads. */
(function () {
  if (!window.fetch || !window.FormData) { return; }

  var local = location.hostname === "localhost" || location.hostname === "127.0.0.1";
  // Site key of the Turnstile widget "Malachi report" (hostname malachimail.app) in the Cloudflare
  // dashboard, shared by both forms (they differ in the action); on localhost Cloudflare's test
  // key, which always passes.
  var SITEKEY = local ? "1x00000000000000000000AA" : "0x4AAAAAAFxBMHDxSS8hmg2T";
  var API = local ? "http://localhost:8789/api/v1/" : "https://admin.malachimail.app/api/v1/";
  var TURNSTILE = "https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit";
  var MAX_SIDE = 2560, MAX_BYTES = 5 * 1024 * 1024, MAX_TEXT = 8000;

  var locale = (document.documentElement.lang || "en").toLowerCase().indexOf("cs") === 0 ? "cs" : "en";
  var COMMON = {
    en: {
      sending: "Sending…",
      check: "The check by Cloudflare Turnstile has not finished yet. Please wait a moment.",
      image: "The screenshot could not be read. Choose a PNG, JPEG or WebP picture."
    },
    cs: {
      sending: "Odesílá se…",
      check: "Ověření službou Cloudflare Turnstile ještě neskončilo. Chvíli prosím počkejte.",
      image: "Snímek se nepodařilo přečíst. Vyberte obrázek PNG, JPEG nebo WebP."
    }
  }[locale];
  var FORMS = {
    report: {
      path: "reports",
      trigger: "[data-report]",
      hash: "#report",
      en: {
        thanks: "Thank you, the report has arrived. Its number is ",
        invalid: "The report was not accepted. Check the description and the e-mail address, and try again.",
        tooLarge: "The report is too large: the description may have at most 8000 bytes and the screenshot 5 MiB.",
        unavailable: "Reports cannot be received right now. Please try again in a while.",
        network: "The report could not be sent: the connection failed. Please try again.",
        loadFailed: "Cloudflare Turnstile could not be loaded, so the report cannot be sent. You can report the problem on GitHub instead."
      },
      cs: {
        thanks: "Děkujeme, hlášení dorazilo. Jeho číslo je ",
        invalid: "Hlášení nebylo přijato. Zkontrolujte popis a e-mailovou adresu a zkuste to znovu.",
        tooLarge: "Hlášení je příliš velké: popis smí mít nejvýš 8000 bajtů a snímek 5 MiB.",
        unavailable: "Hlášení teď nelze přijmout. Zkuste to prosím za chvíli.",
        network: "Hlášení se nepodařilo odeslat: spojení selhalo. Zkuste to prosím znovu.",
        loadFailed: "Cloudflare Turnstile se nepodařilo načíst, hlášení proto nelze odeslat. Problém můžete nahlásit na GitHubu."
      }
    },
    idea: {
      path: "ideas",
      trigger: "[data-idea]",
      hash: "#idea",
      en: {
        thanks: "Thank you, the suggestion has arrived. Its number is ",
        invalid: "The suggestion was not accepted. Check the description and the e-mail address, and try again.",
        tooLarge: "The suggestion is too long: the description may have at most 8000 bytes.",
        unavailable: "Suggestions cannot be received right now. Please try again in a while.",
        network: "The suggestion could not be sent: the connection failed. Please try again.",
        loadFailed: "Cloudflare Turnstile could not be loaded, so the suggestion cannot be sent. You can suggest the feature on GitHub instead."
      },
      cs: {
        thanks: "Děkujeme, návrh dorazil. Jeho číslo je ",
        invalid: "Návrh nebyl přijat. Zkontrolujte popis a e-mailovou adresu a zkuste to znovu.",
        tooLarge: "Návrh je příliš dlouhý: popis smí mít nejvýš 8000 bajtů.",
        unavailable: "Návrhy teď nelze přijmout. Zkuste to prosím za chvíli.",
        network: "Návrh se nepodařilo odeslat: spojení selhalo. Zkuste to prosím znovu.",
        loadFailed: "Cloudflare Turnstile se nepodařilo načíst, návrh proto nelze odeslat. Funkci můžete navrhnout na GitHubu."
      }
    }
  };

  // Turnstile's script, loaded once for every form; each form renders its widget when it is there.
  var loading = false, waiting = [];
  function withTurnstile(ready, failed) {
    if (window.turnstile) { ready(); return; }
    waiting.push({ ready: ready, failed: failed });
    if (loading) { return; }
    loading = true;
    var s = document.createElement("script");
    s.src = TURNSTILE;
    s.onload = function () { var w = waiting; waiting = []; w.forEach(function (x) { x.ready(); }); };
    s.onerror = function () { loading = false; var w = waiting; waiting = []; w.forEach(function (x) { x.failed(); }); };
    document.head.appendChild(s);
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

  var opens = {};

  function setup(dialog) {
    var name = dialog.getAttribute("data-form") || "report", cfg = FORMS[name];
    if (!cfg || typeof dialog.showModal !== "function") { return; }
    var T = cfg[locale];
    var form = dialog.querySelector("form"), status = dialog.querySelector(".rp-status");
    var send = dialog.querySelector("button[type=submit]"), close = dialog.querySelector(".rp-close");
    var file = form.elements.screenshot || null, preview = dialog.querySelector(".rp-preview");
    var box = dialog.querySelector(".rp-turnstile");
    var widget = null, shown = null, busy = false, downOnBackdrop = false, requested = false;

    function say(text, kind) {
      status.textContent = text;
      status.className = "rp-status" + (kind ? " " + kind : "");
    }

    function render() {
      if (widget !== null || !window.turnstile) { return; }
      widget = window.turnstile.render(box, {
        sitekey: SITEKEY,
        action: name,
        theme: "auto",
        language: locale,
        size: window.innerWidth < 360 ? "compact" : "flexible"
      });
    }
    function loadTurnstile() {
      if (widget !== null || requested) { return; }
      requested = true;
      withTurnstile(render, function () { requested = false; say(T.loadFailed, "error"); });
    }
    function resetTurnstile() { if (widget !== null && window.turnstile) { window.turnstile.reset(widget); } }

    function clearPreview() {
      if (!preview) { return; }
      if (shown) { URL.revokeObjectURL(shown); shown = null; }
      preview.removeAttribute("src");
      preview.hidden = true;
    }

    // Every opening starts with an empty form, unless it is still being sent.
    function open() {
      if (!busy) { form.reset(); clearPreview(); say(""); }
      if (!dialog.open) { dialog.showModal(); }
      loadTurnstile();
    }
    opens[name] = open;

    function failed(code) {
      busy = false;
      send.disabled = false;
      say(T[code] || T.invalid, "error");
      resetTurnstile();
    }

    function post(fd) {
      fetch(API + cfg.path, { method: "POST", body: fd }).then(function (r) {
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

    document.addEventListener("click", function (e) {
      var a = e.target.closest ? e.target.closest(cfg.trigger) : null;
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

    if (file && preview) {
      file.addEventListener("change", function () {
        clearPreview();
        var f = file.files && file.files[0];
        if (!f) { return; }
        shown = URL.createObjectURL(f);
        preview.src = shown;
        preview.hidden = false;
      });
    }

    form.addEventListener("submit", function (e) {
      e.preventDefault();
      if (busy) { return; }
      var fd = new FormData(form);
      var text = String(fd.get("text") || "");
      if (!text.trim()) { say(T.invalid, "error"); return; }
      if (window.TextEncoder && new TextEncoder().encode(text).length > MAX_TEXT) { say(T.tooLarge, "error"); return; }
      var token = widget !== null && window.turnstile ? window.turnstile.getResponse(widget) : "";
      if (!token) { say(requested ? COMMON.check : T.loadFailed, "error"); loadTurnstile(); return; }
      fd.set("cf-turnstile-response", token);
      fd.set("locale", locale);
      fd.set("page", location.pathname);
      fd["delete"]("screenshot");

      busy = true;
      send.disabled = true;
      say(COMMON.sending);
      var f = file && file.files && file.files[0];
      if (!f) { post(fd); return; }
      encode(f, function (b) {
        if (!b) { busy = false; send.disabled = false; say(COMMON.image, "error"); return; }
        if (b.size > MAX_BYTES) { busy = false; send.disabled = false; say(T.tooLarge, "error"); return; }
        fd.set("screenshot", b, b.type === "image/webp" ? "screenshot.webp" : "screenshot.jpg");
        post(fd);
      });
    });
  }

  Array.prototype.forEach.call(document.querySelectorAll("dialog.report"), setup);

  // #report or #idea in the address opens that form; the fragment is
  // dropped, so a reload or Back does not open it again.
  function fromHash() {
    for (var k in FORMS) {
      if (FORMS.hasOwnProperty(k) && location.hash === FORMS[k].hash && opens[k]) {
        history.replaceState(null, "", location.pathname + location.search);
        opens[k]();
        return;
      }
    }
  }
  window.addEventListener("hashchange", fromHash);
  fromHash();
})();

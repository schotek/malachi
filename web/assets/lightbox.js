/* Malachi Mail website: a screenshot opens in an overlay (the page's
   <dialog class="lightbox">) with its caption, the previous and next
   picture of the strip it came from, Escape, the arrow keys and a click
   beside the picture to close. Without this script a screenshot is a
   link to the picture itself. */
(function () {
  var dialog = document.querySelector("dialog.lightbox");
  if (!dialog || typeof dialog.showModal !== "function") { return; }
  var img = dialog.querySelector("img"), cap = dialog.querySelector("figcaption");
  var prev = dialog.querySelector(".lb-prev"), next = dialog.querySelector(".lb-next"), close = dialog.querySelector(".lb-close");
  var links = [], at = -1;

  function shown(a) { var i = a.querySelector("img"); return i && i.getClientRects().length > 0; }
  function show() {
    var a = links[at], i = a.querySelector("img");
    img.src = a.getAttribute("href");
    img.alt = i ? i.alt : "";
    var fc = a.parentNode.querySelector("figcaption");
    cap.innerHTML = fc ? fc.innerHTML : "";
    prev.hidden = at <= 0;
    next.hidden = at >= links.length - 1;
  }
  function open(a) {
    links = a.hasAttribute("data-solo") ? [a] : Array.prototype.filter.call(document.querySelectorAll("a.shot:not([data-solo])"), shown);
    at = links.indexOf(a);
    if (at < 0) { links = [a]; at = 0; }
    show();
    if (!dialog.open) { dialog.showModal(); }
    close.focus();
  }
  function step(d) { if (at + d >= 0 && at + d < links.length) { at += d; show(); } }

  document.addEventListener("click", function (e) {
    var a = e.target.closest ? e.target.closest("a.shot") : null;
    if (!a || e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) { return; }
    e.preventDefault();
    open(a);
  });
  dialog.addEventListener("click", function (e) { if (e.target === dialog) { dialog.close(); } });
  close.addEventListener("click", function () { dialog.close(); });
  prev.addEventListener("click", function () { step(-1); });
  next.addEventListener("click", function () { step(1); });
  dialog.addEventListener("keydown", function (e) {
    if (e.key === "ArrowLeft") { step(-1); e.preventDefault(); }
    else if (e.key === "ArrowRight") { step(1); e.preventDefault(); }
  });
  dialog.addEventListener("close", function () { img.removeAttribute("src"); cap.textContent = ""; });
})();

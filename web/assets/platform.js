/* Malachi Mail website: the visitor's own desktop first. The browser's
   platform picks the hero picture, the gallery's platform and the
   download card that comes first; nothing is sent anywhere, and without
   this script the pages show macOS first. */
(function () {
  var ua = navigator.userAgent || "";
  var pf = (navigator.userAgentData && navigator.userAgentData.platform) || navigator.platform || "";
  var os = "";
  if (/Android|iPhone|iPod|Windows Phone/.test(ua)) { os = ""; }
  else if (/Win/.test(pf) || /Windows/.test(ua)) { os = "windows"; }
  else if (/Mac/.test(pf) || /Macintosh/.test(ua)) { os = "macos"; }
  else if (/Linux|X11|CrOS/.test(pf + " " + ua)) { os = "linux"; }
  if (!os) { return; }
  document.documentElement.setAttribute("data-platform", os);
  var names = { macos: "macOS", linux: "Linux", windows: "Windows" };
  function ready() {
    var radio = document.getElementById("g-" + os);
    if (radio) { radio.checked = true; }
    var links = document.querySelectorAll("a[data-download]");
    for (var i = 0; i < links.length; i++) {
      links[i].setAttribute("href", links[i].getAttribute("href").split("#")[0] + "#" + os);
      links[i].appendChild(document.createTextNode(" · " + names[os]));
    }
    var card = document.getElementById(os);
    if (card && card.classList.contains("card")) {
      card.classList.add("yours");
      card.parentNode.insertBefore(card, card.parentNode.firstChild);
    }
  }
  if (document.readyState === "loading") { document.addEventListener("DOMContentLoaded", ready); } else { ready(); }
})();

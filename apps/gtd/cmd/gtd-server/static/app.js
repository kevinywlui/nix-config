// Progressive enhancement only: the Clarify form works without JS (every panel
// is a plain field set); this just reveals the sub-fields for the chosen
// decision so the page isn't a wall of inputs.
(function () {
  var radios = document.querySelectorAll('.clarify input[name="decision"]');
  var panels = document.querySelectorAll('.clarify .panel');
  if (!radios.length) return;

  function sync() {
    var chosen = "";
    radios.forEach(function (r) { if (r.checked) chosen = r.value; });
    panels.forEach(function (p) {
      p.classList.toggle("show", p.getAttribute("data-for") === chosen);
    });
  }
  radios.forEach(function (r) { r.addEventListener("change", sync); });
  sync();
})();

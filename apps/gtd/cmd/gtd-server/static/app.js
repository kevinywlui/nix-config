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

// Ctrl/Cmd-Enter submits the form you're working in — handy on the Clarify form
// (many fields) and in Capture. Uses requestSubmit so HTML5 validation (e.g. the
// required capture field) still fires. Falls back to the page's first form when
// focus isn't inside one.
(function () {
  document.addEventListener("keydown", function (e) {
    if (e.key !== "Enter" || !(e.ctrlKey || e.metaKey)) return;
    var form = (e.target.closest && e.target.closest("form")) || document.querySelector("form");
    if (!form) return;
    e.preventDefault();
    if (form.requestSubmit) form.requestSubmit(); else form.submit();
  });
})();

// Quick-set buttons beside every date field, so the common cases (defer/due =
// today / tomorrow / next week) are one tap instead of spinning a date picker.
// Progressive enhancement: without JS the native picker still works and no dead
// buttons are rendered. We deliberately don't default the fields — most actions
// want no date at all.
(function () {
  function isoOffset(days) {
    var d = new Date();
    d.setDate(d.getDate() + days); // rolls over month/year correctly
    var m = String(d.getMonth() + 1).padStart(2, "0");
    var day = String(d.getDate()).padStart(2, "0");
    return d.getFullYear() + "-" + m + "-" + day;
  }
  var quicks = [
    { label: "Today", days: 0 },
    { label: "Tomorrow", days: 1 },
    { label: "+1 week", days: 7 },
  ];
  document.querySelectorAll('input[type="date"]').forEach(function (input) {
    var row = document.createElement("div");
    row.className = "quickdates";
    quicks.forEach(function (q) {
      var btn = document.createElement("button");
      btn.type = "button";
      btn.textContent = q.label;
      btn.addEventListener("click", function () {
        input.value = isoOffset(q.days);
      });
      row.appendChild(btn);
    });
    input.insertAdjacentElement("afterend", row);
  });
})();

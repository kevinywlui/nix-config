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

// A "Today" button beside every date field, so the common case (defer/due =
// today) is one tap instead of spinning a date picker. Progressive enhancement:
// without JS the native picker still works and no dead button is rendered. We
// deliberately don't default the fields to today — most actions want no date.
(function () {
  function todayISO() {
    var d = new Date();
    var m = String(d.getMonth() + 1).padStart(2, "0");
    var day = String(d.getDate()).padStart(2, "0");
    return d.getFullYear() + "-" + m + "-" + day;
  }
  document.querySelectorAll('input[type="date"]').forEach(function (input) {
    var btn = document.createElement("button");
    btn.type = "button";
    btn.className = "today";
    btn.textContent = "Today";
    btn.addEventListener("click", function () {
      input.value = todayISO();
    });
    input.insertAdjacentElement("afterend", btn);
  });
})();

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

// Voice capture: a mic button on the Capture field that dictates straight into
// it via the browser's Web Speech API, so a task can be added hands-light on a
// phone — tap, speak, Capture. Pure progressive enhancement: injected only when
// recognition is available AND the page is a secure context (the API needs one),
// so browsers without it, plain-HTTP origins, and JS-off never see a dead control
// — the HTTPS Tailscale Serve endpoint qualifies. Heads up on privacy: on most
// browsers (including Android Chrome) recognition uploads the audio to the browser
// vendor's cloud to transcribe, so voice input is NOT on-device like the rest of
// the app — type instead for anything sensitive.
(function () {
  var SpeechRecognition = window.SpeechRecognition || window.webkitSpeechRecognition;
  if (!SpeechRecognition || !window.isSecureContext) return;
  var input = document.querySelector('.capture input[name="text"]');
  if (!input) return;

  // A polite status line (announced to screen readers, shown on screen) so the
  // listening / stopped / mic-blocked states are conveyed by more than colour.
  var status = document.createElement("p");
  status.className = "hint micstatus";
  status.setAttribute("aria-live", "polite");
  status.textContent = "Tip: tap the mic to dictate."; // set pre-insert, so not announced

  var btn = document.createElement("button");
  btn.type = "button"; // never submits the form
  btn.className = "micbtn";
  btn.title = "Dictate task";
  btn.setAttribute("aria-label", "Dictate task");
  btn.setAttribute("aria-pressed", "false"); // toggle semantics carry the state
  // The glyph is decorative (aria-label is the name); it swaps to a stop square
  // while listening, so the active state isn't signalled by colour alone.
  var icon = document.createElement("span");
  icon.setAttribute("aria-hidden", "true");
  icon.textContent = "🎙";
  btn.appendChild(icon);

  var rec = null;       // a fresh recognizer per session — reuse is flaky on Android
  var listening = false;
  var prefix = "";      // whatever was already typed, so dictation appends not replaces

  function setListening(on) {
    listening = on;
    btn.classList.toggle("listening", on);
    btn.setAttribute("aria-pressed", on ? "true" : "false");
    icon.textContent = on ? "■" : "🎙";
  }

  btn.addEventListener("click", function () {
    if (listening) {                       // tap-to-stop
      setListening(false);
      status.textContent = "";
      try { rec.stop(); } catch (e) {}
      return;
    }
    prefix = input.value.replace(/\s+$/, "");
    if (prefix) prefix += " ";

    rec = new SpeechRecognition();
    rec.lang = "en-US"; // a region tag is required; don't derive it from <html lang>
    rec.interimResults = true; // show words as they're recognized
    rec.continuous = false;    // one thought per capture — stop on a natural pause

    rec.addEventListener("result", function (e) {
      var text = "";
      for (var i = 0; i < e.results.length; i++) text += e.results[i][0].transcript;
      input.value = prefix + text;
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
    rec.addEventListener("end", function () {
      setListening(false);
      if (status.textContent === "Listening…") status.textContent = "";
      input.focus();
    });
    rec.addEventListener("error", function (e) {
      setListening(false);
      status.textContent =
        e.error === "not-allowed" ? "Microphone blocked — allow mic access to dictate." :
        e.error === "no-speech"   ? "Didn't catch that — tap the mic and try again." :
                                    "Voice input is unavailable right now.";
    });

    // Claim the toggle synchronously so a fast second tap stops rather than
    // restarting (the start event is async); roll back if start() throws.
    setListening(true);
    status.textContent = "Listening…";
    if (navigator.vibrate) navigator.vibrate(15);
    try { rec.start(); }
    catch (e) { setListening(false); status.textContent = ""; }
  });

  // Layout: [🎙] [text] [Capture] — mic on the far side from the destructive
  // Capture submit, with the status line under the row.
  input.insertAdjacentElement("beforebegin", btn);
  input.closest(".capture").insertAdjacentElement("afterend", status);
})();

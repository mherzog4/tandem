// Tandem replay player (FR19): loads an asciinema v2 cast with Tandem's
// extra "c" (composer) and "b" (board) event codes, and scrubs the
// terminal, Composer, and Board timelines together.
/* global Terminal */
(function () {
  "use strict";

  let events = []; // {t, code, data}
  let duration = 0;
  let term = null;
  let playing = false;
  let rafId = null;
  let baseWall = 0; // performance.now() anchor
  let baseT = 0; // cast time at anchor

  const drop = document.getElementById("drop");
  const main = document.getElementById("main");
  const scrub = document.getElementById("scrub");
  const clock = document.getElementById("clock");
  const playBtn = document.getElementById("play");

  function parseCast(text) {
    const lines = text.split("\n").filter((l) => l.trim());
    if (lines.length === 0) throw new Error("empty cast");
    JSON.parse(lines[0]); // header (validate)
    const evs = [];
    for (let i = 1; i < lines.length; i++) {
      const [t, code, data] = JSON.parse(lines[i]);
      evs.push({ t, code, data });
    }
    return evs;
  }

  function load(text, name) {
    events = parseCast(text);
    duration = events.length ? events[events.length - 1].t : 0;
    drop.style.display = "none";
    main.style.display = "block";
    document.getElementById("fname").textContent = name || "";
    if (term) term.dispose();
    term = new Terminal({
      scrollback: 10000,
      fontFamily: "ui-monospace, Menlo, monospace",
      fontSize: 13,
      disableStdin: true,
      theme: { background: "#0d1117" },
    });
    term.open(document.getElementById("term"));
    seek(0);
  }

  // Render terminal + panels up to cast time `upto` from scratch (a
  // clean rebuild avoids escape-sequence corruption when scrubbing back).
  function renderTo(upto) {
    term.reset();
    let composer = "";
    let board = [];
    let out = "";
    for (const ev of events) {
      if (ev.t > upto) break;
      if (ev.code === "o") out += ev.data;
      else if (ev.code === "c") composer = ev.data;
      else if (ev.code === "b") { try { board = JSON.parse(ev.data); } catch { /* keep */ } }
    }
    term.write(out);
    document.getElementById("composer").textContent = composer;
    const cards = document.getElementById("cards");
    cards.innerHTML = "";
    for (const c of board) {
      const el = document.createElement("div");
      el.className = "pcard";
      el.dataset.type = c.type;
      el.textContent = `${c.text} — ${c.state || ""}`;
      cards.appendChild(el);
    }
    clock.textContent = `${upto.toFixed(1)}s / ${duration.toFixed(1)}s`;
    scrub.value = duration ? Math.round((upto / duration) * 1000) : 0;
  }

  function seek(t) {
    pause();
    renderTo(t);
  }

  function tick() {
    if (!playing) return;
    const now = (performance.now() - baseWall) / 1000 + baseT;
    if (now >= duration) {
      renderTo(duration);
      pause();
      return;
    }
    renderTo(now);
    rafId = requestAnimationFrame(tick);
  }

  function play() {
    if (playing || duration === 0) return;
    playing = true;
    playBtn.textContent = "⏸";
    baseWall = performance.now();
    baseT = (scrub.value / 1000) * duration;
    if (baseT >= duration) baseT = 0;
    rafId = requestAnimationFrame(tick);
  }
  function pause() {
    playing = false;
    playBtn.textContent = "▶";
    if (rafId) cancelAnimationFrame(rafId);
  }

  playBtn.addEventListener("click", () => (playing ? pause() : play()));
  scrub.addEventListener("input", () => seek((scrub.value / 1000) * duration));

  document.getElementById("file").addEventListener("change", (e) => {
    const f = e.target.files[0];
    if (f) f.text().then((t) => load(t, f.name));
  });
  ["dragover", "dragenter"].forEach((ev) =>
    drop.addEventListener(ev, (e) => { e.preventDefault(); drop.classList.add("over"); }));
  ["dragleave", "drop"].forEach((ev) =>
    drop.addEventListener(ev, (e) => { e.preventDefault(); drop.classList.remove("over"); }));
  drop.addEventListener("drop", (e) => {
    const f = e.dataTransfer.files[0];
    if (f) f.text().then((t) => load(t, f.name));
  });
})();

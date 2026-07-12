// Tandem guest client: read-only terminal view (issue #5).
//
// Frames from the host are AES-256-GCM sealed; the session key lives in
// the URL fragment and never reaches the relay. Plaintext frames carry a
// 1-byte envelope: 0x00 = raw PTY bytes, 0x01 = JSON control (resize).
//
// This client has no code path that writes to the PTY: it never sends
// binary frames. That is the core Tandem invariant (FR6/FR21); the
// Composer (issue #10+) adds CRDT ops, still never stdin.

/* global Terminal, TandemLib */
(function () {
  "use strict";

  // Pure helpers and frame crypto live in lib.js (loaded first) so they can
  // be unit-tested outside the browser. See web/lib.test.mjs.
  const { FRAME_PTY, FRAME_CTRL, FRAME_REPLAY, importKey, sealFrame, openFrame, rebase, diffOp } = TandemLib;

  function sessionInfo() {
    const m = location.pathname.match(/\/s\/([^/]+)/);
    const frag = new URLSearchParams(location.hash.slice(1));
    // hostToken is a capability: present only on the link the daemon
    // prints for the host. It gates confirm/alias actions.
    return { id: m && m[1], keyB64: frag.get("k"), hostToken: frag.get("h") };
  }

  // Minimal markdown renderer for reader mode: escapes HTML, then handles
  // fenced code, ###-headings, -/* bullets, `code`, and **bold**. Not a full
  // parser — just enough to make agent output readable for a nontechnical guest.
  function mdEscape(s) { return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;"); }
  function mdInline(s) {
    return mdEscape(s).replace(/`([^`]+)`/g, "<code>$1</code>").replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");
  }
  function mdToHtml(text) {
    let html = "", inCode = false, inList = false;
    const close = () => { if (inList) { html += "</ul>"; inList = false; } };
    for (const raw of text.split("\n")) {
      const line = raw.replace(/\s+$/, "");
      if (line.trim().startsWith("```")) {
        if (inCode) { html += "</code></pre>"; inCode = false; } else { close(); html += "<pre><code>"; inCode = true; }
        continue;
      }
      if (inCode) { html += mdEscape(raw) + "\n"; continue; }
      const h = line.match(/^(#{1,3})\s+(.*)/), li = line.match(/^\s*[-*]\s+(.*)/);
      if (h) { close(); html += `<h${h[1].length}>${mdInline(h[2])}</h${h[1].length}>`; }
      else if (li) { if (!inList) { html += "<ul>"; inList = true; } html += `<li>${mdInline(li[1])}</li>`; }
      else if (line.trim() === "") { close(); }
      else { close(); html += `<p>${mdInline(line)}</p>`; }
    }
    if (inCode) html += "</code></pre>";
    close();
    return html;
  }

  const joinEl = document.getElementById("join");
  const appEl = document.getElementById("app");
  const statusEl = document.getElementById("status");
  const whoEl = document.getElementById("who");
  const latEl = document.getElementById("lat");
  const errEl = document.getElementById("joinerr");

  document.getElementById("go").addEventListener("click", start);
  document.getElementById("consentok").addEventListener("click", () => {
    window.__recConsented = true;
    document.getElementById("consent").style.display = "none";
  });
  document.getElementById("name").addEventListener("keydown", (e) => {
    if (e.key === "Enter") start();
  });

  async function start() {
    const name = document.getElementById("name").value.trim() || "guest";
    const { id, keyB64, hostToken } = sessionInfo();
    if (!id || !keyB64) {
      errEl.textContent = "Bad link: missing session ID or key fragment.";
      return;
    }
    let key;
    try {
      key = await importKey(keyB64);
    } catch {
      errEl.textContent = "Bad link: session key is invalid.";
      return;
    }

    joinEl.style.display = "none";
    appEl.style.display = "block";

    // First-join onboarding: explain the compose-only model once, then
    // remember the dismissal so returning guests skip it.
    const onboardEl = document.getElementById("onboard");
    if (!localStorage.getItem("tandem_onboarded")) onboardEl.style.display = "flex";
    document.getElementById("onboardok").addEventListener("click", () => {
      onboardEl.style.display = "none";
      localStorage.setItem("tandem_onboarded", "1");
    });

    // Turn-state chip. Derived entirely client-side from the authoritative
    // composer doc: empty = the guest's turn to compose, non-empty = the
    // engineer is reviewing it. The host's "submitted" ctrl flips it to
    // "ran" for a beat before recomputing.
    const turnEl = document.getElementById("turnchip");
    let ranTimer = null;
    function setTurn(state) {
      turnEl.className = "badge chip-" + state;
      turnEl.textContent = state === "yours" ? "your turn" : state === "reviewing" ? "engineer reviewing" : "engineer ran it";
    }
    function refreshTurn() {
      if (ranTimer) return;
      setTurn(comp.chars.length > 0 ? "reviewing" : "yours");
    }
    function flashRan() {
      setTurn("ran");
      if (ranTimer) clearTimeout(ranTimer);
      ranTimer = setTimeout(() => { ranTimer = null; refreshTurn(); }, 3000);
    }

    const term = new Terminal({
      convertEol: false,
      scrollback: 10000,
      fontFamily: "ui-monospace, SFMono-Regular, Menlo, monospace",
      fontSize: 13,
      disableStdin: true, // read-only view; guest input arrives with the Composer
      theme: { background: "#0d1117" },
    });
    term.open(document.getElementById("term"));

    // Reader mode: render xterm's already-resolved screen buffer as clean,
    // larger markdown so a nontechnical guest reads prose, not raw ANSI.
    // Reads the buffer (not the PTY stream) so TUI repaints are already
    // settled. ponytail: last 1200 buffer lines, refreshed on render.
    const readerEl = document.getElementById("reader");
    const readerMd = readerEl.querySelector(".md");
    const readerBtn = document.getElementById("readertoggle");
    let readerOn = false, readerTimer = null;
    readerBtn.addEventListener("click", () => {
      readerOn = !readerOn;
      readerEl.classList.toggle("on", readerOn);
      readerBtn.classList.toggle("active", readerOn);
      readerBtn.textContent = readerOn ? "🖥 terminal" : "📖 reader";
      if (readerOn) renderReader();
    });
    term.onRender(() => { if (readerOn && !readerTimer) readerTimer = setTimeout(() => { readerTimer = null; renderReader(); }, 300); });
    function renderReader() {
      const buf = term.buffer.active;
      const start = Math.max(0, buf.length - 1200);
      const lines = [];
      for (let i = start; i < buf.length; i++) lines.push(buf.getLine(i).translateToString(true));
      while (lines.length && lines[0].trim() === "") lines.shift();
      while (lines.length && lines[lines.length - 1].trim() === "") lines.pop();
      readerMd.innerHTML = mdToHtml(lines.join("\n"));
      readerEl.scrollTop = readerEl.scrollHeight;
    }

    const proto = location.protocol === "https:" ? "wss" : "ws";
    const email = document.getElementById("email").value.trim();
    const qs = `name=${encodeURIComponent(name)}` + (email ? `&email=${encodeURIComponent(email)}` : "");
    const ws = new WebSocket(`${proto}://${location.host}/ws/join/${id}?${qs}`);
    ws.binaryType = "arraybuffer";

    // Allowlisted sessions (FR22) reject the upgrade with a 403; the
    // socket errors before opening. Reveal the email field and retry.
    let opened = false;
    ws.addEventListener("open", () => { opened = true; });
    ws.addEventListener("close", () => {
      if (opened) return;
      appEl.style.display = "none";
      joinEl.style.display = "flex";
      document.getElementById("email").hidden = false;
      errEl.textContent = "This session has a guest list — enter the email the host invited.";
    });

    // Decrypt strictly in arrival order: a queue, not concurrent awaits.
    let chain = Promise.resolve();

    ws.onmessage = (ev) => {
      if (typeof ev.data === "string") {
        try {
          const msg = JSON.parse(ev.data);
          if (msg.type === "presence") {
            statusEl.textContent = `${msg.name} ${msg.event === "join" ? "joined" : "left"}`;
            bumpRoster(msg.name, msg.event === "join" ? 1 : -1);
          } else if (msg.type === "roster" && Array.isArray(msg.names)) {
            setRoster(msg.names);
          }
        } catch { /* ignore malformed relay text */ }
        return;
      }
      chain = chain.then(async () => {
        let plain;
        try {
          plain = await openFrame(key, ev.data);
        } catch {
          return; // tampered/foreign frame: drop silently
        }
        const kind = plain[0];
        const body = plain.slice(1);
        if (kind === FRAME_PTY) {
          term.write(body);
        } else if (kind === FRAME_REPLAY) {
          term.reset();
          term.write(body);
        } else if (kind === FRAME_CTRL) {
          try {
            const ctrl = JSON.parse(new TextDecoder().decode(body));
            if (ctrl.type === "resize" && ctrl.cols > 0 && ctrl.rows > 0) {
              term.resize(ctrl.cols, ctrl.rows);
            } else if (ctrl.type === "pong" && typeof ctrl.t === "number") {
              recordRTT(ctrl.t);
            } else if (ctrl.type === "recording") {
              // FR24: guests must acknowledge before continuing to view.
              if (ctrl.on && !window.__recConsented) {
                document.getElementById("consent").style.display = "flex";
              }
              statusEl.textContent = ctrl.on ? "🔴 recorded session" : "live";
            } else if (ctrl.type === "shutter") {
              statusEl.textContent = ctrl.on ? "⏸ host paused sharing" : "live";
              // Full overlay: guests must never sit on a frozen frame of
              // possibly sensitive content (FR4).
              document.getElementById("shutter").style.display = ctrl.on ? "flex" : "none";
            } else {
              handleComposerCtrl(ctrl);
            }
          } catch { /* ignore malformed control */ }
        }
      });
    };

    // Latency probe (FR3): sealed ping through the relay, echoed by the
    // host daemon. One-way ≈ RTT/2; avoids host/guest clock skew.
    const rtts = [];
    function recordRTT(sentAt) {
      rtts.push(performance.now() - sentAt);
      if (rtts.length > 60) rtts.shift();
      const sorted = [...rtts].sort((a, b) => a - b);
      const q = (p) => sorted[Math.min(sorted.length - 1, Math.floor(p * sorted.length))];
      latEl.textContent = `echo p50 ${(q(0.5) / 2).toFixed(0)}ms · p95 ${(q(0.95) / 2).toFixed(0)}ms`;
    }
    setInterval(async () => {
      if (ws.readyState !== WebSocket.OPEN) return;
      ws.send(await sealFrame(key, FRAME_CTRL, { type: "ping", t: performance.now() }));
    }, 5000);

    // ---- Prompt Composer (FR6/FR7) -------------------------------
    // Replica of the host-authoritative document. Char/author arrays
    // are code-point based to match the host's rune indexing.
    const comp = { rev: 0, chars: [], authors: [], cursors: {} };
    const cinput = document.getElementById("cinput");
    const cmirror = document.getElementById("cmirror");
    const cauthors = document.getElementById("cauthors");
    let shadow = ""; // optimistic local text (textarea contents)

    const hue = (a) => { let h = 0; for (const c of a) h = (h * 31 + c.codePointAt(0)) % 360; return h; };
    const colorOf = (a) => `hsl(${hue(a)} 70% 65%)`;

    // Presence roster: names of connected guests, kept live from the
    // relay's roster snapshot (on join) plus join/leave deltas. Counts
    // handle duplicate display names. The engineer is always shown since
    // a session without a host is already dead.
    const roster = new Map();
    const rosterEl = document.getElementById("roster");
    function renderRoster() {
      const chips = [["🧑‍💻 engineer", "#3fb950"]];
      for (const n of roster.keys()) chips.push([n, colorOf(n)]);
      rosterEl.innerHTML = chips
        .map(([label, color]) => `<span class="rchip"><span class="dot" style="background:${color}"></span>${esc(label)}</span>`)
        .join("");
    }
    function bumpRoster(n, delta) {
      const c = (roster.get(n) || 0) + delta;
      if (c <= 0) roster.delete(n); else roster.set(n, c);
      renderRoster();
    }
    function setRoster(names) {
      roster.clear();
      for (const n of names) roster.set(n, (roster.get(n) || 0) + 1);
      renderRoster();
    }
    renderRoster();

    async function sendCtrl(obj) {
      if (ws.readyState === WebSocket.OPEN) ws.send(await sealFrame(key, FRAME_CTRL, obj));
    }

    function applyOp(op) {
      const ins = Array.from(op.ins || "");
      comp.chars.splice(op.pos, op.del, ...ins);
      comp.authors.splice(op.pos, op.del, ...ins.map(() => op.author));
      comp.rev = op.rev;
      // Shift every remote caret the same way the text moved.
      for (const a in comp.cursors) {
        let p = comp.cursors[a];
        if (op.del > 0 && p > op.pos) p = Math.max(op.pos, p - op.del);
        if (ins.length && p >= op.pos) p += ins.length;
        comp.cursors[a] = p;
      }
    }

    // Apply a remote op into the local textarea without disturbing
    // unacked local edits: shift the remote position past our pending
    // inserts, splice, and move the caret if it sat after the change.
    function syncTextarea(op) {
      let rpos = op.pos;
      for (const p of [inflight, ...sendQueue]) {
        if (p && p.pos <= rpos) rpos += Array.from(p.ins || "").length - p.del;
      }
      rpos = Math.max(0, rpos);
      const chars = Array.from(shadow);
      rpos = Math.min(rpos, chars.length);
      const del = Math.min(op.del, chars.length - rpos);
      const ins = Array.from(op.ins || "");

      const caretUtf16 = cinput.selectionStart;
      let caret = Array.from(cinput.value.slice(0, caretUtf16)).length;
      chars.splice(rpos, del, ...ins);
      if (caret > rpos) caret = Math.max(rpos, caret - del) + ins.length;

      shadow = chars.join("");
      cinput.value = shadow;
      const utf16 = chars.slice(0, caret).join("").length;
      cinput.setSelectionRange(utf16, utf16);
      renderMirror();
    }

    // Escapes text AND attribute contexts: author names and buffer
    // content are guest-controlled (untrusted).
    function esc(s) {
      return String(s).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/"/g, "&quot;").replace(/'/g, "&#39;");
    }

    function renderMirror() {
      let html = "";
      const carets = {};
      for (const [a, p] of Object.entries(comp.cursors)) {
        if (a !== name) (carets[p] = carets[p] || []).push(a);
      }
      for (let i = 0; i <= comp.chars.length; i++) {
        for (const a of carets[i] || []) {
          html += `<span class="caret" style="border-color:${colorOf(a)}" title="${esc(a)}"></span>`;
        }
        if (i < comp.chars.length) {
          html += `<span style="color:${colorOf(comp.authors[i])}">${esc(comp.chars[i])}</span>`;
        }
      }
      cmirror.innerHTML = html;
      const seen = [...new Set(comp.authors)];
      cauthors.innerHTML = seen.map((a) => `<span style="color:${colorOf(a)}">●</span> ${esc(a)}`).join("  ");
    }

    // Classic OT client (Jupiter model): at most one op in flight, the
    // rest queue locally. Own echoes advance the revision; remote ops
    // transform every pending op. Without this, rapid typing sends
    // stale baseRevs and the host transforms our ops against our OWN
    // earlier ops, interleaving text.
    let inflight = null;
    const sendQueue = [];

    function pump() {
      if (inflight || sendQueue.length === 0) return;
      inflight = sendQueue.shift();
      inflight.baseRev = comp.rev;
      sendCtrl({ type: "op", op: inflight });
    }

    // Shift a pending local op to account for a remote op that the
    // server ordered before it.
    cinput.addEventListener("input", () => {
      const now = cinput.value;
      const op = diffOp(shadow, now, name);
      shadow = now;
      if (op.del > 0 || op.ins) {
        sendQueue.push(op);
        pump();
      }
    });

    document.getElementById("cundo").addEventListener("click", () => sendCtrl({ type: "undo", author: name }));

    // ---- Dictation (FR9): push-to-talk via the browser's native
    // SpeechRecognition. No audio ever leaves through the relay and no
    // API keys are needed; the trade-off (speech is processed by the
    // browser vendor's recognizer) is documented in docs/compat.md. A
    // hosted Whisper backend can slot in behind insertDictation later.
    function insertDictation(text) {
      if (!text) return;
      cinput.focus();
      // execCommand fires the input event, so the normal op path runs.
      if (!document.execCommand("insertText", false, text)) {
        const s = cinput.selectionStart, e = cinput.selectionEnd;
        cinput.value = cinput.value.slice(0, s) + text + cinput.value.slice(e);
        cinput.setSelectionRange(s + text.length, s + text.length);
        cinput.dispatchEvent(new Event("input"));
      }
    }

    const micBtn = document.getElementById("cmic");
    const SR = window.SpeechRecognition || window.webkitSpeechRecognition;
    if (!SR) {
      micBtn.hidden = true; // Firefox: no native recognition
    } else {
      let rec = null;
      const start = (ev) => {
        ev.preventDefault();
        if (rec) return;
        rec = new SR();
        rec.continuous = true;
        rec.interimResults = true;
        rec.onresult = (e) => {
          let final = "";
          for (let i = e.resultIndex; i < e.results.length; i++) {
            if (e.results[i].isFinal) final += e.results[i][0].transcript;
            else statusEl.textContent = `🎤 ${e.results[i][0].transcript}`;
          }
          if (final) insertDictation(final.trim() + " ");
        };
        rec.onerror = (e) => { statusEl.textContent = `mic error: ${e.error}`; };
        rec.onend = () => { statusEl.textContent = "live"; };
        rec.start();
        micBtn.classList.add("rec");
        statusEl.textContent = "🎤 listening…";
      };
      const stop = () => {
        if (!rec) return;
        rec.stop();
        rec = null;
        micBtn.classList.remove("rec");
      };
      micBtn.addEventListener("mousedown", start);
      micBtn.addEventListener("touchstart", start);
      micBtn.addEventListener("mouseup", stop);
      micBtn.addEventListener("mouseleave", stop);
      micBtn.addEventListener("touchend", stop);
    }

    let cursorTimer = null;
    document.addEventListener("selectionchange", () => {
      if (document.activeElement !== cinput || cursorTimer) return;
      cursorTimer = setTimeout(() => {
        cursorTimer = null;
        const pos = Array.from(cinput.value.slice(0, cinput.selectionStart)).length;
        sendCtrl({ type: "cursor", author: name, pos });
      }, 200);
    });

    // ---- Pointing (FR10) and raise-hand (FR11) --------------------
    const pointerLayer = document.getElementById("pointer-layer");
    const termwrap = document.getElementById("termwrap");

    function showPing(author, fx, fy) {
      const el = document.createElement("div");
      el.className = "ping";
      el.style.borderColor = colorOf(author);
      el.style.left = `${fx * 100}%`;
      el.style.top = `${fy * 100}%`;
      const label = document.createElement("small");
      label.textContent = author;
      label.style.color = colorOf(author);
      el.appendChild(label);
      pointerLayer.appendChild(el);
      setTimeout(() => el.remove(), 2600);
    }

    // Alt-click (or double-click) the terminal to point at it.
    termwrap.addEventListener("click", (ev) => {
      if (!ev.altKey) return;
      const r = termwrap.getBoundingClientRect();
      const fx = (ev.clientX - r.left) / r.width;
      const fy = (ev.clientY - r.top) / r.height;
      showPing(name, fx, fy); // local echo
      sendCtrl({ type: "highlight", author: name, x: fx, y: fy });
    });

    const reactionsEl = document.getElementById("reactions");
    function showReaction(author, emoji) {
      const el = document.createElement("div");
      el.className = "react-item";
      el.textContent = `${emoji} ${author}`;
      reactionsEl.appendChild(el);
      setTimeout(() => el.remove(), 4100);
    }
    document.querySelectorAll("#reactbar button").forEach((btn) => {
      btn.addEventListener("click", () => {
        showReaction(name, btn.dataset.emoji); // local echo
        sendCtrl({ type: "react", author: name, emoji: btn.dataset.emoji });
      });
    });

    // Raise hand on text (FR11): select agent output, quote it into the
    // Composer as a correction the pair can edit and send. Uses the
    // normal op path — no new privileged message type.
    const quoteBtn = document.getElementById("quotebtn");
    term.onSelectionChange(() => {
      quoteBtn.hidden = term.getSelection().trim() === "";
    });
    quoteBtn.addEventListener("click", () => {
      const sel = term.getSelection().trim();
      if (!sel) return;
      const quoted = `> "${sel}" — `;
      cinput.focus();
      const end = cinput.value.length;
      cinput.setSelectionRange(end, end);
      if (!document.execCommand("insertText", false, (end ? "\n" : "") + quoted)) {
        cinput.value += (end ? "\n" : "") + quoted;
        cinput.dispatchEvent(new Event("input"));
      }
      quoteBtn.hidden = true;
    });
    // ---------------------------------------------------------------

    // ---- Domain Board (FR12 manual cards + FR16 ordering) ---------
    const boardEl = document.getElementById("board");
    document.getElementById("boardtoggle").addEventListener("click", () => {
      boardEl.hidden = !boardEl.hidden;
    });

    document.getElementById("cardform").addEventListener("submit", (ev) => {
      ev.preventDefault();
      const text = document.getElementById("cardtext").value.trim();
      if (!text) return;
      sendCtrl({
        type: "board-add",
        cardType: document.getElementById("cardtype").value,
        text,
        author: name,
      });
      document.getElementById("cardtext").value = "";
    });

    let dragId = null;

    function renderBoard(cards) {
      document.querySelectorAll(".lane").forEach((lane) => {
        const laneType = lane.dataset.type;
        const holder = lane.querySelector(".cards");
        holder.innerHTML = "";
        cards.filter((c) => c.type === laneType).forEach((c, idx) => {
          const el = document.createElement("div");
          el.className = `card ${c.state}`;
          el.dataset.type = c.type;
          el.dataset.id = c.id;
          el.dataset.index = idx;
          el.draggable = laneType === "event";

          const del = document.createElement("span");
          del.className = "del";
          del.textContent = "×";
          del.title = "remove card";
          del.addEventListener("click", () => sendCtrl({ type: "board-del", id: c.id, author: name }));

          const text = document.createElement("span");
          text.textContent = c.text;
          const meta = document.createElement("small");
          meta.textContent = `${c.author} · ${c.state}${c.codeName ? ` · code: ${c.codeName}` : ""}`;
          // Extractor provenance (FR12): hover shows the transcript
          // quote that produced the proposal.
          if (c.provenance) meta.title = `evidence: "${c.provenance}"`;

          el.append(del, text, meta);

          // Host-only controls (FR13): confirm proposed cards, map a
          // code-name alias (PRD risk 5). Gated by the capability token
          // server-side; guests simply don't render these.
          if (hostToken) {
            const row = document.createElement("small");
            if (c.state === "proposed") {
              const ok = document.createElement("button");
              ok.textContent = "✓ confirm";
              ok.className = "cardbtn";
              ok.addEventListener("click", () =>
                sendCtrl({ type: "board-confirm", id: c.id, author: name, token: hostToken }));
              row.appendChild(ok);
            }
            const alias = document.createElement("button");
            alias.textContent = c.codeName ? "edit code name" : "+ code name";
            alias.className = "cardbtn";
            alias.addEventListener("click", () => {
              const v = prompt(`Code name for "${c.text}"`, c.codeName || "");
              if (v !== null) sendCtrl({ type: "board-alias", id: c.id, text: v.trim(), author: name, token: hostToken });
            });
            row.appendChild(alias);
            el.appendChild(row);
          }

          // Double-click edits in place; stakeholder wording wins by
          // default so anyone can rewrite (FR13's editing half).
          el.addEventListener("dblclick", () => {
            const input = document.createElement("input");
            input.value = c.text;
            input.style.width = "95%";
            el.replaceChildren(input);
            input.focus();
            const commit = () => {
              const t = input.value.trim();
              if (t && t !== c.text) sendCtrl({ type: "board-edit", id: c.id, text: t, author: name });
              else renderBoard(comp.board || []);
            };
            input.addEventListener("blur", commit);
            input.addEventListener("keydown", (e) => { if (e.key === "Enter") input.blur(); });
          });

          // Drag ordering for events (FR16).
          if (el.draggable) {
            el.addEventListener("dragstart", () => { dragId = c.id; });
            el.addEventListener("dragover", (e) => { e.preventDefault(); el.classList.add("dragover"); });
            el.addEventListener("dragleave", () => el.classList.remove("dragover"));
            el.addEventListener("drop", (e) => {
              e.preventDefault();
              el.classList.remove("dragover");
              if (dragId && dragId !== c.id) {
                sendCtrl({ type: "board-move", id: dragId, toIndex: idx, author: name });
              }
              dragId = null;
            });
          }
          holder.appendChild(el);
        });
      });
    }
    // ---------------------------------------------------------------

    function handleComposerCtrl(ctrl) {
      if (ctrl.type === "composer-op" && ctrl.op) {
        applyOp(ctrl.op);
        if (ctrl.op.author === name && inflight) {
          // Our echo: revision advances, next queued op may fly.
          inflight = null;
          renderMirror();
          pump();
        } else {
          // Remote op: rebase everything we haven't been acked for.
          if (inflight) rebase(inflight, ctrl.op);
          sendQueue.forEach((p) => rebase(p, ctrl.op));
          syncTextarea(ctrl.op);
        }
        refreshTurn();
      } else if (ctrl.type === "composer-snapshot" && ctrl.snapshot) {
        comp.chars = Array.from(ctrl.snapshot.text || "");
        comp.authors = [];
        for (const sp of ctrl.snapshot.spans || []) {
          for (let i = 0; i < sp.len; i++) comp.authors.push(sp.author);
        }
        comp.rev = ctrl.snapshot.rev;
        shadow = comp.chars.join("");
        cinput.value = shadow;
        renderMirror();
        refreshTurn();
      } else if (ctrl.type === "recap" && typeof ctrl.markdown === "string") {
        // Session ended: show the recap over everything. Rendered as
        // plain text (no markdown lib) to keep the guest bundle
        // dependency-free and avoid any injection surface.
        const overlay = document.getElementById("recap");
        overlay.querySelector("pre").textContent = ctrl.markdown;
        overlay.style.display = "flex";
        statusEl.textContent = "session ended";
      } else if (ctrl.type === "submitted") {
        statusEl.textContent = "✓ the engineer ran it";
        flashRan();
      } else if (ctrl.type === "highlight" && ctrl.author !== name) {
        showPing(ctrl.author, ctrl.x, ctrl.y);
      } else if (ctrl.type === "react" && ctrl.author !== name) {
        showReaction(ctrl.author, ctrl.emoji);
      } else if (ctrl.type === "drift" && Array.isArray(ctrl.conflicts)) {
        // Vocabulary drift (FR17): surface without interrupting; each
        // flag can be dismissed or turned into a quoted correction.
        const bar = document.getElementById("driftbar");
        for (const cf of ctrl.conflicts.slice(0, 3)) {
          const el = document.createElement("div");
          el.className = "drift";
          const msg = document.createElement("div");
          const b = document.createElement("b");
          b.textContent = "⚠ vocabulary drift: ";
          msg.appendChild(b);
          msg.appendChild(document.createTextNode(`"${cf.usage}" conflicts with the confirmed card "${cf.cardText}"`));
          const actions = document.createElement("div");
          actions.className = "actions";
          const correct = document.createElement("button");
          correct.textContent = "✏ correct in composer";
          correct.addEventListener("click", () => {
            cinput.focus();
            const end = cinput.value.length;
            cinput.setSelectionRange(end, end);
            const text = `${end ? "\n" : ""}> "${cf.quote || cf.usage}" — this conflicts with "${cf.cardText}": `;
            if (!document.execCommand("insertText", false, text)) {
              cinput.value += text;
              cinput.dispatchEvent(new Event("input"));
            }
            el.remove();
          });
          const dismiss = document.createElement("button");
          dismiss.textContent = "dismiss";
          dismiss.addEventListener("click", () => el.remove());
          actions.append(correct, dismiss);
          el.append(msg, actions);
          bar.appendChild(el);
        }
      } else if (ctrl.type === "board-state") {
        comp.board = ctrl.cards || [];
        renderBoard(comp.board);
        if (boardEl.hidden && comp.board.length) boardEl.hidden = false;
      } else if (ctrl.type === "cursor" && ctrl.author) {
        comp.cursors[ctrl.author] = ctrl.pos;
        renderMirror();
      }
    }
    // ---------------------------------------------------------------

    ws.onopen = () => { statusEl.textContent = "live"; whoEl.textContent = `you: ${name}`; };
    ws.onclose = (ev) => {
      statusEl.textContent = ev.reason === "session full" ? "session full (max 3)" : "disconnected";
    };
  }
})();

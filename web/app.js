// Tandem guest client: read-only terminal view (issue #5).
//
// Frames from the host are AES-256-GCM sealed; the session key lives in
// the URL fragment and never reaches the relay. Plaintext frames carry a
// 1-byte envelope: 0x00 = raw PTY bytes, 0x01 = JSON control (resize).
//
// This client has no code path that writes to the PTY: it never sends
// binary frames. That is the core Tandem invariant (FR6/FR21); the
// Composer (issue #10+) adds CRDT ops, still never stdin.

/* global Terminal */
(function () {
  "use strict";

  const FRAME_PTY = 0x00;
  const FRAME_CTRL = 0x01;
  const FRAME_REPLAY = 0x02; // reset terminal, then render scrollback snapshot

  function b64urlDecode(s) {
    s = s.replace(/-/g, "+").replace(/_/g, "/");
    while (s.length % 4) s += "=";
    const bin = atob(s);
    const out = new Uint8Array(bin.length);
    for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
    return out;
  }

  function sessionInfo() {
    const m = location.pathname.match(/\/s\/([^/]+)/);
    const frag = new URLSearchParams(location.hash.slice(1));
    // hostToken is a capability: present only on the link the daemon
    // prints for the host. It gates confirm/alias actions.
    return { id: m && m[1], keyB64: frag.get("k"), hostToken: frag.get("h") };
  }

  async function importKey(keyB64) {
    return crypto.subtle.importKey("raw", b64urlDecode(keyB64), "AES-GCM", false, ["decrypt", "encrypt"]);
  }

  async function sealFrame(key, kind, obj) {
    const body = new TextEncoder().encode(JSON.stringify(obj));
    const plain = new Uint8Array(1 + body.length);
    plain[0] = kind;
    plain.set(body, 1);
    const iv = crypto.getRandomValues(new Uint8Array(12));
    const ct = new Uint8Array(await crypto.subtle.encrypt({ name: "AES-GCM", iv }, key, plain));
    const frame = new Uint8Array(12 + ct.length);
    frame.set(iv, 0);
    frame.set(ct, 12);
    return frame;
  }

  async function openFrame(key, buf) {
    const data = new Uint8Array(buf);
    if (data.length < 13) throw new Error("frame too short");
    const iv = data.slice(0, 12);
    const ct = data.slice(12);
    const plain = await crypto.subtle.decrypt({ name: "AES-GCM", iv }, key, ct);
    return new Uint8Array(plain);
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

    const term = new Terminal({
      convertEol: false,
      scrollback: 10000,
      fontFamily: "ui-monospace, SFMono-Regular, Menlo, monospace",
      fontSize: 13,
      disableStdin: true, // read-only view; guest input arrives with the Composer
      theme: { background: "#0d1117" },
    });
    term.open(document.getElementById("term"));

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
    function rebase(pending, remote) {
      const insLen = Array.from(remote.ins || "").length;
      if (remote.del > 0 && pending.pos > remote.pos) {
        pending.pos = Math.max(remote.pos, pending.pos - remote.del);
      }
      if (insLen && remote.pos <= pending.pos) pending.pos += insLen;
      return pending;
    }

    cinput.addEventListener("input", () => {
      const now = cinput.value;
      const a = Array.from(shadow), b = Array.from(now);
      let s = 0;
      while (s < a.length && s < b.length && a[s] === b[s]) s++;
      let e = 0;
      while (e < a.length - s && e < b.length - s && a[a.length - 1 - e] === b[b.length - 1 - e]) e++;
      const op = { author: name, baseRev: 0, pos: s, del: a.length - s - e, ins: b.slice(s, b.length - e).join("") };
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
      } else if (ctrl.type === "submitted") {
        statusEl.textContent = "prompt sent ✓";
      } else if (ctrl.type === "highlight" && ctrl.author !== name) {
        showPing(ctrl.author, ctrl.x, ctrl.y);
      } else if (ctrl.type === "react" && ctrl.author !== name) {
        showReaction(ctrl.author, ctrl.emoji);
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

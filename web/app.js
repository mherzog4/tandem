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
    return { id: m && m[1], keyB64: frag.get("k") };
  }

  async function importKey(keyB64) {
    return crypto.subtle.importKey("raw", b64urlDecode(keyB64), "AES-GCM", false, ["decrypt"]);
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
  const errEl = document.getElementById("joinerr");

  document.getElementById("go").addEventListener("click", start);
  document.getElementById("name").addEventListener("keydown", (e) => {
    if (e.key === "Enter") start();
  });

  async function start() {
    const name = document.getElementById("name").value.trim() || "guest";
    const { id, keyB64 } = sessionInfo();
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
    const ws = new WebSocket(`${proto}://${location.host}/ws/join/${id}?name=${encodeURIComponent(name)}`);
    ws.binaryType = "arraybuffer";

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
            } else if (ctrl.type === "shutter") {
              statusEl.textContent = ctrl.on ? "⏸ host paused sharing" : "live";
            }
          } catch { /* ignore malformed control */ }
        }
      });
    };

    ws.onopen = () => { statusEl.textContent = "live"; whoEl.textContent = `you: ${name}`; };
    ws.onclose = (ev) => {
      statusEl.textContent = ev.reason === "session full" ? "session full (max 3)" : "disconnected";
    };
  }
})();

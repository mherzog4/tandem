// Framework-free helpers shared by the guest client (app.js) and its unit
// tests (lib.test.mjs). No DOM or app state here — only pure functions and
// the AES-256-GCM frame crypto — so they run identically in the browser
// and under node:test. Exposed on globalThis for both loaders.
(function (root) {
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

  // rebase shifts a pending local op past a remote op that was applied
  // first (Jupiter model): if the remote deletion sat before us, slide our
  // position back; if a remote insert landed at or before us, slide forward.
  function rebase(pending, remote) {
    const insLen = Array.from(remote.ins || "").length;
    if (remote.del > 0 && pending.pos > remote.pos) {
      pending.pos = Math.max(remote.pos, pending.pos - remote.del);
    }
    if (insLen && remote.pos <= pending.pos) pending.pos += insLen;
    return pending;
  }

  // diffOp reduces an edit (oldStr -> newStr) to a single {pos,del,ins} op
  // by matching the common prefix and suffix. Code-point based to match the
  // host's rune indexing.
  function diffOp(oldStr, newStr, author) {
    const a = Array.from(oldStr), b = Array.from(newStr);
    let s = 0;
    while (s < a.length && s < b.length && a[s] === b[s]) s++;
    let e = 0;
    while (e < a.length - s && e < b.length - s && a[a.length - 1 - e] === b[b.length - 1 - e]) e++;
    return { author, baseRev: 0, pos: s, del: a.length - s - e, ins: b.slice(s, b.length - e).join("") };
  }

  const lib = { FRAME_PTY, FRAME_CTRL, FRAME_REPLAY, b64urlDecode, importKey, sealFrame, openFrame, rebase, diffOp };
  root.TandemLib = lib;
  if (typeof module !== "undefined" && module.exports) module.exports = lib;
})(typeof globalThis !== "undefined" ? globalThis : this);

// Unit tests for the guest client's pure helpers and frame crypto.
// Run with: node --test web/
// No browser, no relay — lib.js runs unchanged under Node's WebCrypto.
import test from "node:test";
import assert from "node:assert/strict";
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
const lib = require("./lib.js");

test("diffOp: pure insert at end", () => {
  assert.deepEqual(lib.diffOp("abc", "abcd", "me"), { author: "me", baseRev: 0, pos: 3, del: 0, ins: "d" });
});

test("diffOp: pure delete in middle", () => {
  assert.deepEqual(lib.diffOp("abcd", "ad", "me"), { author: "me", baseRev: 0, pos: 1, del: 2, ins: "" });
});

test("diffOp: replace in middle", () => {
  assert.deepEqual(lib.diffOp("hello", "hEllo", "me"), { author: "me", baseRev: 0, pos: 1, del: 1, ins: "E" });
});

test("diffOp: code-point aware (emoji)", () => {
  // A single astral emoji is one code point, not two UTF-16 units.
  const op = lib.diffOp("a🙂b", "a🙂🎉b", "me");
  assert.equal(op.pos, 2);
  assert.equal(op.del, 0);
  assert.equal(op.ins, "🎉");
});

test("diffOp: no change yields empty op", () => {
  assert.deepEqual(lib.diffOp("same", "same", "me"), { author: "me", baseRev: 0, pos: 4, del: 0, ins: "" });
});

test("rebase: remote insert before pending shifts pending forward", () => {
  assert.equal(lib.rebase({ pos: 5, del: 0, ins: "x" }, { pos: 2, del: 0, ins: "ab" }).pos, 7);
});

test("rebase: remote delete before pending shifts pending back", () => {
  assert.equal(lib.rebase({ pos: 5, del: 0, ins: "x" }, { pos: 1, del: 2, ins: "" }).pos, 3);
});

test("rebase: remote insert after pending leaves pending put", () => {
  assert.equal(lib.rebase({ pos: 2, del: 0, ins: "x" }, { pos: 5, del: 0, ins: "ab" }).pos, 2);
});

test("rebase: remote delete straddling clamps to remote pos", () => {
  assert.equal(lib.rebase({ pos: 3, del: 0, ins: "x" }, { pos: 1, del: 10, ins: "" }).pos, 1);
});

async function makeKey() {
  const raw = new Uint8Array(32); // deterministic all-zero key for tests
  return crypto.subtle.importKey("raw", raw, "AES-GCM", false, ["encrypt", "decrypt"]);
}

test("sealFrame/openFrame round-trips a control object", async () => {
  const key = await makeKey();
  const frame = await lib.sealFrame(key, lib.FRAME_CTRL, { type: "op", pos: 3 });
  const plain = await lib.openFrame(key, frame.buffer);
  assert.equal(plain[0], lib.FRAME_CTRL);
  assert.deepEqual(JSON.parse(new TextDecoder().decode(plain.slice(1))), { type: "op", pos: 3 });
});

test("openFrame rejects a too-short frame", async () => {
  const key = await makeKey();
  await assert.rejects(() => lib.openFrame(key, new Uint8Array(5).buffer));
});

test("openFrame rejects a tampered ciphertext", async () => {
  const key = await makeKey();
  const frame = await lib.sealFrame(key, lib.FRAME_CTRL, { type: "op" });
  frame[frame.length - 1] ^= 0xff; // flip a tag byte
  await assert.rejects(() => lib.openFrame(key, frame.buffer));
});

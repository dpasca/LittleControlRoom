const { test } = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const vm = require("node:vm");

function harness(tabs, storage = {}) {
  const events = {};
  const event = (name) => ({ addListener: (fn) => { events[name] = fn; } });
  let connection;
  const context = vm.createContext({
    console, setTimeout: () => {}, Date,
    audioEndpoint: "ws://127.0.0.1/private",
    WebSocket: class {
      constructor() { connection = this; }
      close() { this.onclose(); }
    },
    chrome: {
      runtime: { id: "lcr", onStartup: event("startup"), onInstalled: event("installed") },
      storage: { session: {
        get: async () => storage,
        set: async (value) => Object.assign(storage, value),
      } },
      alarms: { onAlarm: event("alarm"), create: async () => {} },
      tabs: {
        onCreated: event("created"), onUpdated: event("updated"), onRemoved: event("removed"),
        query: async () => [...tabs.values()].map((tab) => structuredClone(tab)),
        get: async (id) => {
          if (!tabs.has(id)) throw new Error("tab closed");
          return structuredClone(tabs.get(id));
        },
        update: async (id, change) => {
          const tab = tabs.get(id);
          if (!tab) throw new Error("tab closed");
          tab.mutedInfo = { muted: change.muted, reason: "extension", extensionId: "lcr" };
          events.updated(id, { mutedInfo: structuredClone(tab.mutedInfo) }, structuredClone(tab));
        },
      },
    },
  });
  vm.runInContext(fs.readFileSync(`${__dirname}/managed_audio_extension.js`, "utf8"), context);
  return {
    events, storage,
    async drain() {
      for (let i = 0; i < 20; ++i) {
        const pending = vm.runInContext("queue", context);
        await pending;
        if (pending === vm.runInContext("queue", context)) return;
      }
      throw new Error("extension event queue did not settle");
    },
    policy(muted) { connection.onmessage({ data: JSON.stringify({ muted }) }); },
    disconnect() { connection.close(); },
  };
}

test("startup, popups, reveal, re-hide and disconnect preserve user mute choices", async () => {
  const tabs = new Map([
    [1, { id: 1, mutedInfo: { muted: false } }],
    [2, { id: 2, mutedInfo: { muted: true, reason: "user" } }],
  ]);
  const h = harness(tabs);
  await h.drain();
  assert.equal(tabs.get(1).mutedInfo.muted, true);
  assert.equal(tabs.get(2).mutedInfo.reason, "user");
  tabs.set(3, { id: 3, mutedInfo: { muted: false } });
  h.events.created(structuredClone(tabs.get(3)));
  await h.drain();
  assert.equal(tabs.get(3).mutedInfo.muted, true);
  h.policy(false);
  await h.drain();
  assert.equal(tabs.get(1).mutedInfo.muted, false);
  assert.equal(tabs.get(3).mutedInfo.muted, false);
  assert.equal(tabs.get(2).mutedInfo.muted, true);

  // A user mutes a previously LCR-controlled tab while the window is visible.
  tabs.get(1).mutedInfo = { muted: true, reason: "user" };
  h.events.updated(1, { mutedInfo: tabs.get(1).mutedInfo }, structuredClone(tabs.get(1)));
  h.policy(true);
  await h.drain();
  h.policy(false);
  await h.drain();
  assert.equal(tabs.get(1).mutedInfo.muted, true);
  assert.equal(tabs.get(3).mutedInfo.muted, false);
  h.disconnect();
  await h.drain();
  assert.equal(tabs.get(3).mutedInfo.muted, true);
});

test("worker restart retains ownership, and stale events do not undo user muting", async () => {
  const tabs = new Map([[1, { id: 1, mutedInfo: { muted: false } }]]);
  const initial = harness(tabs);
  await initial.drain();
  const resumed = harness(tabs, initial.storage);
  await resumed.drain();
  resumed.policy(false);
  await resumed.drain();
  assert.equal(tabs.get(1).mutedInfo.muted, false);
  const stale = structuredClone(tabs.get(1));
  tabs.get(1).mutedInfo = { muted: true, reason: "user" };
  resumed.policy(true);
  resumed.events.created(stale);
  await resumed.drain();
  resumed.policy(false);
  await resumed.drain();
  assert.equal(tabs.get(1).mutedInfo.reason, "user");
});

test("rapid hide/reveal changes converge and another extension's mute survives", async () => {
  const tabs = new Map([[1, { id: 1, mutedInfo: { muted: false } }]]);
  const h = harness(tabs);
  await h.drain();
  h.policy(false);
  h.policy(true);
  h.policy(false);
  await h.drain();
  assert.equal(tabs.get(1).mutedInfo.muted, false);
  h.policy(true);
  await h.drain();
  tabs.get(1).mutedInfo = { muted: true, reason: "extension", extensionId: "another" };
  h.events.updated(1, { mutedInfo: tabs.get(1).mutedInfo }, structuredClone(tabs.get(1)));
  h.policy(false);
  await h.drain();
  assert.equal(tabs.get(1).mutedInfo.extensionId, "another");
});

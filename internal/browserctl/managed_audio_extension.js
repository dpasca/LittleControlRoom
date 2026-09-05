// Native tab muting covers media elements, frames, and Web Audio without
// changing page scripts or their playback state. Never infer a handoff from
// focus: automation itself can focus a tab while the application is hidden.
let muted = true;
let socket = null;
let lastPolicyAt = 0;
let owned = new Set();
let queue = chrome.storage.session.get("owned").then((state) => {
  owned = new Set(state.owned || []);
});

function serialize(action) {
  queue = queue.then(action).catch((error) => console.error("LCR audio:", error));
  return queue;
}

async function reconcileTab(tab) {
  if (typeof tab.id !== "number") return;
  // Events may have queued behind a policy transition; use current mute state.
  tab = await chrome.tabs.get(tab.id);
  const info = tab.mutedInfo || {};
  if (muted && !info.muted) {
    // Persist ownership before the API call so a worker restart can restore it.
    owned.add(tab.id);
    await chrome.storage.session.set({ owned: [...owned] });
    await chrome.tabs.update(tab.id, { muted: true });
  } else if (!muted && owned.has(tab.id)) {
    if (info.muted && info.reason === "extension" && info.extensionId === chrome.runtime.id) {
      await chrome.tabs.update(tab.id, { muted: false });
    }
    owned.delete(tab.id);
    await chrome.storage.session.set({ owned: [...owned] });
  }
}

async function reconcileAll() {
  for (const tab of await chrome.tabs.query({})) {
    // Closing one tab must not prevent the rest from being muted.
    await reconcileTab(tab).catch((error) => console.error("LCR audio tab:", error));
  }
}

function setMuted(value) {
  return serialize(async () => {
    muted = value;
    await reconcileAll();
  });
}

chrome.tabs.onCreated.addListener((tab) => serialize(() => reconcileTab(tab)));
chrome.tabs.onUpdated.addListener((id, change, tab) => serialize(async () => {
  if (change.mutedInfo && (change.mutedInfo.reason !== "extension" ||
      change.mutedInfo.extensionId !== chrome.runtime.id)) {
    owned.delete(id);
    await chrome.storage.session.set({ owned: [...owned] });
  }
  await reconcileTab(tab);
}));
chrome.tabs.onRemoved.addListener((id) => serialize(async () => {
  owned.delete(id);
  await chrome.storage.session.set({ owned: [...owned] });
}));

function connect() {
  if (socket) return;
  const connection = new WebSocket(audioEndpoint);
  socket = connection;
  connection.onmessage = (event) => {
    try {
      const policy = JSON.parse(event.data);
      if (typeof policy.muted !== "boolean") throw new Error("invalid audio policy");
      lastPolicyAt = Date.now();
      void setMuted(policy.muted);
    } catch (_) {
      void setMuted(true);
    }
  };
  connection.onclose = () => {
    if (socket !== connection) return;
    socket = null;
    void setMuted(true);
    setTimeout(connect, 1000);
  };
  connection.onerror = () => connection.close();
}

// Messages every ten seconds keep the MV3 worker alive while connected.
// Alarms recover an unexpectedly suspended worker; every restart starts muted.
chrome.alarms.onAlarm.addListener(() => {
  if (Date.now() - lastPolicyAt > 15000) {
    void setMuted(true);
    if (socket) socket.close();
  }
  connect();
});
chrome.runtime.onStartup.addListener(connect);
chrome.runtime.onInstalled.addListener(connect);
void chrome.alarms.create("audio-watchdog", { periodInMinutes: 0.5 });
void setMuted(true);
connect();

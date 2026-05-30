const readline = require("readline");
const path = require("path");

function loadPlaywright() {
  try {
    return require("playwright");
  } catch (_) {
    return require("playwright-core");
  }
}

const { chromium } = loadPlaywright();
const config = JSON.parse(process.env.LCR_BROWSER_WORKER_CONFIG || "{}");
let context = null;
let page = null;
let screenshotCount = 0;

async function ensurePage() {
  if (!context) {
    const headless = config.launchMode === "headless";
    context = await chromium.launchPersistentContext(config.profileDir, {
      headless,
      downloadsPath: config.outputDir,
      viewport: { width: 1280, height: 900 },
    });
    page = context.pages()[0] || await context.newPage();
  }
  if (!page || page.isClosed()) {
    page = context.pages()[0] || await context.newPage();
  }
  return page;
}

async function pageState(extra = {}) {
  const p = await ensurePage();
  return {
    URL: p.url(),
    Title: await p.title().catch(() => ""),
    Fresh: true,
    ...extra,
  };
}

async function snapshot(maxChars) {
  const p = await ensurePage();
  const body = await p.evaluate(() => {
    function visible(el) {
      const style = window.getComputedStyle(el);
      const rect = el.getBoundingClientRect();
      return style && style.visibility !== "hidden" && style.display !== "none" && rect.width > 0 && rect.height > 0;
    }
    function roleFor(el) {
      const explicit = el.getAttribute("role");
      if (explicit) return explicit;
      const tag = el.tagName.toLowerCase();
      if (tag === "a") return "link";
      if (tag === "button") return "button";
      if (tag === "input" || tag === "textarea") return "textbox";
      if (tag === "select") return "combobox";
      if (/^h[1-6]$/.test(tag)) return "heading";
      return "";
    }
    function nameFor(el) {
      return (el.getAttribute("aria-label") || el.innerText || el.value || el.textContent || "").replace(/\s+/g, " ").trim();
    }
    let seq = 1;
    const lines = [];
    const elements = Array.from(document.querySelectorAll("a,button,input,textarea,select,[role],h1,h2,h3,h4,h5,h6"));
    for (const el of elements) {
      if (!visible(el)) continue;
      const role = roleFor(el);
      const name = nameFor(el);
      if (!role || !name) continue;
      let ref = el.getAttribute("data-lcr-ref");
      if (!ref) {
        ref = "e" + seq++;
        el.setAttribute("data-lcr-ref", ref);
      }
      lines.push(`- ${role} "${name}" [ref=${ref}]`);
    }
    return lines.join("\n");
  });
  let text = `url: ${p.url()}\ntitle: ${await p.title().catch(() => "")}\nsnapshot:\n${body}`;
  if (maxChars && maxChars > 0 && text.length > maxChars) {
    text = text.slice(0, maxChars) + "\n[truncated]";
  }
  return pageState({ Status: "snapshot", Snapshot: text });
}

async function byRef(ref) {
  const p = await ensurePage();
  const locator = p.locator(`[data-lcr-ref="${String(ref).replace(/"/g, '\\"')}"]`).first();
  if ((await locator.count()) === 0) {
    throw new Error(`stale or unknown browser ref ${ref}; call browser_snapshot again`);
  }
  return locator;
}

async function handle(method, params) {
  const p = await ensurePage();
  if (method === "navigate") {
    const response = await p.goto(params.url, { waitUntil: "domcontentloaded", timeout: 30000 });
    return pageState({ Status: response ? `navigated ${response.status()}` : "navigated" });
  }
  if (method === "snapshot") return snapshot(params.max_chars || 0);
  if (method === "click") {
    await (await byRef(params.ref)).click();
    await p.waitForLoadState("domcontentloaded", { timeout: 5000 }).catch(() => {});
    return pageState({ Status: "clicked" });
  }
  if (method === "fill") {
    await (await byRef(params.ref)).fill(params.value || "");
    return pageState({ Status: "filled" });
  }
  if (method === "press") {
    await p.keyboard.press(params.key);
    await p.waitForLoadState("domcontentloaded", { timeout: 5000 }).catch(() => {});
    return pageState({ Status: "pressed" });
  }
  if (method === "screenshot") {
    const requested = (params.path || "").trim();
    const artifactPath = requested
      ? (path.isAbsolute(requested) ? requested : path.join(config.outputDir, requested))
      : path.join(config.outputDir, `screenshot-${++screenshotCount}.png`);
    await p.screenshot({ path: artifactPath, fullPage: true });
    return pageState({ Status: "screenshot", ArtifactPath: artifactPath });
  }
  if (method === "current_page") return pageState({ Status: "current_page" });
  throw new Error(`unsupported browser method: ${method}`);
}

const rl = readline.createInterface({ input: process.stdin });
rl.on("line", async (line) => {
  let req;
  try {
    req = JSON.parse(line);
    const result = await handle(req.method, req.params || {});
    process.stdout.write(JSON.stringify({ id: req.id, ok: true, result }) + "\n");
  } catch (err) {
    process.stdout.write(JSON.stringify({ id: req && req.id || "", ok: false, error: err && err.message || String(err) }) + "\n");
  }
});

process.on("SIGINT", async () => {
  if (context) await context.close().catch(() => {});
  process.exit(0);
});

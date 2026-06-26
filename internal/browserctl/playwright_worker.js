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
    context = await launchPersistentContext({
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

async function launchPersistentContext(options) {
  const channel = typeof config.browserChannel === "string" ? config.browserChannel.trim() : "";
  if (channel) {
    try {
      return await chromium.launchPersistentContext(config.profileDir, { ...options, channel });
    } catch (err) {
      if (channel !== "chrome") throw err;
    }
  }
  return chromium.launchPersistentContext(config.profileDir, options);
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

function browserSearchQuery(params) {
  const parts = [String(params.query || "").trim()];
  const site = String(params.site || "").trim();
  if (site) parts.push(`site:${site.replace(/^https?:\/\//, "").replace(/^www\./, "").split("/")[0]}`);
  const recencyDays = Number(params.recency_days || 0);
  if (recencyDays > 0) {
    const after = new Date(Date.now() - recencyDays * 24 * 60 * 60 * 1000);
    parts.push(`after:${after.toISOString().slice(0, 10)}`);
  }
  return parts.filter(Boolean).join(" ");
}

function formatBrowserSearchOutput(query, results) {
  const lines = [
    "backend: browser",
    `query: ${String(query || "").trim()}`,
    `results: ${results.length}`,
    "",
  ];
  if (results.length === 0) {
    lines.push("No results.");
  }
  results.forEach((result, index) => {
    lines.push(`${index + 1}. ${result.title}`);
    if (result.url) lines.push(`   url: ${result.url}`);
    lines.push("   source: Google via managed browser");
    if (result.snippet) lines.push(`   snippet: ${result.snippet}`);
  });
  return lines.join("\n") + "\n";
}

async function clickGoogleConsent(p) {
  await p.evaluate(() => {
    const clean = (value) => (value || "").replace(/\s+/g, " ").trim();
    const labels = [
      "accept all",
      "i agree",
      "agree",
      "accetta tutto",
      "tout accepter",
      "aceptar todo",
      "alle akzeptieren",
    ];
    const elements = Array.from(document.querySelectorAll("button,[role=button],input[type=submit],a"));
    for (const el of elements) {
      const text = clean(el.innerText || el.value || el.textContent).toLowerCase();
      if (text && labels.some((label) => text.includes(label))) {
        el.click();
        break;
      }
    }
  }).catch(() => {});
}

async function searchGoogle(params) {
  const maxResults = Math.max(1, Math.min(10, Number(params.max_results || 5)));
  const displayQuery = String(params.query || "").trim();
  if (!displayQuery) throw new Error("search_google query is required");

  await ensurePage();
  const searchPage = await context.newPage();
  try {
    const url = new URL("https://www.google.com/search");
    url.searchParams.set("q", browserSearchQuery(params));
    url.searchParams.set("hl", "en");
    await searchPage.goto(url.toString(), { waitUntil: "domcontentloaded", timeout: 30000 });
    await clickGoogleConsent(searchPage);
    await searchPage.waitForLoadState("domcontentloaded", { timeout: 5000 }).catch(() => {});
    await searchPage.waitForTimeout(300).catch(() => {});
    const extracted = await searchPage.evaluate((limit) => {
      const clean = (value) => (value || "").replace(/\s+/g, " ").trim();
      const visible = (el) => {
        const rect = el.getBoundingClientRect();
        const style = window.getComputedStyle(el);
        return rect.width > 0 && rect.height > 0 && style.display !== "none" && style.visibility !== "hidden" && style.opacity !== "0";
      };
      const badHost = (host) =>
        host.endsWith("google.com") ||
        host.includes(".google.") ||
        host.endsWith("gstatic.com") ||
        host.endsWith("googleusercontent.com");
      const bodyText = clean(document.body.innerText || "");
      if ((bodyText.toLowerCase().includes("unusual traffic") || bodyText.toLowerCase().includes("not a robot")) && !document.querySelector("a[href]")) {
        return { blocked: true, url: location.href, title: document.title || "", results: [] };
      }
      const seen = new Set();
      const results = [];
      for (const a of document.querySelectorAll("a[href]")) {
        if (!visible(a)) continue;
        let href = a.href || "";
        try {
          const maybe = new URL(href);
          if (maybe.pathname === "/url" && maybe.searchParams.get("q")) href = maybe.searchParams.get("q");
        } catch (_) {}
        let parsed;
        try {
          parsed = new URL(href);
        } catch (_) {
          continue;
        }
        if (!/^https?:$/.test(parsed.protocol) || badHost(parsed.hostname) || seen.has(parsed.href)) continue;
        const title = clean(a.innerText || a.textContent);
        if (title.length < 3) continue;
        let container = a;
        for (let i = 0; i < 5 && container.parentElement; i++) {
          const parentText = clean(container.parentElement.innerText || container.parentElement.textContent);
          if (parentText.length > title.length + 20) {
            container = container.parentElement;
          } else {
            break;
          }
        }
        const fullText = clean(container.innerText || container.textContent);
        let snippet = fullText.replace(title, "").trim();
        if (snippet.length > 320) snippet = snippet.slice(0, 320).trim();
        seen.add(parsed.href);
        results.push({ title: title.slice(0, 180), url: parsed.href, snippet });
        if (results.length >= limit) break;
      }
      return { blocked: false, url: location.href, title: document.title || "", results };
    }, maxResults);
    if (extracted.blocked) {
      throw new Error("Google search page appears to require CAPTCHA or unusual-traffic verification");
    }
    return {
      URL: extracted.url || searchPage.url(),
      Title: extracted.title || await searchPage.title().catch(() => ""),
      Status: "searched",
      Snapshot: formatBrowserSearchOutput(displayQuery, extracted.results || []),
      Fresh: true,
    };
  } finally {
    await searchPage.close().catch(() => {});
  }
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
  if (method === "search_google") return searchGoogle(params);
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

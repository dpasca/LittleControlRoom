package tui

import (
	"fmt"
	"html"
	"strings"

	"lcroom/internal/config"
)

const (
	mobileMockupPhoneWidth  = 390
	mobileMockupPhoneHeight = 844
	mobileMockupCropLeft    = 10
	mobileMockupCropRight   = 10
	mobileMockupCropTop     = 10
	mobileMockupCropBottom  = 18
)

type mobileMockupSpec struct {
	name   string
	title  string
	screen string
}

func generateMobileMockupAssets(config.ScreenshotConfig) []ScreenshotAsset {
	specs := []mobileMockupSpec{
		{name: "mobile-dashboard", title: "Mobile - Dashboard", screen: "dashboard"},
		{name: "mobile-boss-chat", title: "Mobile - Boss Chat", screen: "boss"},
		{name: "mobile-project-detail", title: "Mobile - Project Detail", screen: "project"},
		{name: "mobile-engineer-session", title: "Mobile - Engineer Session", screen: "engineer"},
	}

	assets := make([]ScreenshotAsset, 0, len(specs))
	for _, spec := range specs {
		body := mobileMockupScreenBody(spec.screen)
		doc, width, height := renderMobileMockupHTML(spec.title, body)
		assets = append(assets, ScreenshotAsset{
			Name:   spec.name,
			Title:  spec.title,
			ANSI:   "",
			HTML:   doc,
			Width:  width,
			Height: height,
		})
	}
	return assets
}

func renderMobileMockupHTML(title, body string) (string, int, int) {
	width := mobileMockupPhoneWidth + mobileMockupCropLeft + mobileMockupCropRight
	height := mobileMockupPhoneHeight + mobileMockupCropTop + mobileMockupCropBottom

	var out strings.Builder
	out.WriteString(`<!doctype html><html lang="en"><head><meta charset="utf-8">`)
	fmt.Fprintf(&out, `<meta name="viewport" content="width=%d,height=%d,initial-scale=1">`, width, height)
	out.WriteString(`<title>`)
	out.WriteString(html.EscapeString(title))
	out.WriteString(`</title><style>`)
	out.WriteString(mobileMockupCSS(width, height))
	out.WriteString(`</style></head><body>`)
	fmt.Fprintf(&out, `<div class="stage" style="width:%dpx;height:%dpx">`, width, height)
	out.WriteString(`<main class="phone" aria-label="`)
	out.WriteString(html.EscapeString(title))
	out.WriteString(`"><section class="app">`)
	out.WriteString(body)
	out.WriteString(`</section></main></div></body></html>`)
	return out.String(), width, height
}

func mobileMockupCSS(width, height int) string {
	return fmt.Sprintf(`
:root {
  color-scheme: dark;
  --bg: #0b0d0c;
  --app: #101312;
  --panel: #171c1a;
  --panel-2: #1f2523;
  --panel-3: #252728;
  --ink: #f6f0df;
  --muted: #a6aaa4;
  --quiet: #747c76;
  --line: #303633;
  --amber: #f2c36b;
  --green: #77c98f;
  --blue: #93b9ff;
  --coral: #ff806f;
  --violet: #c8a3ff;
}
* { box-sizing: border-box; }
html, body {
  margin: 0;
  padding: 0;
  width: %dpx;
  height: %dpx;
  overflow: hidden;
  background: var(--bg);
  font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
  -webkit-font-smoothing: antialiased;
  font-synthesis: none;
}
.stage {
  position: relative;
  overflow: hidden;
  padding: %dpx %dpx %dpx %dpx;
  background:
    linear-gradient(135deg, rgba(242, 195, 107, 0.08), transparent 35%%),
    linear-gradient(315deg, rgba(147, 185, 255, 0.10), transparent 38%%),
    var(--bg);
}
.phone {
  width: %dpx;
  height: %dpx;
  overflow: hidden;
  border: 1px solid #343936;
  border-radius: 36px;
  background: var(--app);
  box-shadow: 0 28px 80px rgba(0, 0, 0, 0.54), inset 0 1px 0 rgba(255, 255, 255, 0.08);
}
.app {
  height: 100%%;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  background:
    linear-gradient(180deg, rgba(255, 255, 255, 0.035), transparent 28%%),
    var(--app);
  color: var(--ink);
}
.statusbar {
  height: 34px;
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  padding: 0 22px 8px;
  color: var(--muted);
  font-size: 12px;
  line-height: 1;
}
.status-icons {
  display: flex;
  align-items: center;
  gap: 7px;
}
.signal {
  width: 18px;
  height: 11px;
  display: grid;
  grid-template-columns: repeat(4, 3px);
  align-items: end;
  gap: 2px;
}
.signal i { display: block; width: 3px; border-radius: 2px; background: var(--muted); }
.signal i:nth-child(1) { height: 4px; }
.signal i:nth-child(2) { height: 6px; }
.signal i:nth-child(3) { height: 8px; }
.signal i:nth-child(4) { height: 11px; }
.battery {
  width: 22px;
  height: 11px;
  border: 1px solid var(--muted);
  border-radius: 3px;
  position: relative;
}
.battery::before {
  content: "";
  position: absolute;
  right: 2px;
  top: 2px;
  width: 13px;
  height: 5px;
  border-radius: 1px;
  background: var(--green);
}
.battery::after {
  content: "";
  position: absolute;
  right: -4px;
  top: 3px;
  width: 2px;
  height: 5px;
  border-radius: 0 2px 2px 0;
  background: var(--muted);
}
.topbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 12px 20px 10px;
}
.eyebrow {
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--muted);
  font-size: 12px;
  line-height: 1.2;
}
.dot {
  width: 8px;
  height: 8px;
  border-radius: 50%%;
  background: var(--green);
  box-shadow: 0 0 0 3px rgba(119, 201, 143, 0.14);
}
.title {
  margin: 2px 0 0;
  font-size: 30px;
  line-height: 1;
  letter-spacing: 0;
  font-weight: 780;
}
.icon-button {
  width: 42px;
  height: 42px;
  border: 1px solid var(--line);
  border-radius: 12px;
  display: grid;
  place-items: center;
  color: var(--ink);
  background: rgba(255, 255, 255, 0.035);
  font-size: 18px;
}
.content {
  flex: 1;
  min-height: 0;
  overflow: hidden;
  padding: 0 16px 12px;
}
.tabs {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: 6px;
  padding: 0 4px 12px;
}
.tab {
  height: 36px;
  border: 1px solid var(--line);
  border-radius: 11px;
  display: grid;
  place-items: center;
  color: var(--muted);
  font-size: 13px;
  font-weight: 680;
  background: rgba(255, 255, 255, 0.025);
}
.tab.active {
  color: #17120a;
  border-color: transparent;
  background: var(--amber);
}
.hero-stat {
  padding: 15px;
  margin-bottom: 10px;
  border: 1px solid rgba(242, 195, 107, 0.24);
  border-radius: 18px;
  background: linear-gradient(135deg, rgba(242, 195, 107, 0.13), rgba(119, 201, 143, 0.06));
}
.hero-stat .big {
  display: flex;
  align-items: baseline;
  gap: 8px;
  font-size: 38px;
  line-height: 0.95;
  font-weight: 820;
}
.hero-stat .big span {
  font-size: 15px;
  color: var(--muted);
  font-weight: 620;
}
.hero-stat p {
  margin: 8px 0 0;
  color: var(--muted);
  font-size: 14px;
  line-height: 1.35;
}
.section-title {
  margin: 14px 4px 8px;
  color: var(--muted);
  font-size: 12px;
  font-weight: 760;
  letter-spacing: 0.08em;
  text-transform: uppercase;
}
.project-list {
  display: grid;
  gap: 8px;
}
.project-row,
.session-row,
.info-row {
  border: 1px solid var(--line);
  border-radius: 16px;
  background: var(--panel);
  overflow: hidden;
}
.project-row {
  padding: 13px 13px 12px;
}
.row-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 10px;
}
.row-title {
  min-width: 0;
  font-size: 16px;
  line-height: 1.1;
  font-weight: 760;
}
.row-sub {
  margin-top: 5px;
  color: var(--muted);
  font-size: 13px;
  line-height: 1.3;
}
.pills {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 10px;
}
.pill {
  min-height: 24px;
  padding: 4px 8px;
  border: 1px solid var(--line);
  border-radius: 999px;
  color: var(--muted);
  background: rgba(255, 255, 255, 0.025);
  font-size: 11px;
  font-weight: 720;
  line-height: 1.2;
}
.pill.green { color: #0f2117; background: var(--green); border-color: transparent; }
.pill.amber { color: #241807; background: var(--amber); border-color: transparent; }
.pill.blue { color: #0b1729; background: var(--blue); border-color: transparent; }
.pill.coral { color: #2c0d08; background: var(--coral); border-color: transparent; }
.pill.violet { color: #1c102d; background: var(--violet); border-color: transparent; }
.mini-score {
  flex: 0 0 auto;
  width: 42px;
  height: 42px;
  border-radius: 13px;
  display: grid;
  place-items: center;
  font-size: 15px;
  font-weight: 820;
  color: #201406;
  background: var(--amber);
}
.bottom-nav {
  height: 76px;
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: 4px;
  padding: 8px 13px 16px;
  border-top: 1px solid var(--line);
  background: rgba(12, 15, 14, 0.94);
}
.nav-item {
  display: grid;
  justify-items: center;
  align-content: center;
  gap: 5px;
  color: var(--quiet);
  font-size: 11px;
  font-weight: 700;
}
.nav-icon {
  width: 24px;
  height: 20px;
  border: 1.5px solid currentColor;
  border-radius: 7px;
  position: relative;
}
.nav-icon.chat { border-radius: 9px; }
.nav-icon.chat::after {
  content: "";
  position: absolute;
  left: 5px;
  bottom: -5px;
  width: 7px;
  height: 7px;
  border-left: 1.5px solid currentColor;
  border-bottom: 1.5px solid currentColor;
  transform: skew(-20deg);
}
.nav-icon.bars::before,
.nav-icon.bars::after {
  content: "";
  position: absolute;
  left: 5px;
  right: 5px;
  height: 1.5px;
  background: currentColor;
}
.nav-icon.bars::before { top: 6px; }
.nav-icon.bars::after { top: 12px; }
.nav-icon.gear::before {
  content: "";
  position: absolute;
  inset: 6px;
  border: 1.5px solid currentColor;
  border-radius: 50%%;
}
.nav-item.active { color: var(--amber); }
.chat-thread {
  display: grid;
  gap: 10px;
  padding: 0 3px 12px;
}
.bubble {
  max-width: 86%%;
  padding: 12px 13px;
  border-radius: 18px;
  font-size: 15px;
  line-height: 1.35;
}
.bubble.user {
  justify-self: end;
  color: #18120a;
  background: var(--amber);
  border-bottom-right-radius: 6px;
}
.bubble.assistant {
  justify-self: start;
  color: var(--ink);
  background: var(--panel);
  border: 1px solid var(--line);
  border-bottom-left-radius: 6px;
}
.action-stack {
  display: grid;
  gap: 8px;
  margin: 4px 0 12px;
}
.action-button {
  min-height: 48px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  padding: 10px 13px;
  border-radius: 15px;
  border: 1px solid rgba(242, 195, 107, 0.32);
  color: var(--ink);
  background: rgba(242, 195, 107, 0.08);
  font-size: 14px;
  font-weight: 720;
}
.composer {
  display: flex;
  align-items: center;
  gap: 8px;
  min-height: 58px;
  padding: 8px 13px 16px;
  border-top: 1px solid var(--line);
  background: rgba(12, 15, 14, 0.95);
}
.composer-field {
  flex: 1;
  min-height: 40px;
  display: flex;
  align-items: center;
  padding: 0 13px;
  border: 1px solid var(--line);
  border-radius: 14px;
  color: var(--muted);
  background: var(--panel);
  font-size: 14px;
}
.send-button {
  width: 42px;
  height: 40px;
  display: grid;
  place-items: center;
  border-radius: 14px;
  color: #161006;
  background: var(--amber);
  font-size: 18px;
  font-weight: 820;
}
.project-hero {
  padding: 15px;
  margin: 0 0 12px;
  border-radius: 20px;
  border: 1px solid var(--line);
  background: var(--panel);
}
.project-hero h2 {
  margin: 0;
  font-size: 24px;
  line-height: 1;
  letter-spacing: 0;
}
.project-hero p {
  margin: 9px 0 0;
  color: var(--muted);
  font-size: 14px;
  line-height: 1.35;
}
.primary-actions {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 8px;
  margin-top: 13px;
}
.primary-action {
  min-height: 44px;
  display: grid;
  place-items: center;
  border-radius: 14px;
  font-size: 14px;
  font-weight: 760;
}
.primary-action.main {
  color: #18120a;
  background: var(--amber);
}
.primary-action.secondary {
  color: var(--ink);
  border: 1px solid var(--line);
  background: rgba(255, 255, 255, 0.035);
}
.info-stack {
  display: grid;
  gap: 9px;
}
.info-row {
  display: grid;
  grid-template-columns: 42px 1fr auto;
  align-items: center;
  gap: 10px;
  min-height: 66px;
  padding: 11px 12px;
}
.info-glyph {
  width: 42px;
  height: 42px;
  display: grid;
  place-items: center;
  border-radius: 13px;
  color: #141816;
  background: var(--green);
  font-size: 18px;
  font-weight: 840;
}
.info-glyph.blue { background: var(--blue); }
.info-glyph.amber { background: var(--amber); }
.info-glyph.violet { background: var(--violet); }
.info-label {
  font-size: 15px;
  font-weight: 760;
}
.info-meta {
  margin-top: 3px;
  color: var(--muted);
  font-size: 13px;
  line-height: 1.25;
}
.chevron {
  color: var(--quiet);
  font-size: 22px;
}
.transcript {
  display: grid;
  gap: 8px;
  padding: 0 3px 10px;
}
.transcript-line {
  border: 1px solid var(--line);
  border-radius: 15px;
  padding: 10px 12px;
  background: var(--panel);
}
.transcript-line .kind {
  color: var(--muted);
  font-size: 11px;
  line-height: 1;
  font-weight: 780;
  text-transform: uppercase;
}
.transcript-line .text {
  margin-top: 6px;
  font-size: 14px;
  line-height: 1.32;
}
.approval {
  margin: 0 16px 10px;
  padding: 12px;
  border: 1px solid rgba(255, 128, 111, 0.42);
  border-radius: 17px;
  background: rgba(255, 128, 111, 0.09);
}
.approval h3 {
  margin: 0;
  color: var(--coral);
  font-size: 14px;
  line-height: 1.2;
}
.approval p {
  margin: 7px 0 11px;
  color: var(--muted);
  font-size: 13px;
  line-height: 1.35;
}
.approval-actions {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 8px;
}
.approval-actions div {
  min-height: 38px;
  display: grid;
  place-items: center;
  border-radius: 12px;
  font-size: 13px;
  font-weight: 780;
}
.approval-actions .deny {
  border: 1px solid var(--line);
  color: var(--ink);
}
.approval-actions .allow {
  color: #170f08;
  background: var(--coral);
}
`, width, height, mobileMockupCropTop, mobileMockupCropRight, mobileMockupCropBottom, mobileMockupCropLeft, mobileMockupPhoneWidth, mobileMockupPhoneHeight)
}

func mobileMockupScreenBody(screen string) string {
	switch screen {
	case "boss":
		return mobileBossChatScreen()
	case "project":
		return mobileProjectDetailScreen()
	case "engineer":
		return mobileEngineerSessionScreen()
	default:
		return mobileDashboardScreen()
	}
}

func mobileStatusBar() string {
	return `<div class="statusbar"><div>9:41</div><div class="status-icons"><div class="signal"><i></i><i></i><i></i><i></i></div><div>LAN</div><div class="battery"></div></div></div>`
}

func mobileTopBar(title, eyebrow, button string) string {
	return `<div class="topbar"><div><div class="eyebrow"><span class="dot"></span><span>` + eyebrow + `</span></div><h1 class="title">` + title + `</h1></div><div class="icon-button">` + button + `</div></div>`
}

func mobileBottomNav(active string) string {
	items := []struct {
		key   string
		label string
		icon  string
	}{
		{key: "work", label: "Work", icon: "bars"},
		{key: "boss", label: "Boss", icon: "chat"},
		{key: "sessions", label: "Sessions", icon: "bars"},
		{key: "settings", label: "Setup", icon: "gear"},
	}
	var out strings.Builder
	out.WriteString(`<nav class="bottom-nav">`)
	for _, item := range items {
		classes := "nav-item"
		if item.key == active {
			classes += " active"
		}
		out.WriteString(`<div class="`)
		out.WriteString(classes)
		out.WriteString(`"><div class="nav-icon `)
		out.WriteString(item.icon)
		out.WriteString(`"></div><div>`)
		out.WriteString(item.label)
		out.WriteString(`</div></div>`)
	}
	out.WriteString(`</nav>`)
	return out.String()
}

func mobileDashboardScreen() string {
	return mobileStatusBar() +
		mobileTopBar("Work", "Little Control Room - paired", "+") +
		`<div class="content">
<div class="tabs"><div class="tab active">Need</div><div class="tab">Active</div><div class="tab">Quiet</div><div class="tab">All</div></div>
<section class="hero-stat"><div class="big">3 <span>items need a human</span></div><p>Three engineer sessions are running. One project is ready for a decision before the next step.</p></section>
<div class="section-title">Attention Queue</div>
<div class="project-list">
<article class="project-row"><div class="row-head"><div><div class="row-title">LittleControlRoom</div><div class="row-sub">Boss 2.0 plan changed; review mobile surface direction.</div></div><div class="mini-score">92</div></div><div class="pills"><span class="pill amber">decision</span><span class="pill green">3 agents</span><span class="pill blue">dirty</span></div></article>
<article class="project-row"><div class="row-head"><div><div class="row-title">assistant-lab</div><div class="row-sub">Codex is active, tests have been quiet for 18 minutes.</div></div><div class="mini-score">76</div></div><div class="pills"><span class="pill green">running</span><span class="pill blue">runtime</span></div></article>
<article class="project-row"><div class="row-head"><div><div class="row-title">billing-tool</div><div class="row-sub">Patch looks complete; commit preview is waiting.</div></div><div class="mini-score">69</div></div><div class="pills"><span class="pill violet">ready</span><span class="pill amber">review</span></div></article>
</div>
</div>` +
		mobileBottomNav("work")
}

func mobileBossChatScreen() string {
	return mobileStatusBar() +
		mobileTopBar("Boss", "LAN session - read first", "...") +
		`<div class="content">
<div class="chat-thread">
<div class="bubble user">What needs me right now?</div>
<div class="bubble assistant">LittleControlRoom is the top item. The mobile interface can start as read-only plus explicit approvals. I would review that direction before opening more engineer work.</div>
<div class="bubble assistant">The other active sessions can keep running. Nothing needs a risky action from the phone yet.</div>
</div>
<div class="section-title">Suggested Moves</div>
<div class="action-stack">
<div class="action-button"><span>Open LittleControlRoom detail</span><span>&gt;</span></div>
<div class="action-button"><span>Summarize active engineer sessions</span><span>&gt;</span></div>
<div class="action-button"><span>Keep quiet projects snoozed</span><span>&gt;</span></div>
</div>
<div class="section-title">Pinned Context</div>
<div class="info-stack">
<div class="info-row"><div class="info-glyph amber">!</div><div><div class="info-label">1 approval boundary</div><div class="info-meta">Mutating actions stay confirm-only on mobile.</div></div><div class="chevron">&gt;</div></div>
<div class="info-row"><div class="info-glyph blue">L</div><div><div class="info-label">LAN paired</div><div class="info-meta">Desktop host is reachable at lcr.local.</div></div><div class="chevron">&gt;</div></div>
</div>
</div>
<div class="composer"><div class="composer-field">Ask Boss...</div><div class="send-button">&gt;</div></div>` +
		mobileBottomNav("boss")
}

func mobileProjectDetailScreen() string {
	return mobileStatusBar() +
		mobileTopBar("Project", "Little Control Room", "&lt;") +
		`<div class="content">
<section class="project-hero">
<h2>LittleControlRoom</h2>
<p>Mobile interface spike. Clean fast-forward from root repo; mockups are ready for taste review.</p>
<div class="pills"><span class="pill amber">decision</span><span class="pill green">agents active</span><span class="pill blue">branch mobile-interface</span></div>
<div class="primary-actions"><div class="primary-action main">Open Latest</div><div class="primary-action secondary">Ask Boss</div></div>
</section>
<div class="section-title">Project Surface</div>
<div class="info-stack">
<div class="info-row"><div class="info-glyph">C</div><div><div class="info-label">Codex engineer</div><div class="info-meta">Waiting on design choice, transcript live.</div></div><div class="chevron">&gt;</div></div>
<div class="info-row"><div class="info-glyph blue">R</div><div><div class="info-label">Runtime</div><div class="info-meta">No managed server running for this project.</div></div><div class="chevron">&gt;</div></div>
<div class="info-row"><div class="info-glyph amber">G</div><div><div class="info-label">Git state</div><div class="info-meta">Working tree has mockup generator changes.</div></div><div class="chevron">&gt;</div></div>
<div class="info-row"><div class="info-glyph violet">T</div><div><div class="info-label">TODOs</div><div class="info-meta">2 ideas tagged mobile, 1 for security.</div></div><div class="chevron">&gt;</div></div>
</div>
</div>` +
		mobileBottomNav("work")
}

func mobileEngineerSessionScreen() string {
	return mobileStatusBar() +
		mobileTopBar("Codex", "LittleControlRoom - live", "&lt;") +
		`<div class="content">
<div class="pills" style="margin: 0 4px 12px;"><span class="pill green">running</span><span class="pill amber">needs approval</span><span class="pill blue">30s timeout</span></div>
<div class="transcript">
<div class="transcript-line"><div class="kind">User</div><div class="text">Make some mockups of how the mobile app would look.</div></div>
<div class="transcript-line"><div class="kind">Plan</div><div class="text">Add phone-sized static screens, render PNGs, then run validation.</div></div>
<div class="transcript-line"><div class="kind">Tool</div><div class="text">Generating dashboard, Boss Chat, project detail, and engineer session mockups.</div></div>
<div class="transcript-line"><div class="kind">Assistant</div><div class="text">The phone surface is an attention cockpit, not a full terminal clone.</div></div>
</div>
</div>
<section class="approval"><h3>Confirm before side effects</h3><p>Allow this session to run validation commands after generating the mockups?</p><div class="approval-actions"><div class="deny">Deny</div><div class="allow">Allow once</div></div></section>
<div class="composer"><div class="composer-field">Message Codex...</div><div class="send-button">&gt;</div></div>` +
		mobileBottomNav("sessions")
}

package tui

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"html"
	"image"
	"image/color"
	"image/png"
	"strings"

	"lcroom/internal/pixelart"
)

type pocketControlRoomMockupSpec struct {
	name   string
	title  string
	screen string
}

func generatePocketControlRoomMockupAssets() []ScreenshotAsset {
	specs := []pocketControlRoomMockupSpec{
		{name: "mobile-pocket-switchboard", title: "Pocket Control Room - Switchboard", screen: "switchboard"},
		{name: "mobile-pocket-project", title: "Pocket Control Room - Project Console", screen: "project"},
		{name: "mobile-pocket-session", title: "Pocket Control Room - Session Interlock", screen: "session"},
	}

	assets := make([]ScreenshotAsset, 0, len(specs))
	for _, spec := range specs {
		doc, width, height := renderPocketControlRoomMockupHTML(spec.title, pocketControlRoomScreenBody(spec.screen))
		assets = append(assets, ScreenshotAsset{
			Name:   spec.name,
			Title:  spec.title,
			HTML:   doc,
			Width:  width,
			Height: height,
		})
	}
	return assets
}

func renderPocketControlRoomMockupHTML(title, body string) (string, int, int) {
	width := mobileMockupPhoneWidth + mobileMockupCropLeft + mobileMockupCropRight
	height := mobileMockupPhoneHeight + mobileMockupCropTop + mobileMockupCropBottom

	var out strings.Builder
	out.WriteString(`<!doctype html><html lang="en"><head><meta charset="utf-8">`)
	fmt.Fprintf(&out, `<meta name="viewport" content="width=%d,height=%d,initial-scale=1">`, width, height)
	out.WriteString(`<title>`)
	out.WriteString(html.EscapeString(title))
	out.WriteString(`</title><style>`)
	out.WriteString(pocketControlRoomMockupCSS(width, height))
	out.WriteString(`</style></head><body>`)
	fmt.Fprintf(&out, `<div class="pocket-stage" style="width:%dpx;height:%dpx">`, width, height)
	out.WriteString(`<main class="pocket-phone" aria-label="`)
	out.WriteString(html.EscapeString(title))
	out.WriteString(`">`)
	out.WriteString(`<i class="case-screw screw-a"></i><i class="case-screw screw-b"></i><i class="case-screw screw-c"></i><i class="case-screw screw-d"></i>`)
	out.WriteString(`<section class="pocket-console">`)
	out.WriteString(body)
	out.WriteString(`</section></main></div></body></html>`)
	return out.String(), width, height
}

func pocketControlRoomMockupCSS(width, height int) string {
	css := `
:root {
  color-scheme: dark;
  --stage: #090b0d;
  --case: #252c33;
  --case-edge: #56616b;
  --case-shadow: #101419;
  --panel: #171c21;
  --panel-raised: #303943;
  --panel-edge: #596570;
  --well: #080d10;
  --well-edge: #050709;
  --screen: #07191a;
  --screen-ink: #95e8df;
  --ink: #e7e0d2;
  --muted: #9ca5a8;
  --quiet: #687279;
  --cyan: #4fd6cb;
  --cyan-dim: #347c79;
  --amber: #f4c458;
  --amber-dark: #7e5d20;
  --green: #69da79;
  --red: #ff6c58;
  --blue: #4b8ec2;
  --brown: #6b4f38;
  font-family: "Avenir Next", "Segoe UI", ui-sans-serif, system-ui, sans-serif;
  font-size: 16px;
  letter-spacing: 0;
}
* { box-sizing: border-box; }
html, body {
  width: __WIDTH__px;
  height: __HEIGHT__px;
  margin: 0;
  overflow: hidden;
  background: var(--stage);
}
button { font: inherit; letter-spacing: 0; }
.pocket-stage {
  padding: __TOP__px __RIGHT__px __BOTTOM__px __LEFT__px;
  background: var(--stage);
}
.pocket-phone {
  position: relative;
  width: __PHONE_WIDTH__px;
  height: __PHONE_HEIGHT__px;
  padding: 6px;
  overflow: hidden;
  border: 2px solid var(--case-edge);
  border-radius: 30px;
  background: var(--case);
  box-shadow:
    0 24px 56px rgba(0, 0, 0, 0.64),
    inset 0 1px 0 #77818a,
    inset 0 -3px 0 var(--case-shadow);
}
.pocket-console {
  height: 100%;
  overflow: hidden;
  display: flex;
  flex-direction: column;
  border: 1px solid #0b0e11;
  border-radius: 24px;
  background: #11161a;
  color: var(--ink);
  box-shadow: inset 0 0 0 1px #343d45;
}
.case-screw {
  position: absolute;
  z-index: 10;
  width: 8px;
  height: 8px;
  border: 1px solid #0d1115;
  border-radius: 50%;
  background: #68737d;
  box-shadow: inset 1px 1px 0 #98a1a8, inset -1px -1px 0 #303840;
}
.case-screw::after {
  content: "";
  position: absolute;
  top: 3px;
  left: 1px;
  width: 4px;
  height: 1px;
  background: #20262c;
}
.screw-a { top: 11px; left: 11px; }
.screw-b { top: 11px; right: 11px; }
.screw-c { bottom: 11px; left: 11px; }
.screw-d { bottom: 11px; right: 11px; }
.system-strip {
  height: 28px;
  flex: 0 0 28px;
  padding: 0 18px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  border-bottom: 1px solid #313941;
  background: #151b20;
  color: var(--muted);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 10px;
  line-height: 1;
}
.link-readout {
  display: flex;
  align-items: center;
  gap: 6px;
}
.lamp {
  display: inline-block;
  width: 8px;
  height: 8px;
  flex: 0 0 8px;
  border: 1px solid #09100b;
  border-radius: 50%;
  background: var(--green);
  box-shadow: 0 0 7px rgba(105, 218, 121, 0.72), inset 0 1px 0 rgba(255, 255, 255, 0.6);
}
.lamp.amber {
  background: var(--amber);
  box-shadow: 0 0 7px rgba(244, 196, 88, 0.72), inset 0 1px 0 rgba(255, 255, 255, 0.58);
}
.lamp.red {
  background: var(--red);
  box-shadow: 0 0 8px rgba(255, 108, 88, 0.74), inset 0 1px 0 rgba(255, 255, 255, 0.58);
}
.lamp.cyan {
  background: var(--cyan);
  box-shadow: 0 0 7px rgba(79, 214, 203, 0.7), inset 0 1px 0 rgba(255, 255, 255, 0.58);
}
.console-header {
  min-height: 61px;
  flex: 0 0 61px;
  padding: 9px 14px 8px;
  display: grid;
  grid-template-columns: 42px minmax(0, 1fr) 42px;
  align-items: center;
  gap: 9px;
  border-bottom: 2px solid #090c0f;
  background: var(--case);
  box-shadow: inset 0 -1px 0 #46515b, inset 0 1px 0 #4b555f;
}
.console-header.no-back { grid-template-columns: minmax(0, 1fr) 42px; }
.brand-plate {
  min-width: 0;
  padding: 6px 9px;
  border: 1px solid #0b0f12;
  border-radius: 4px;
  background: #1a2025;
  box-shadow: inset 0 1px 2px #090c0f, 0 1px 0 #48535d;
}
.brand-plate .kicker {
  color: var(--cyan);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 9px;
  line-height: 1;
  text-transform: uppercase;
}
.brand-plate h1 {
  margin: 3px 0 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 17px;
  line-height: 1;
  font-weight: 800;
}
.hardware-button {
  width: 42px;
  height: 42px;
  padding: 0;
  display: grid;
  place-items: center;
  border: 1px solid #0b0f12;
  border-radius: 5px;
  background: var(--panel-raised);
  color: var(--ink);
  font-size: 20px;
  line-height: 1;
  box-shadow: inset 0 1px 0 #66717b, inset 0 -3px 0 #171d22, 0 1px 1px #080b0d;
}
.hardware-button.amber { color: #17120a; background: var(--amber); }
.screen-content {
  min-height: 0;
  flex: 1;
  overflow: hidden;
  padding: 9px 10px 8px;
  background: #11161a;
}
.operator-bay {
  min-height: 96px;
  display: grid;
  grid-template-columns: minmax(0, 1fr) 176px;
  overflow: hidden;
  border: 2px solid var(--well-edge);
  border-radius: 5px;
  background: var(--well);
  box-shadow: inset 0 0 0 1px #2c383e, inset 0 5px 10px #020405, 0 1px 0 #46515a;
}
.briefing-readout {
  min-width: 0;
  padding: 10px 0 8px 11px;
}
.instrument-label {
  color: var(--muted);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 9px;
  line-height: 1.1;
  text-transform: uppercase;
}
.briefing-count {
  margin-top: 4px;
  display: flex;
  align-items: baseline;
  gap: 7px;
}
.briefing-count strong {
  color: var(--amber);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 34px;
  line-height: 0.9;
  text-shadow: 0 0 8px rgba(244, 196, 88, 0.36);
}
.briefing-count span {
  color: var(--ink);
  font-size: 11px;
  font-weight: 800;
  line-height: 1.1;
}
.briefing-readout p {
  margin: 7px 0 0;
  color: var(--muted);
  font-size: 11px;
  line-height: 1.25;
}
.station-wrap {
  position: relative;
  min-width: 0;
  align-self: stretch;
  display: flex;
  align-items: flex-end;
  justify-content: center;
  padding-bottom: 9px;
}
.operator-sprite {
  width: 176px;
  height: 75px;
  object-fit: contain;
  image-rendering: pixelated;
  image-rendering: crisp-edges;
}
.station-caption {
  position: absolute;
  right: 7px;
  bottom: 4px;
  display: flex;
  align-items: center;
  gap: 5px;
  color: var(--muted);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 8px;
}
.selector-bank {
  height: 46px;
  margin-top: 8px;
  padding: 3px;
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 4px;
  border: 1px solid #090c0f;
  border-radius: 5px;
  background: var(--case);
  box-shadow: inset 0 1px 0 #4a555f, inset 0 -2px 0 #11161a;
}
.selector-key {
  min-width: 0;
  border: 1px solid #11171b;
  border-radius: 4px;
  background: #353f48;
  color: var(--muted);
  box-shadow: inset 0 1px 0 #69747d, inset 0 -3px 0 #1a2026;
  font-size: 10px;
  line-height: 1.05;
  font-weight: 800;
  text-transform: uppercase;
}
.selector-key strong {
  display: block;
  color: var(--ink);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 15px;
}
.selector-key.selected {
  background: var(--amber);
  color: #4e3711;
  box-shadow: inset 0 1px 0 #ffe1a0, inset 0 -3px 0 var(--amber-dark);
}
.selector-key.selected strong { color: #17120a; }
.category-rail {
  height: 35px;
  margin: 7px 0 0;
  display: flex;
  align-items: stretch;
  gap: 4px;
  overflow: hidden;
}
.category-key {
  min-width: 70px;
  padding: 0 9px;
  display: grid;
  place-items: center;
  border: 1px solid #090c0f;
  border-radius: 3px 3px 0 0;
  background: #20272d;
  color: var(--quiet);
  box-shadow: inset 0 1px 0 #4a555e;
  font-size: 10px;
  font-weight: 800;
  text-transform: uppercase;
}
.category-key.selected {
  border-bottom-color: var(--cyan);
  color: var(--cyan);
  background: #172328;
}
.rack-heading {
  height: 25px;
  padding: 0 5px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  color: var(--muted);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 9px;
  text-transform: uppercase;
}
.rack-list {
  display: grid;
  gap: 5px;
}
.rack-row {
  position: relative;
  min-height: 72px;
  padding: 9px 10px 8px 32px;
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  gap: 9px;
  border: 1px solid #080b0d;
  border-radius: 4px;
  background: var(--panel);
  box-shadow: inset 0 1px 0 #39434b, inset 3px 0 0 var(--cyan-dim), 0 1px 0 #3d474f;
}
.rack-row.attention { box-shadow: inset 0 1px 0 #4e4634, inset 3px 0 0 var(--amber), 0 1px 0 #3d474f; }
.rack-row.ready { box-shadow: inset 0 1px 0 #35453a, inset 3px 0 0 var(--green), 0 1px 0 #3d474f; }
.rack-row.conflict { box-shadow: inset 0 1px 0 #56342f, inset 3px 0 0 var(--red), 0 1px 0 #3d474f; }
.rack-lamp {
  position: absolute;
  top: 13px;
  left: 12px;
}
.rack-name {
  overflow-wrap: anywhere;
  font-size: 14px;
  line-height: 1.12;
  font-weight: 800;
}
.rack-summary {
  margin-top: 4px;
  display: -webkit-box;
  overflow: hidden;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  color: var(--muted);
  font-size: 11px;
  line-height: 1.25;
}
.rack-meta {
  min-width: 61px;
  text-align: right;
}
.rack-time {
  color: var(--muted);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 9px;
}
.metal-tag {
  margin-top: 7px;
  padding: 3px 5px;
  display: inline-block;
  border: 1px solid #0c1013;
  border-radius: 3px;
  background: #313b43;
  color: var(--cyan);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 8px;
  font-weight: 800;
  text-transform: uppercase;
  box-shadow: inset 0 1px 0 #56616b;
}
.metal-tag.amber { color: var(--amber); }
.metal-tag.green { color: var(--green); }
.metal-tag.red { color: var(--red); }
.hardware-nav {
  height: 67px;
  flex: 0 0 67px;
  padding: 7px 14px 11px;
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 6px;
  border-top: 2px solid #080b0d;
  background: var(--case);
  box-shadow: inset 0 1px 0 #4b555e;
}
.nav-key {
  min-width: 0;
  display: grid;
  place-items: center;
  align-content: center;
  gap: 2px;
  border: 1px solid #0c1013;
  border-radius: 4px;
  background: #323b43;
  color: var(--quiet);
  box-shadow: inset 0 1px 0 #65707a, inset 0 -3px 0 #181e23;
  font-size: 9px;
  font-weight: 800;
  text-transform: uppercase;
}
.nav-key .nav-symbol { font-size: 15px; line-height: 1; }
.nav-key.selected {
  color: #16120b;
  background: var(--amber);
  box-shadow: inset 0 1px 0 #ffe19c, inset 0 -3px 0 var(--amber-dark);
}
.project-plate {
  padding: 11px 12px;
  border: 1px solid #080b0d;
  border-radius: 5px;
  background: var(--panel-raised);
  box-shadow: inset 0 1px 0 #68737c, inset 0 -3px 0 #151b20;
}
.project-plate-top {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 10px;
}
.project-plate h2 {
  margin: 2px 0 0;
  overflow-wrap: anywhere;
  font-size: 20px;
  line-height: 1.05;
}
.project-plate p {
  margin: 7px 0 0;
  color: var(--muted);
  font-size: 12px;
  line-height: 1.3;
}
.status-lamps {
  margin-top: 9px;
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}
.status-lamp {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  color: var(--muted);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 9px;
  text-transform: uppercase;
}
.section-plate {
  min-height: 25px;
  margin-top: 8px;
  padding: 0 6px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  color: var(--muted);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 9px;
  text-transform: uppercase;
}
.crt-panel {
  position: relative;
  overflow: hidden;
  border: 3px solid #05080a;
  border-radius: 6px;
  background: var(--screen);
  box-shadow: inset 0 0 0 1px #174142, inset 0 7px 18px #010303, 0 1px 0 #4b555e;
}
.engineer-panel {
  min-height: 145px;
  padding: 11px 12px 10px;
}
.crt-title-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
}
.crt-title {
  color: var(--screen-ink);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 13px;
  font-weight: 800;
}
.crt-status {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  color: var(--green);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 9px;
  text-transform: uppercase;
}
.crt-copy {
  margin: 12px 0 0;
  color: #b5d5d0;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 11px;
  line-height: 1.38;
}
.crt-command-row {
  margin-top: 12px;
  display: grid;
  grid-template-columns: minmax(0, 1fr) 92px;
  gap: 7px;
}
.crt-command {
  min-width: 0;
  min-height: 34px;
  padding: 0 9px;
  display: flex;
  align-items: center;
  overflow: hidden;
  border: 1px solid #255355;
  border-radius: 3px;
  color: var(--cyan);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 9px;
  white-space: nowrap;
}
.crt-action {
  min-height: 34px;
  display: grid;
  place-items: center;
  border: 1px solid #75602d;
  border-radius: 3px;
  background: var(--amber);
  color: #17120a;
  font-size: 10px;
  font-weight: 850;
  text-transform: uppercase;
}
.drawer-stack {
  display: grid;
  gap: 5px;
}
.instrument-drawer {
  min-height: 61px;
  padding: 8px 10px;
  display: grid;
  grid-template-columns: 35px minmax(0, 1fr) auto;
  align-items: center;
  gap: 9px;
  border: 1px solid #080b0d;
  border-radius: 4px;
  background: var(--panel);
  box-shadow: inset 0 1px 0 #3f4951, inset 0 -2px 0 #0e1215;
}
.drawer-gauge {
  width: 35px;
  height: 35px;
  display: grid;
  place-items: center;
  border: 2px solid #080b0d;
  border-radius: 50%;
  background: #20282e;
  color: var(--cyan);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 11px;
  font-weight: 850;
  box-shadow: inset 0 0 0 1px #53606a;
}
.drawer-gauge.amber { color: var(--amber); }
.drawer-gauge.blue { color: #8ec6ed; }
.drawer-label { font-size: 12px; font-weight: 800; }
.drawer-meta {
  margin-top: 3px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--muted);
  font-size: 10px;
}
.drawer-handle {
  width: 28px;
  height: 12px;
  border: 1px solid #0a0e11;
  border-radius: 3px;
  background: #3f4951;
  box-shadow: inset 0 1px 0 #707a83, inset 0 -2px 0 #20262b;
}
.session-strip {
  min-height: 44px;
  padding: 6px 9px;
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: center;
  gap: 8px;
  border: 1px solid #090c0f;
  border-radius: 4px;
  background: var(--panel-raised);
  box-shadow: inset 0 1px 0 #65717b, inset 0 -2px 0 #151b20;
}
.session-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 12px;
  font-weight: 800;
}
.session-meta {
  margin-top: 2px;
  color: var(--muted);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 8px;
}
.sidebar-key {
  min-width: 74px;
  height: 29px;
  display: grid;
  place-items: center;
  border: 1px solid #0b0f12;
  border-radius: 3px;
  background: #343e47;
  color: var(--cyan);
  box-shadow: inset 0 1px 0 #66717a, inset 0 -2px 0 #171d22;
  font-size: 9px;
  font-weight: 800;
  text-transform: uppercase;
}
.transcript-crt {
  height: 320px;
  margin-top: 7px;
  padding: 11px 10px;
}
.transcript-line {
  padding: 0 0 9px;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 10px;
  line-height: 1.36;
}
.transcript-kind {
  margin-bottom: 3px;
  color: var(--cyan);
  font-size: 8px;
  font-weight: 800;
  text-transform: uppercase;
}
.transcript-kind.user { color: var(--amber); }
.transcript-kind.tool { color: #8ec6ed; }
.transcript-text { color: #b5d5d0; }
.tool-cassette {
  min-height: 42px;
  padding: 7px 9px;
  display: grid;
  grid-template-columns: 20px minmax(0, 1fr) auto;
  align-items: center;
  gap: 7px;
  border: 1px solid #24484a;
  border-radius: 3px;
  background: #0b2021;
  color: #88bbb7;
  font-size: 9px;
}
.tool-cassette strong { color: var(--cyan); }
.interlock-panel {
  margin-top: 7px;
  padding: 8px;
  border: 2px solid #3c100c;
  border-radius: 5px;
  background: #2b1512;
  box-shadow: inset 0 1px 0 #71372f, inset 0 -3px 0 #110806, 0 1px 0 #4a555e;
}
.hazard-rail {
  height: 6px;
  display: grid;
  grid-template-columns: repeat(12, 1fr);
  gap: 2px;
}
.hazard-rail i:nth-child(odd) { background: var(--amber); }
.hazard-rail i:nth-child(even) { background: #17120a; }
.interlock-title {
  margin-top: 8px;
  display: flex;
  align-items: center;
  gap: 7px;
  color: var(--red);
  font-size: 12px;
  font-weight: 850;
  text-transform: uppercase;
}
.interlock-panel p {
  margin: 6px 0 8px;
  color: #d7b4ad;
  font-size: 10px;
  line-height: 1.28;
}
.interlock-actions {
  display: grid;
  grid-template-columns: 1fr 1.35fr;
  gap: 7px;
}
.interlock-key {
  min-height: 38px;
  display: grid;
  place-items: center;
  border: 1px solid #080b0d;
  border-radius: 4px;
  background: #39424a;
  color: var(--ink);
  box-shadow: inset 0 1px 0 #68737c, inset 0 -3px 0 #171d22;
  font-size: 10px;
  font-weight: 850;
  text-transform: uppercase;
}
.interlock-key.allow {
  background: var(--red);
  color: #1c0906;
  box-shadow: inset 0 1px 0 #ffb1a5, inset 0 -3px 0 #7b291f;
}
.composer-deck {
  min-height: 56px;
  flex: 0 0 56px;
  padding: 7px 10px 10px;
  display: grid;
  grid-template-columns: minmax(0, 1fr) 44px;
  gap: 7px;
  border-top: 2px solid #080b0d;
  background: var(--case);
  box-shadow: inset 0 1px 0 #4b555e;
}
.composer-screen {
  min-width: 0;
  padding: 0 10px;
  display: flex;
  align-items: center;
  overflow: hidden;
  border: 2px solid #05080a;
  border-radius: 4px;
  background: var(--screen);
  color: #78a8a4;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 10px;
  white-space: nowrap;
  box-shadow: inset 0 0 0 1px #214344;
}
.send-key {
  display: grid;
  place-items: center;
  border: 1px solid #0b0f12;
  border-radius: 4px;
  background: var(--amber);
  color: #181208;
  box-shadow: inset 0 1px 0 #ffe1a0, inset 0 -3px 0 var(--amber-dark);
  font-size: 20px;
  font-weight: 850;
}
`
	return strings.NewReplacer(
		"__WIDTH__", fmt.Sprintf("%d", width),
		"__HEIGHT__", fmt.Sprintf("%d", height),
		"__TOP__", fmt.Sprintf("%d", mobileMockupCropTop),
		"__RIGHT__", fmt.Sprintf("%d", mobileMockupCropRight),
		"__BOTTOM__", fmt.Sprintf("%d", mobileMockupCropBottom),
		"__LEFT__", fmt.Sprintf("%d", mobileMockupCropLeft),
		"__PHONE_WIDTH__", fmt.Sprintf("%d", mobileMockupPhoneWidth),
		"__PHONE_HEIGHT__", fmt.Sprintf("%d", mobileMockupPhoneHeight),
	).Replace(css)
}

func pocketControlRoomScreenBody(screen string) string {
	switch screen {
	case "project":
		return pocketProjectConsoleScreen()
	case "session":
		return pocketSessionInterlockScreen()
	default:
		return pocketSwitchboardScreen()
	}
}

func pocketSystemStrip(mode string) string {
	return `<div class="system-strip"><span>11:22</span><span class="link-readout"><i class="lamp"></i>HOST LCR-DESK / ` + mode + `</span></div>`
}

func pocketConsoleHeader(kicker, title, action, actionLabel string, back bool) string {
	classes := "console-header"
	if !back {
		classes += " no-back"
	}
	var out strings.Builder
	out.WriteString(`<header class="` + classes + `">`)
	if back {
		out.WriteString(`<button class="hardware-button" type="button" aria-label="Back" title="Back">&#8592;</button>`)
	}
	out.WriteString(`<div class="brand-plate"><div class="kicker">` + kicker + `</div><h1>` + title + `</h1></div>`)
	out.WriteString(`<button class="hardware-button" type="button" aria-label="` + actionLabel + `" title="` + actionLabel + `">` + action + `</button>`)
	out.WriteString(`</header>`)
	return out.String()
}

func pocketOperatorBay(mode string) string {
	state := pixelart.OperatorStationState{Width: 36, Pose: pixelart.OperatorInspect, Phase: 2, WalkCycle: false}
	count := "3"
	label := "NEED YOU"
	copy := "One decision is blocking progress. Two sessions can keep working."
	caption := "INSPECTING QUEUE"
	lampClass := "amber"
	switch mode {
	case "project":
		state.Pose = pixelart.OperatorTypeA
		state.Phase = 12
		count = "1"
		label = "ENGINEER LIVE"
		copy = "Codex is active and the latest transcript is streaming."
		caption = "AT CONSOLE"
		lampClass = "cyan"
	case "session":
		state.Pose = pixelart.OperatorTypeB
		state.Phase = 17
		count = "!"
		label = "APPROVAL"
		copy = "A command is paused at the safety interlock."
		caption = "WAITING ON YOU"
		lampClass = "red"
	}

	return `<section class="operator-bay">
<div class="briefing-readout"><div class="instrument-label">Operator desk / live briefing</div><div class="briefing-count"><strong>` + count + `</strong><span>` + label + `</span></div><p>` + copy + `</p></div>
<div class="station-wrap"><img class="operator-sprite" src="` + pocketOperatorStationDataURL(state) + `" alt="Pixel operator at the control station"><div class="station-caption"><i class="lamp ` + lampClass + `"></i>` + caption + `</div></div>
</section>`
}

func pocketHardwareNav(active string) string {
	items := []struct {
		key    string
		label  string
		symbol string
	}{
		{key: "work", label: "Work", symbol: "&#8962;"},
		{key: "sessions", label: "Sessions", symbol: "&#9635;"},
		{key: "boss", label: "Boss", symbol: "&#9673;"},
		{key: "system", label: "System", symbol: "&#9881;"},
	}
	var out strings.Builder
	out.WriteString(`<nav class="hardware-nav" aria-label="Primary">`)
	for _, item := range items {
		classes := "nav-key"
		if item.key == active {
			classes += " selected"
		}
		out.WriteString(`<button class="` + classes + `" type="button"><span class="nav-symbol">` + item.symbol + `</span><span>` + item.label + `</span></button>`)
	}
	out.WriteString(`</nav>`)
	return out.String()
}

func pocketSwitchboardScreen() string {
	return pocketSystemStrip("READ ONLY") +
		pocketConsoleHeader("Portable command system", "Little Control Room", "&#8635;", "Refresh switchboard", false) +
		`<div class="screen-content">` +
		pocketOperatorBay("switchboard") +
		`<div class="selector-bank" role="group" aria-label="Project state">
<button class="selector-key selected" type="button"><strong>3</strong>Need</button>
<button class="selector-key" type="button"><strong>6</strong>Active</button>
<button class="selector-key" type="button"><strong>21</strong>All</button>
</div>
<div class="category-rail" role="tablist"><button class="category-key selected" type="button">Main</button><button class="category-key" type="button">Clients</button><button class="category-key" type="button">Labs</button><button class="category-key" type="button">Archive</button></div>
<div class="rack-heading"><span>Attention switchboard</span><span>Live feed</span></div>
<div class="rack-list">
<article class="rack-row attention"><i class="lamp amber rack-lamp"></i><div><div class="rack-name">LittleControlRoom</div><div class="rack-summary">Choose the visual language for the mobile control console.</div></div><div class="rack-meta"><div class="rack-time">NOW</div><span class="metal-tag amber">Decision</span></div></article>
<article class="rack-row"><i class="lamp cyan rack-lamp"></i><div><div class="rack-name">assistant-lab</div><div class="rack-summary">Codex is active; validation has been quiet for 18 minutes.</div></div><div class="rack-meta"><div class="rack-time">18M</div><span class="metal-tag">Running</span></div></article>
<article class="rack-row ready"><i class="lamp rack-lamp"></i><div><div class="rack-name">billing-tool</div><div class="rack-summary">Patch is complete and the commit preview is waiting.</div></div><div class="rack-meta"><div class="rack-time">32M</div><span class="metal-tag green">Ready</span></div></article>
<article class="rack-row attention"><i class="lamp amber rack-lamp"></i><div><div class="rack-name">render-bench</div><div class="rack-summary">Golden captures are ready; the engineer needs approval to replace them.</div></div><div class="rack-meta"><div class="rack-time">41M</div><span class="metal-tag amber">Approval</span></div></article>
<article class="rack-row conflict"><i class="lamp red rack-lamp"></i><div><div class="rack-name">client-portal</div><div class="rack-summary">Release worktree has unresolved files after the latest merge.</div></div><div class="rack-meta"><div class="rack-time">1H</div><span class="metal-tag red">Conflict</span></div></article>
</div></div>` +
		pocketHardwareNav("work")
}

func pocketProjectConsoleScreen() string {
	return pocketSystemStrip("PROJECT") +
		pocketConsoleHeader("Project console", "LittleControlRoom", "&#8942;", "Project actions", true) +
		`<div class="screen-content">
<section class="project-plate"><div class="project-plate-top"><div><div class="instrument-label">Main rack / mobile-interface</div><h2>LittleControlRoom</h2></div><span class="metal-tag amber">Needs review</span></div><p>The handheld interface is ready for a visual direction and the engineer can continue once it is chosen.</p><div class="status-lamps"><span class="status-lamp"><i class="lamp cyan"></i>Codex live</span><span class="status-lamp"><i class="lamp amber"></i>Dirty</span><span class="status-lamp"><i class="lamp"></i>Runtime quiet</span></div></section>
<div class="section-plate"><span>Operator status</span><span>Updated now</span></div>` +
		pocketOperatorBay("project") +
		`<div class="section-plate"><span>Active engineer</span><span>Session 019f1c4d</span></div>
<section class="crt-panel engineer-panel"><div class="crt-title-row"><div class="crt-title">CODEX / MOBILE INTERFACE</div><div class="crt-status"><i class="lamp"></i>WORKING</div></div><p class="crt-copy">Building three Pocket Control Room studies from the shared pixel-art system...</p><div class="crt-command-row"><div class="crt-command">latest: render phone mockups</div><button class="crt-action" type="button">Open session</button></div></section>
<div class="section-plate"><span>Project instruments</span><span>3 drawers</span></div>
<div class="drawer-stack">
<div class="instrument-drawer"><div class="drawer-gauge amber">G</div><div><div class="drawer-label">Repository</div><div class="drawer-meta">mobile-interface / 4 local changes</div></div><div class="drawer-handle"></div></div>
<div class="instrument-drawer"><div class="drawer-gauge">T</div><div><div class="drawer-label">TODO register</div><div class="drawer-meta">2 open items, project scoped</div></div><div class="drawer-handle"></div></div>
<div class="instrument-drawer"><div class="drawer-gauge blue">R</div><div><div class="drawer-label">Runtime</div><div class="drawer-meta">No managed process running</div></div><div class="drawer-handle"></div></div>
</div></div>` +
		pocketHardwareNav("work")
}

func pocketSessionInterlockScreen() string {
	return pocketSystemStrip("WRITE ARMED") +
		pocketConsoleHeader("Engineer channel", "Codex / LittleControlRoom", "&#8942;", "Session controls", true) +
		`<div class="screen-content">
<div class="session-strip"><div><div class="session-name">mobile-interface / 019f1c4d</div><div class="session-meta">gpt-5.4 / high reasoning / 4m 18s</div></div><button class="sidebar-key" type="button">Instruments</button></div>
<section class="crt-panel transcript-crt" aria-label="Engineer transcript">
<div class="transcript-line"><div class="transcript-kind user">You</div><div class="transcript-text">Give me mockups that feel like a real Little Control Room.</div></div>
<div class="transcript-line"><div class="transcript-kind">Engineer</div><div class="transcript-text">Using the operator, signal lamps, inset monitors, and physical control banks as the shared visual system.</div></div>
<div class="transcript-line"><div class="transcript-kind tool">Tool activity</div><div class="tool-cassette"><span>&#9635;</span><span><strong>apply_patch</strong><br>mobile_console_mockups.go</span><span>DONE</span></div></div>
<div class="transcript-line"><div class="transcript-kind">Engineer</div><div class="transcript-text">The three layouts are ready. Running the renderer now.</div></div>
</section>
<div class="interlock-panel"><div class="hazard-rail"><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i></div><div class="interlock-title"><i class="lamp red"></i>Approval interlock</div><p>Allow the engineer to run the mockup renderer and write PNG captures under /tmp/lcroom-mockups?</p><div class="interlock-actions"><button class="interlock-key" type="button">Deny</button><button class="interlock-key allow" type="button">Allow once</button></div></div>
<div class="section-plate"><span>Operator channel</span><span>Waiting on you</span></div>` +
		pocketOperatorBay("session") +
		`</div><div class="composer-deck"><div class="composer-screen">Message engineer...</div><button class="send-key" type="button" aria-label="Send">&#8593;</button></div>`
}

func pocketOperatorStationDataURL(state pixelart.OperatorStationState) string {
	width := max(pixelart.OperatorStationWidth, state.Width)
	canvas := image.NewNRGBA(image.Rect(0, 0, width, pixelart.OperatorStationHeight))
	pixelart.DrawOperatorStation(func(x, y int, pixel pixelart.Color) {
		if x < 0 || y < 0 || x >= width || y >= pixelart.OperatorStationHeight {
			return
		}
		canvas.SetNRGBA(x, y, color.NRGBA{R: pixel.R, G: pixel.G, B: pixel.B, A: 255})
	}, state)

	var encoded bytes.Buffer
	if err := png.Encode(&encoded, canvas); err != nil {
		return ""
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(encoded.Bytes())
}

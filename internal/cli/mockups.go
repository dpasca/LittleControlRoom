package cli

import (
	"context"
	"errors"
	"fmt"
	"html"
	"image/png"
	"os"
	"path/filepath"
	"strings"

	"lcroom/internal/config"
	"lcroom/internal/tui"
)

func runMockups(ctx context.Context, rawArgs []string) int {
	args, screenshotConfigPath, err := stripPathFlagArg(rawArgs, "--screenshot-config")
	if err != nil {
		fmt.Fprintf(os.Stderr, "mockups flag error: %v\n", err)
		return 2
	}
	args, outputOverride, err := stripPathFlagArg(args, "--output-dir")
	if err != nil {
		fmt.Fprintf(os.Stderr, "mockups flag error: %v\n", err)
		return 2
	}
	if len(args) > 0 {
		fmt.Fprintf(os.Stderr, "mockups flag error: unknown argument %q\n", args[0])
		return 2
	}

	cfg := config.DefaultScreenshotConfig()
	cfg.Path = "mockups"
	cfg.OutputDir = filepath.Join("docs", "mockups")
	if strings.TrimSpace(screenshotConfigPath) != "" {
		cfg, err = config.ParseScreenshotConfig(screenshotConfigPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "mockup config error: %v\n", err)
			return 2
		}
	}
	if strings.TrimSpace(outputOverride) != "" {
		outputDir := strings.TrimSpace(outputOverride)
		if !filepath.IsAbs(outputDir) {
			cwd, cwdErr := os.Getwd()
			if cwdErr != nil {
				fmt.Fprintf(os.Stderr, "resolve output dir: %v\n", cwdErr)
				return 1
			}
			outputDir = filepath.Join(cwd, outputDir)
		}
		cfg.OutputDir = filepath.Clean(outputDir)
	}

	report := tui.GenerateMockups(cfg)
	if err := os.MkdirAll(cfg.OutputDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "create output dir: %v\n", err)
		return 1
	}

	browserPath, browserErr := resolveScreenshotBrowser(cfg.BrowserPath)
	if browserErr != nil {
		fmt.Fprintf(os.Stderr, "warning: %v\n", browserErr)
		fmt.Fprintln(os.Stderr, "warning: writing HTML and ANSI mockups without PNG captures")
	}

	fmt.Printf("mockups written to %s\n", cfg.OutputDir)
	indexItems := make([]mockupIndexItem, 0, len(report.Assets))
	for _, asset := range report.Assets {
		basePath := filepath.Join(cfg.OutputDir, asset.Name)
		htmlPath := basePath + ".html"
		ansiPath := basePath + ".ansi"
		if err := os.WriteFile(htmlPath, []byte(asset.HTML), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "write %s: %v\n", htmlPath, err)
			return 1
		}
		if err := os.WriteFile(ansiPath, []byte(asset.ANSI), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "write %s: %v\n", ansiPath, err)
			return 1
		}
		fmt.Printf("  - %s\n", htmlPath)
		fmt.Printf("  - %s\n", ansiPath)
		if browserErr != nil {
			continue
		}
		pngPath := basePath + ".png"
		if err := captureBrowserScreenshot(ctx, browserPath, htmlPath, pngPath, asset.Width, asset.Height, cfg.CaptureScale); err != nil {
			if errors.Is(err, context.Canceled) {
				fmt.Fprintf(os.Stderr, "render %s: %v\n", pngPath, err)
				return 1
			}
			fmt.Fprintf(os.Stderr, "render %s: %v\n", pngPath, err)
			fmt.Fprintf(os.Stderr, "debug html left at %s\n", htmlPath)
			return 1
		}
		fmt.Printf("  - %s\n", pngPath)
		indexItems = append(indexItems, mockupIndexItem{
			Name:  asset.Name,
			Title: asset.Title,
			PNG:   filepath.Base(pngPath),
			HTML:  filepath.Base(htmlPath),
			ANSI:  filepath.Base(ansiPath),
		})
	}

	if len(indexItems) > 0 {
		for _, asset := range tui.GenerateMockupRasterAssets(cfg) {
			pngPath := filepath.Join(cfg.OutputDir, asset.Name+".png")
			file, err := os.Create(pngPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "write %s: %v\n", pngPath, err)
				return 1
			}
			encodeErr := png.Encode(file, asset.Image)
			closeErr := file.Close()
			if encodeErr != nil {
				fmt.Fprintf(os.Stderr, "write %s: %v\n", pngPath, encodeErr)
				return 1
			}
			if closeErr != nil {
				fmt.Fprintf(os.Stderr, "close %s: %v\n", pngPath, closeErr)
				return 1
			}
			fmt.Printf("  - %s\n", pngPath)
			indexItems = append(indexItems, mockupIndexItem{
				Name:  asset.Name,
				Title: asset.Title,
				PNG:   filepath.Base(pngPath),
			})
		}

		indexPath := filepath.Join(cfg.OutputDir, "index.html")
		if err := os.WriteFile(indexPath, []byte(renderMockupIndex(indexItems)), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "write %s: %v\n", indexPath, err)
			return 1
		}
		fmt.Printf("  - %s\n", indexPath)
	}
	return 0
}

type mockupIndexItem struct {
	Name  string
	Title string
	PNG   string
	HTML  string
	ANSI  string
}

func renderMockupIndex(items []mockupIndexItem) string {
	var b strings.Builder
	b.WriteString(`<!doctype html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Little Control Room Mockups</title>
<style>
:root {
  color-scheme: dark;
  --bg: #09110d;
  --panel: #14251d;
  --ink: #f4ead1;
  --muted: #9aab92;
  --accent: #f0c15b;
  --edge: #3a2a1e;
}
* { box-sizing: border-box; }
body {
  margin: 0;
  padding: 28px;
  background: radial-gradient(circle at top left, #223b2c 0, var(--bg) 42rem);
  color: var(--ink);
  font: 15px/1.5 ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
}
h1 { margin: 0 0 4px; font-size: 24px; color: var(--accent); }
p { margin: 0 0 24px; color: var(--muted); }
.grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(340px, 1fr));
  gap: 18px;
}
.card {
  border: 1px solid var(--edge);
  border-radius: 14px;
  background: color-mix(in srgb, var(--panel) 88%, black);
  box-shadow: 0 16px 40px rgb(0 0 0 / 0.32);
  overflow: hidden;
}
.card header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 12px 14px;
  border-bottom: 1px solid var(--edge);
}
.card h2 { margin: 0; font-size: 14px; color: var(--accent); }
.links { display: flex; gap: 10px; font-size: 12px; }
a { color: var(--ink); }
img {
  display: block;
  width: 100%;
  height: auto;
  image-rendering: auto;
  background: #050806;
}
</style>
</head>
<body>
<h1>Little Control Room Mockups</h1>
<p>Rendering-only previews generated by <code>lcroom mockups</code>. Use PNGs for quick review, HTML for terminal-color inspection, ANSI for raw terminal output.</p>
<main class="grid">
`)
	for _, item := range items {
		title := html.EscapeString(item.Title)
		b.WriteString(`<section class="card">
<header>
<h2>`)
		b.WriteString(title)
		b.WriteString(`</h2>
<nav class="links"><a href="`)
		b.WriteString(html.EscapeString(item.PNG))
		b.WriteString(`">png</a>`)
		if item.HTML != "" {
			b.WriteString(`<a href="`)
			b.WriteString(html.EscapeString(item.HTML))
			b.WriteString(`">html</a>`)
		}
		if item.ANSI != "" {
			b.WriteString(`<a href="`)
			b.WriteString(html.EscapeString(item.ANSI))
			b.WriteString(`">ansi</a>`)
		}
		b.WriteString(`</nav>
</header>
<a href="`)
		b.WriteString(html.EscapeString(item.PNG))
		b.WriteString(`"><img src="`)
		b.WriteString(html.EscapeString(item.PNG))
		b.WriteString(`" alt="`)
		b.WriteString(title)
		b.WriteString(`"></a>
</section>
`)
	}
	b.WriteString(`</main>
</body>
</html>
`)
	return b.String()
}

package projectrun

import (
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var (
	runtimeEnvPortPattern  = regexp.MustCompile(`(?i)(?:^|[\s;&|(])(?:PORT|[A-Z0-9_]+_PORT)\s*=\s*["']?([0-9]{2,5})`)
	runtimeFlagPortPattern = regexp.MustCompile(`(?i)(?:^|[\s;&|(])(?:--(?:host-)?port|-p)(?:=|\s+)["']?([0-9]{2,5})`)
	runtimeHostPortPattern = regexp.MustCompile(`(?i)(?:localhost|127\.0\.0\.1|0\.0\.0\.0|\[::1\]):([0-9]{2,5})`)
)

// ExpectedPorts returns the ports a project runtime appears likely to serve.
func ExpectedPorts(command string, announcedURLs []string, observedPorts []int) []int {
	seen := map[int]struct{}{}
	add := func(port int) {
		if port <= 0 || port > 65535 {
			return
		}
		seen[port] = struct{}{}
	}
	for _, port := range observedPorts {
		add(port)
	}
	for _, rawURL := range announcedURLs {
		if port, ok := portFromRuntimeURL(rawURL); ok {
			add(port)
		}
	}

	command = strings.TrimSpace(command)
	if command != "" {
		for _, rawURL := range announcedURLPattern.FindAllString(command, -1) {
			if port, ok := portFromRuntimeURL(rawURL); ok {
				add(port)
			}
		}
		for _, match := range runtimeEnvPortPattern.FindAllStringSubmatch(command, -1) {
			if len(match) > 1 {
				addRuntimePortString(match[1], add)
			}
		}
		for _, match := range runtimeFlagPortPattern.FindAllStringSubmatch(command, -1) {
			if len(match) > 1 {
				addRuntimePortString(match[1], add)
			}
		}
		for _, match := range runtimeHostPortPattern.FindAllStringSubmatch(command, -1) {
			if len(match) > 1 {
				addRuntimePortString(match[1], add)
			}
		}
	}

	out := make([]int, 0, len(seen))
	for port := range seen {
		out = append(out, port)
	}
	sort.Ints(out)
	return out
}

func addRuntimePortString(value string, add func(int)) {
	port, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return
	}
	add(port)
}

func portFromRuntimeURL(rawURL string) (int, bool) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed == nil {
		return 0, false
	}
	portText := parsed.Port()
	if portText == "" {
		return 0, false
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port <= 0 || port > 65535 {
		return 0, false
	}
	return port, true
}

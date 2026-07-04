package tui

func dialogListWindow(selected, total, visible int) (int, int) {
	if total <= 0 {
		return 0, 0
	}
	if visible <= 0 {
		visible = 1
	}
	if visible > total {
		visible = total
	}
	if selected < 0 {
		selected = 0
	}
	if selected >= total {
		selected = total - 1
	}
	start := selected - visible/2
	maxStart := total - visible
	if start < 0 {
		start = 0
	}
	if start > maxStart {
		start = maxStart
	}
	return start, start + visible
}

package tui

import "github.com/atotto/clipboard"

var clipboardTextReader = clipboard.ReadAll
var clipboardTextWriter = clipboard.WriteAll

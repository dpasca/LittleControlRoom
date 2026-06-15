package tui

import (
	"reflect"
	"unicode"
	"unsafe"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
)

func codexWordBackwardKey(msg tea.KeyMsg) bool {
	switch msg.String() {
	case "alt+b", "alt+left":
		return true
	default:
		return false
	}
}

// bubbles/textarea v1.0.0 can spin forever on WordBackward when the cursor is
// at a position with no reachable non-space rune to the left. Guard those key
// paths locally until the upstream fix is available.
func codexTextareaWordBackwardWouldStall(input *textarea.Model) bool {
	rows, row, col, ok := codexTextareaState(input)
	if !ok || len(rows) == 0 {
		return false
	}
	row = max(0, min(row, len(rows)-1))
	col = max(0, min(col, len(rows[row])))

	for {
		prevRow, prevCol := row, col
		row, col = codexTextareaCharacterLeft(rows, row, col, true)
		if col < len(rows[row]) && !unicode.IsSpace(rows[row][col]) {
			return false
		}
		if row == prevRow && col == prevCol {
			return true
		}
	}
}

func codexShouldIgnoreTextareaWordBackward(input *textarea.Model, msg tea.KeyMsg) bool {
	return codexWordBackwardKey(msg) && codexTextareaWordBackwardWouldStall(input)
}

func codexShouldIgnoreStraySGRMousePacket(msg tea.KeyMsg) bool {
	if msg.Type != tea.KeyRunes || msg.Alt || msg.Paste || len(msg.Runes) == 0 {
		return false
	}
	return codexParseSGRMousePackets(msg.Runes)
}

func codexParseSGRMousePackets(runes []rune) bool {
	if len(runes) == 0 {
		return false
	}
	i := 0
	for i < len(runes) {
		if runes[i] == '\x1b' {
			if i+2 >= len(runes) || runes[i+1] != '[' {
				return false
			}
			i += 2
		}
		if i >= len(runes) || runes[i] != '<' {
			return false
		}
		i++
		for field := 0; field < 3; field++ {
			start := i
			for i < len(runes) && runes[i] >= '0' && runes[i] <= '9' {
				i++
			}
			if i == start {
				return false
			}
			if field < 2 {
				if i >= len(runes) || runes[i] != ';' {
					return false
				}
				i++
			}
		}
		if i >= len(runes) || (runes[i] != 'M' && runes[i] != 'm') {
			return false
		}
		i++
	}
	return true
}

func codexTextareaCharacterLeft(rows [][]rune, row, col int, insideLine bool) (int, int) {
	if col == 0 && row != 0 {
		row--
		col = len(rows[row])
		if !insideLine {
			return row, col
		}
	}
	if col > 0 {
		col--
	}
	return row, col
}

func codexTextareaState(input *textarea.Model) ([][]rune, int, int, bool) {
	if input == nil {
		return nil, 0, 0, false
	}

	valueField, ok := codexTextareaField(input, "value")
	if !ok {
		return nil, 0, 0, false
	}
	rowField, ok := codexTextareaField(input, "row")
	if !ok {
		return nil, 0, 0, false
	}
	colField, ok := codexTextareaField(input, "col")
	if !ok {
		return nil, 0, 0, false
	}

	rows, ok := valueField.Interface().([][]rune)
	if !ok {
		return nil, 0, 0, false
	}
	return rows, int(rowField.Int()), int(colField.Int()), true
}

func codexTextareaField(input *textarea.Model, name string) (reflect.Value, bool) {
	value := reflect.ValueOf(input)
	if value.Kind() != reflect.Ptr || value.IsNil() {
		return reflect.Value{}, false
	}
	field := value.Elem().FieldByName(name)
	if !field.IsValid() || !field.CanAddr() {
		return reflect.Value{}, false
	}
	return reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem(), true
}

package codexslash

import "testing"

func TestFastCommands(t *testing.T) {
	for _, tt := range []struct{ input, mode string }{
		{"/fast", "status"}, {"/fast status", "status"}, {"/fast on", "on"}, {"/fast off", "off"}, {"/fast OFF", "off"},
	} {
		inv, err := Parse(tt.input)
		if err != nil || inv.Kind != KindFast || inv.FastMode != tt.mode {
			t.Fatalf("%s => %#v, %v", tt.input, inv, err)
		}
	}
	for _, input := range []string{"/fast toggle", "/fast on now", "/fast off extra"} {
		if _, err := Parse(input); err == nil {
			t.Fatalf("accepted %s", input)
		}
	}
	choices := Suggestions("/fast ")
	for _, want := range []string{"/fast on", "/fast off", "/fast status"} {
		found := false
		for _, choice := range choices {
			found = found || choice.Insert == want
		}
		if !found {
			t.Fatalf("missing suggestion %s", want)
		}
	}
}

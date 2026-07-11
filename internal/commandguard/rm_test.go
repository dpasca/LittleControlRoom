package commandguard

import "testing"

func TestContainsDirectRM(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    bool
	}{
		{name: "forced recursive", command: `rm -rf /Users/example`, want: true},
		{name: "reversed flags", command: `rm -fr build`, want: true},
		{name: "absolute executable", command: `/bin/rm -rf build`, want: true},
		{name: "command chain", command: `git status && rm -rf build`, want: true},
		{name: "command substitution", command: `echo "$(rm -rf build)"`, want: true},
		{name: "sudo options", command: `sudo -n -u root rm -rf build`, want: true},
		{name: "env assignments", command: `env MODE=clean rm -rf build`, want: true},
		{name: "dynamic target", command: `rm -rf "$TARGET"`, want: true},
		{name: "nested shell", command: `bash -lc '/bin/rm -rf "$TARGET"'`, want: true},
		{name: "wrapped nested shell", command: `sudo -n env MODE=clean zsh -c 'rm -fr build'`, want: true},
		{name: "quoted example", command: `printf '%s\n' 'rm -rf /'`, want: false},
		{name: "search text", command: `rg 'rm -rf' README.md`, want: false},
		{name: "rmdir remains available", command: `rmdir empty-dir`, want: false},
		{name: "dynamic command is not guessed", command: `$DELETE -rf build`, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ContainsDirectRM(tt.command); got != tt.want {
				t.Fatalf("ContainsDirectRM(%q) = %v, want %v", tt.command, got, tt.want)
			}
		})
	}
}

func TestArgvInvokesRM(t *testing.T) {
	tests := []struct {
		argv []string
		want bool
	}{
		{argv: []string{"rm", "file"}, want: true},
		{argv: []string{"/usr/bin/rm", "-rf", "build"}, want: true},
		{argv: []string{"command", "--", "rm", "file"}, want: true},
		{argv: []string{"exec", "-a", "cleanup", "rm", "file"}, want: true},
		{argv: []string{"env", "MODE=clean", "rm", "file"}, want: true},
		{argv: []string{"sudo", "-u", "root", "rm", "file"}, want: true},
		{argv: []string{"echo", "rm"}, want: false},
	}
	for _, tt := range tests {
		if got := ArgvInvokesRM(tt.argv); got != tt.want {
			t.Fatalf("ArgvInvokesRM(%q) = %v, want %v", tt.argv, got, tt.want)
		}
	}
}

func TestArgvContainsDirectRMInShellScript(t *testing.T) {
	for _, argv := range [][]string{
		{"sh", "-c", "rm -rf build"},
		{"/bin/zsh", "-lc", "/bin/rm -fr build"},
		{"sudo", "-n", "bash", "-c", "rm -rf build"},
	} {
		if !ArgvContainsDirectRM(argv) {
			t.Fatalf("ArgvContainsDirectRM(%q) = false, want true", argv)
		}
	}
	if ArgvContainsDirectRM([]string{"sh", "-c", "printf '%s\\n' 'rm -rf /'"}) {
		t.Fatal("quoted rm example should not be classified as a command")
	}
}

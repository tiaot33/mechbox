package main

import "testing"

func TestExplorerSelect(t *testing.T) {
	line, err := explorerSelect(`\\wsl.localhost\Ubuntu\home\me\proj\.claude\skills\my draft`)
	if err != nil || line != `explorer.exe /select,"\\wsl.localhost\Ubuntu\home\me\proj\.claude\skills\my draft"` {
		t.Fatalf("select = %q %v", line, err)
	}
	if line, err := explorerSelect(`\\wsl.localhost\Ubuntu\home\me\proj\.claude\skills\a",\\203.0.113.5\s,"`); err == nil {
		t.Fatalf("a double quote in the folder name ends up on explorer's command line: %q", line)
	}
}

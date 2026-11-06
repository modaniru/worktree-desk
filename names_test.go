package main

import (
	"path/filepath"
	"testing"
)

func TestSuggestPathWithName(t *testing.T) {
	app := &App{cfg: Config{Root: "/p"}}
	if got := app.SuggestPath("/p/frontend", "feat/x", "Всякая всячина"); got != filepath.Join("/p", ".worktrees", "frontend", "vsyakaya-vsyachina") {
		t.Fatal(got)
	}
}

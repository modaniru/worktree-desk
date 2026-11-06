package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestMultipleRoots(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // configPath → временная папка
	base := t.TempDir()
	p1, p2 := filepath.Join(base, "one"), filepath.Join(base, "two")
	for _, r := range []string{filepath.Join(p1, "api"), filepath.Join(p2, "api"), filepath.Join(p2, "web")} {
		if out, err := exec.Command("git", "init", "-q", "-b", "master", r).CombinedOutput(); err != nil {
			t.Fatalf("%v %s", err, out)
		}
	}

	app := &App{}
	if err := app.addRoot(p1); err != nil {
		t.Fatal(err)
	}
	if err := app.addRoot(p2); err != nil {
		t.Fatal(err)
	}
	if err := app.addRoot(p1); err == nil {
		t.Fatal("дубликат проекта добавился")
	}
	if err := app.addRoot(filepath.Join(p2, "web")); err == nil {
		t.Fatal("вложенная папка добавилась как отдельный проект")
	}

	repos, err := app.ListRepos()
	if err != nil || len(repos) != 3 {
		t.Fatalf("repos: %+v %v", repos, err)
	}
	if repos[0].Project != p1 || repos[1].Project != p2 || repos[1].Name != "api" {
		t.Fatalf("project/name: %+v", repos)
	}
	// путь нового ворктри — от корня своего проекта
	if got := app.SuggestPath(filepath.Join(p2, "web"), "feat/x", ""); got != filepath.Join(p2, ".worktrees", "web", "feat-x") {
		t.Fatalf("suggest: %s", got)
	}

	if err := app.RemoveRoot(p2); err != nil || len(app.GetRoots()) != 1 {
		t.Fatalf("remove: %v %v", app.GetRoots(), err)
	}
}

func TestConfigMigration(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	p := configPath()
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	_ = os.WriteFile(p, []byte(`{"root":"/x/old","names":{"/x/old/a":"A"}}`), 0o644)

	c := loadConfig()
	if len(c.Roots) != 1 || c.Roots[0] != "/x/old" || c.Root != "" || c.Names["/x/old/a"] != "A" {
		t.Fatalf("migration: %+v", c)
	}
	b, _ := json.Marshal(c)
	if json.Unmarshal(b, &map[string]any{}) != nil || string(b) == "" {
		t.Fatal("marshal")
	}
}

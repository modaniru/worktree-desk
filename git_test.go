package main

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestWorktreeLifecycle(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "svc", "a")
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run(root, "init", "-q", "-b", "master", repo)
	run(repo, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init")
	run(repo, "branch", "feature/x")

	repos, err := discoverRepos(root, scanDepth)
	if err != nil || len(repos) != 1 || repos[0] != repo {
		t.Fatalf("discover: %v %v", repos, err)
	}

	app := &App{cfg: Config{Roots: []string{root}}}
	p1 := app.SuggestPath(repo, "feat/new", "")
	if p1 != filepath.Join(root, ".worktrees", "svc-a", "feat-new") {
		t.Fatalf("suggest: %s", p1)
	}
	if err := addWorktree(AddRequest{RepoPath: repo, Mode: "new", Branch: "feat/new", Base: "master", Path: p1}); err != nil {
		t.Fatal(err)
	}
	p2 := app.SuggestPath(repo, "feature/x", "")
	if err := addWorktree(AddRequest{RepoPath: repo, Mode: "existing", Branch: "feature/x", Path: p2}); err != nil {
		t.Fatal(err)
	}
	// ворктри внутри .worktrees не должны находиться как отдельные репо
	if repos, _ := discoverRepos(root, scanDepth); len(repos) != 1 {
		t.Fatalf("discover after add: %v", repos)
	}

	wts, err := listWorktrees(repo)
	if err != nil || len(wts) != 3 || !wts[0].Main || wts[1].Branch != "feat/new" || wts[1].Dirty != 0 {
		t.Fatalf("list: %+v %v", wts, err)
	}
	brs, _ := listBranches(repo)
	for _, b := range brs {
		if !b.InUse {
			t.Fatalf("branch %s should be in use", b.Name)
		}
	}

	if err := removeWorktree(RemoveRequest{RepoPath: repo, Path: wts[0].Path}); err == nil {
		t.Fatal("main worktree removed")
	}
	if err := removeWorktree(RemoveRequest{RepoPath: repo, Path: p1, Branch: "feat/new", DeleteBranch: true}); err != nil {
		t.Fatal(err)
	}
	if wts, _ := listWorktrees(repo); len(wts) != 2 {
		t.Fatalf("after remove: %+v", wts)
	}
}

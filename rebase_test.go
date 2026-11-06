package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestConflictsAndRebase(t *testing.T) {
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	run := func(d string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.email=t@t", "-c", "user.name=t"}, args...)...)
		cmd.Dir = d
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(p, s string) {
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run(dir, "init", "-q", "-b", "master", repo)
	write(filepath.Join(repo, "a.txt"), "base\n")
	write(filepath.Join(repo, "b.txt"), "base\n")
	run(repo, "add", ".")
	run(repo, "commit", "-q", "-m", "init")

	ok, bad := filepath.Join(dir, "wt", "ok"), filepath.Join(dir, "wt", "bad")
	for _, p := range []struct{ path, br string }{{ok, "ok"}, {bad, "bad"}} {
		if err := addWorktree(AddRequest{RepoPath: repo, Mode: "new", Branch: p.br, Base: "master", Path: p.path}); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(ok, "b.txt"), "ok\n")
	run(ok, "commit", "-qam", "ok")
	write(filepath.Join(bad, "a.txt"), "bad\n")
	run(bad, "commit", "-qam", "bad")
	write(filepath.Join(repo, "a.txt"), "master\n")
	run(repo, "commit", "-qam", "m")

	wts, _ := listWorktrees(repo)
	byBranch := map[string]Worktree{}
	for _, w := range wts {
		byBranch[w.Branch] = w
	}
	if w := byBranch["ok"]; w.BaseBehind != 1 || len(w.Conflicts) != 0 {
		t.Fatalf("ok: %+v", w)
	}
	if w := byBranch["bad"]; len(w.Conflicts) != 1 || w.Conflicts[0] != "a.txt" {
		t.Fatalf("bad: %+v", w)
	}

	if stopped, err := rebaseOnto(ok, "master", false); err != nil || stopped {
		t.Fatalf("rebase ok: %v %v", stopped, err)
	}
	if _, err := rebaseOnto(bad, "master", false); err == nil || rebaseInProgress(bad) {
		t.Fatalf("rebase bad should fail and abort: %v", err)
	}
	if stopped, _ := rebaseOnto(bad, "master", true); !stopped || !rebaseInProgress(bad) {
		t.Fatal("rebase bad keep: should stop")
	}
	app := &App{}
	if err := app.RebaseContinue(bad); err == nil {
		t.Fatal("continue with markers should fail")
	}
	write(filepath.Join(bad, "a.txt"), "resolved\n")
	if err := app.RebaseContinue(bad); err != nil || rebaseInProgress(bad) {
		t.Fatalf("continue: %v", err)
	}
}

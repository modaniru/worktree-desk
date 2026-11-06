package main

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestTrackingAndPull(t *testing.T) {
	dir := t.TempDir()
	run := func(d string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.email=t@t", "-c", "user.name=t"}, args...)...)
		cmd.Dir = d
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	remote, other, repo := filepath.Join(dir, "remote.git"), filepath.Join(dir, "other"), filepath.Join(dir, "repo")
	run(dir, "init", "-q", "--bare", "-b", "master", remote)
	run(dir, "clone", "-q", remote, other)
	run(other, "commit", "-q", "--allow-empty", "-m", "c1")
	run(other, "push", "-q", "origin", "HEAD:master")
	run(dir, "clone", "-q", remote, repo)

	wtPath := filepath.Join(dir, "wt", "feat")
	if err := addWorktree(AddRequest{RepoPath: repo, Mode: "new", Branch: "feat", Base: "master", Path: wtPath}); err != nil {
		t.Fatal(err)
	}
	// на remote появляется 2 новых коммита
	run(other, "commit", "-q", "--allow-empty", "-m", "c2")
	run(other, "commit", "-q", "--allow-empty", "-m", "c3")
	run(other, "push", "-q", "origin", "HEAD:master")

	app := &App{}
	if err := app.Fetch(repo); err != nil {
		t.Fatal(err)
	}
	wts, _ := listWorktrees(repo)
	if m := wts[0]; m.Upstream != "origin/master" || m.Behind != 2 || m.Ahead != 0 {
		t.Fatalf("main tracking: %+v", m)
	}
	if f := wts[1]; f.Upstream != "" || f.BaseRef != "origin/master" || f.BaseBehind != 2 {
		t.Fatalf("feat tracking: %+v", f)
	}
	if _, err := app.Pull(repo); err != nil {
		t.Fatal(err)
	}
	if wts, _ := listWorktrees(repo); wts[0].Behind != 0 || wts[1].BaseBehind != 2 {
		t.Fatalf("after pull: %+v", wts)
	}
}

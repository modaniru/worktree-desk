package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Worktree — одна запись из `git worktree list --porcelain`.
type Worktree struct {
	Path     string `json:"path"`
	Name     string `json:"name"`   // своё название ворктри (хранится в конфиге приложения)
	Pinned   bool   `json:"pinned"` // закреплён наверху списка
	Head     string `json:"head"`
	Branch   string `json:"branch"`
	Detached bool   `json:"detached"`
	Bare     bool   `json:"bare"`
	Locked   bool   `json:"locked"`
	Prunable bool   `json:"prunable"`
	Main     bool   `json:"main"`
	Exists   bool   `json:"exists"`
	Dirty    int    `json:"dirty"` // число изменённых файлов, -1 если не удалось посчитать
	Subject  string `json:"subject"`
	When     string `json:"when"`
	// Updated — unix-время последнего изменения: максимум из времени
	// последнего коммита и mtime незакоммиченных файлов.
	Updated int64 `json:"updated"`

	Upstream   string `json:"upstream"`   // origin/<branch>, если настроен
	Ahead      int    `json:"ahead"`      // коммитов впереди upstream
	Behind     int    `json:"behind"`     // коммитов позади upstream
	Gone       bool   `json:"gone"`       // upstream удалён на remote
	BaseRef    string `json:"baseRef"`    // основная ветка репо (upstream main-ворктри)
	BaseBehind int    `json:"baseBehind"` // сколько коммитов BaseRef нет в HEAD
	BaseAhead  int    `json:"baseAhead"`  // сколько коммитов HEAD нет в BaseRef (свои коммиты ветки)

	// Conflicts — файлы, которые конфликтуют при слиянии с BaseRef
	// (пробное слияние в памяти, рабочую копию не трогает).
	Conflicts []string `json:"conflicts"`
	Rebasing  bool     `json:"rebasing"` // в ворктри идёт незавершённый rebase
}

// Repo — git-репозиторий внутри корня проекта.
type Repo struct {
	Name      string     `json:"name"`
	Path      string     `json:"path"`
	Worktrees []Worktree `json:"worktrees"`
	Error     string     `json:"error,omitempty"`
	Project   string     `json:"project"` // папка проекта, в которой найден репо
}

// Branch — ветка для выбора при создании ворктри.
type Branch struct {
	Name   string `json:"name"`
	Remote bool   `json:"remote"`
	InUse  bool   `json:"inUse"`
}

const (
	gitTimeout     = 30 * time.Second
	networkTimeout = 3 * time.Minute // fetch / pull
)

func git(dir string, args ...string) (string, error) {
	return gitT(gitTimeout, dir, args...)
}

func gitT(timeout time.Duration, dir string, args ...string) (string, error) {
	out, _, err := gitRun(timeout, dir, args...)
	if err != nil {
		return "", err
	}
	return out, nil
}

// gitRun возвращает stdout даже при ненулевом коде выхода (нужно для merge-tree).
func gitRun(timeout time.Duration, dir string, args ...string) (string, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	// GIT_EDITOR=true — rebase --continue и merge не должны ждать редактор
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C", "GIT_EDITOR=true")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		code := -1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg == "" {
			msg = err.Error()
		}
		return stdout.String(), code, fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return stdout.String(), 0, nil
}

var skipDirs = map[string]bool{
	"node_modules": true,
	"vendor":       true,
	"dist":         true,
	"build":        true,
}

// discoverRepos ищет репозитории (папки с .git-директорией) в root на глубину maxDepth.
// Связанные ворктри (.git — файл) пропускаются: они показываются внутри своего репо.
func discoverRepos(root string, maxDepth int) ([]string, error) {
	var repos []string
	root = filepath.Clean(root)

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == root {
				return err
			}
			return fs.SkipDir
		}
		if !d.IsDir() {
			return nil
		}
		name := d.Name()
		if path != root && (strings.HasPrefix(name, ".") || skipDirs[name]) {
			return fs.SkipDir
		}
		depth := 0
		if rel, _ := filepath.Rel(root, path); rel != "." {
			depth = strings.Count(rel, string(filepath.Separator)) + 1
		}
		if info, err := os.Stat(filepath.Join(path, ".git")); err == nil && info.IsDir() {
			repos = append(repos, path)
			if path != root {
				return fs.SkipDir
			}
		}
		if depth >= maxDepth {
			return fs.SkipDir
		}
		return nil
	})
	sort.Strings(repos)
	return repos, err
}

// parseWorktrees — только `git worktree list`, без статуса и коммитов (быстро).
func parseWorktrees(repoPath string) ([]Worktree, error) {
	out, err := git(repoPath, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}

	var (
		list []Worktree
		cur  *Worktree
	)
	for _, line := range strings.Split(out, "\n") {
		key, val, _ := strings.Cut(line, " ")
		switch key {
		case "worktree":
			list = append(list, Worktree{Path: val, Dirty: -1})
			cur = &list[len(list)-1]
		case "HEAD":
			if cur != nil {
				cur.Head = val
			}
		case "branch":
			if cur != nil {
				cur.Branch = strings.TrimPrefix(val, "refs/heads/")
			}
		case "detached":
			if cur != nil {
				cur.Detached = true
			}
		case "bare":
			if cur != nil {
				cur.Bare = true
			}
		case "locked":
			if cur != nil {
				cur.Locked = true
			}
		case "prunable":
			if cur != nil {
				cur.Prunable = true
			}
		}
	}
	if len(list) > 0 {
		list[0].Main = true
	}
	for i := range list {
		if info, err := os.Stat(list[i].Path); err == nil && info.IsDir() {
			list[i].Exists = true
		}
	}
	return list, nil
}

type tracking struct {
	upstream      string
	ahead, behind int
	gone          bool
}

// branchTracking — upstream и ahead/behind для всех локальных веток (по последнему fetch).
func branchTracking(repoPath string) map[string]tracking {
	out, err := git(repoPath, "for-each-ref",
		"--format=%(refname:short)%00%(upstream:short)%00%(upstream:track)", "refs/heads")
	res := map[string]tracking{}
	if err != nil {
		return res
	}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, "\x00")
		if len(f) != 3 || f[1] == "" {
			continue
		}
		t := tracking{upstream: f[1], gone: strings.Contains(f[2], "gone")}
		for _, part := range strings.Split(strings.Trim(f[2], "[]"), ",") {
			var n int
			if _, err := fmt.Sscanf(strings.TrimSpace(part), "ahead %d", &n); err == nil {
				t.ahead = n
			} else if _, err := fmt.Sscanf(strings.TrimSpace(part), "behind %d", &n); err == nil {
				t.behind = n
			}
		}
		res[f[0]] = t
	}
	return res
}

func listWorktrees(repoPath string) ([]Worktree, error) {
	list, err := parseWorktrees(repoPath)
	if err != nil {
		return nil, err
	}

	track := branchTracking(repoPath)
	baseRef := ""
	if len(list) > 0 && list[0].Branch != "" {
		baseRef = list[0].Branch
		if t, ok := track[baseRef]; ok && !t.gone {
			baseRef = t.upstream
		}
	}
	for i := range list {
		wt := &list[i]
		if t, ok := track[wt.Branch]; ok && wt.Branch != "" {
			wt.Upstream, wt.Ahead, wt.Behind, wt.Gone = t.upstream, t.ahead, t.behind, t.gone
		}
		if !wt.Main && baseRef != "" && wt.Branch != list[0].Branch {
			wt.BaseRef = baseRef
		}
	}

	var wg sync.WaitGroup
	for i := range list {
		wt := &list[i]
		if !wt.Exists || wt.Bare {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if st, err := git(wt.Path, "status", "--porcelain"); err == nil {
				wt.Dirty = countLines(st)
				wt.Updated = max(wt.Updated, dirtyMtime(wt.Path, st))
			}
			wt.Rebasing = rebaseInProgress(wt.Path)
			if wt.BaseRef != "" {
				// «свои N  отстаёт M»: HEAD...base → "<только в HEAD>\t<только в base>"
				if n, err := git(wt.Path, "rev-list", "--left-right", "--count", "HEAD..."+wt.BaseRef); err == nil {
					if f := strings.Fields(n); len(f) == 2 {
						wt.BaseAhead, _ = strconv.Atoi(f[0])
						wt.BaseBehind, _ = strconv.Atoi(f[1])
					}
				}
				if wt.BaseBehind > 0 {
					wt.Conflicts = mergeConflicts(wt.Path, wt.BaseRef)
				}
			}
			if lg, err := git(wt.Path, "log", "-1", "--format=%s%x00%cr%x00%ct"); err == nil {
				f := strings.SplitN(strings.TrimSpace(lg), "\x00", 3)
				if len(f) == 3 {
					wt.Subject, wt.When = f[0], f[1]
					if ct, err := strconv.ParseInt(f[2], 10, 64); err == nil {
						wt.Updated = max(wt.Updated, ct)
					}
				}
			}
		}()
	}
	wg.Wait()
	return list, nil
}

// dirtyMtime — самый свежий mtime среди файлов из `git status --porcelain`.
func dirtyMtime(dir, status string) int64 {
	var newest int64
	for _, line := range strings.Split(status, "\n") {
		if len(line) < 4 {
			continue
		}
		p := line[3:]
		if _, to, ok := strings.Cut(p, " -> "); ok {
			p = to
		}
		if info, err := os.Stat(filepath.Join(dir, strings.Trim(p, `"`))); err == nil {
			newest = max(newest, info.ModTime().Unix())
		}
	}
	return newest
}

func countLines(s string) int {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

func listBranches(repoPath string) ([]Branch, error) {
	out, err := git(repoPath, "for-each-ref", "--sort=-committerdate",
		"--format=%(refname)", "refs/heads", "refs/remotes")
	if err != nil {
		return nil, err
	}
	wts, _ := listWorktrees(repoPath)
	inUse := map[string]bool{}
	for _, wt := range wts {
		if wt.Branch != "" {
			inUse[wt.Branch] = true
		}
	}

	local := map[string]bool{}
	var branches, remotes []Branch
	for _, ref := range strings.Fields(out) {
		switch {
		case strings.HasPrefix(ref, "refs/heads/"):
			name := strings.TrimPrefix(ref, "refs/heads/")
			local[name] = true
			branches = append(branches, Branch{Name: name, InUse: inUse[name]})
		case strings.HasPrefix(ref, "refs/remotes/"):
			name := strings.TrimPrefix(ref, "refs/remotes/")
			if strings.HasSuffix(name, "/HEAD") {
				continue
			}
			remotes = append(remotes, Branch{Name: name, Remote: true})
		}
	}
	// удалённые ветки, у которых уже есть локальная копия, не дублируем
	for _, r := range remotes {
		_, short, _ := strings.Cut(r.Name, "/")
		if !local[short] {
			branches = append(branches, r)
		}
	}
	return branches, nil
}

// AddRequest — параметры создания ворктри.
type AddRequest struct {
	RepoPath string `json:"repoPath"`
	Mode     string `json:"mode"`   // new | existing
	Name     string `json:"name"`   // необязательное своё название
	Branch   string `json:"branch"` // имя новой ветки или существующая (локальная/удалённая)
	Base     string `json:"base"`   // от чего ответвлять новую ветку (mode=new)
	Path     string `json:"path"`
}

func addWorktree(r AddRequest) error {
	r.Branch = strings.TrimSpace(r.Branch)
	r.Path = strings.TrimSpace(r.Path)
	if r.Branch == "" {
		return errors.New("не указана ветка")
	}
	if r.Path == "" {
		return errors.New("не указан путь")
	}
	if _, err := os.Stat(r.Path); err == nil {
		return fmt.Errorf("путь уже существует: %s", r.Path)
	}
	if err := os.MkdirAll(filepath.Dir(r.Path), 0o755); err != nil {
		return err
	}

	var args []string
	switch r.Mode {
	case "new":
		if _, err := git(r.RepoPath, "check-ref-format", "--branch", r.Branch); err != nil {
			return fmt.Errorf("некорректное имя ветки: %s", r.Branch)
		}
		args = []string{"worktree", "add", "-b", r.Branch, r.Path}
		if b := strings.TrimSpace(r.Base); b != "" {
			args = append(args, b)
		}
	case "existing":
		if isRemoteBranch(r.RepoPath, r.Branch) {
			_, short, _ := strings.Cut(r.Branch, "/")
			args = []string{"worktree", "add", "--track", "-b", short, r.Path, r.Branch}
		} else {
			args = []string{"worktree", "add", r.Path, r.Branch}
		}
	default:
		return fmt.Errorf("неизвестный режим: %q", r.Mode)
	}
	_, err := git(r.RepoPath, args...)
	return err
}

func isRemoteBranch(repoPath, name string) bool {
	_, err := git(repoPath, "show-ref", "--verify", "--quiet", "refs/remotes/"+name)
	return err == nil
}

// RemoveRequest — параметры удаления ворктри.
type RemoveRequest struct {
	RepoPath     string `json:"repoPath"`
	Path         string `json:"path"`
	Branch       string `json:"branch"`
	Force        bool   `json:"force"`
	DeleteBranch bool   `json:"deleteBranch"`
}

func removeWorktree(r RemoveRequest) error {
	wts, err := parseWorktrees(r.RepoPath)
	if err != nil {
		return err
	}
	var target *Worktree
	for i := range wts {
		if samePath(wts[i].Path, r.Path) {
			target = &wts[i]
		}
	}
	if target == nil {
		return fmt.Errorf("ворктри не найден: %s", r.Path)
	}
	if target.Main {
		return errors.New("основной ворктри удалить нельзя")
	}

	if target.Exists {
		args := []string{"worktree", "remove"}
		if r.Force {
			args = append(args, "--force", "--force") // второй -f снимает и lock
		}
		if _, err := git(r.RepoPath, append(args, target.Path)...); err != nil {
			return err
		}
	} else if _, err := git(r.RepoPath, "worktree", "prune"); err != nil {
		return err
	}

	if r.DeleteBranch && r.Branch != "" {
		flag := "-d"
		if r.Force {
			flag = "-D"
		}
		if _, err := git(r.RepoPath, "branch", flag, r.Branch); err != nil {
			return fmt.Errorf("ворктри удалён, но ветку удалить не удалось: %w", err)
		}
	}
	return nil
}

func samePath(a, b string) bool {
	norm := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return r
		}
		return filepath.Clean(p)
	}
	return norm(a) == norm(b)
}

// mergeConflicts — пробное слияние HEAD с ref в памяти (`git merge-tree`, git ≥ 2.38).
// Возвращает конфликтующие файлы; nil — конфликтов нет (или проверить не удалось).
func mergeConflicts(dir, ref string) []string {
	out, code, _ := gitRun(gitTimeout, dir, "merge-tree", "--write-tree", "--name-only", "--no-messages", "HEAD", ref)
	if code != 1 {
		return nil
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 2 {
		return nil
	}
	return lines[1:] // первая строка — id дерева
}

func rebaseInProgress(dir string) bool {
	for _, name := range []string{"rebase-merge", "rebase-apply"} {
		p, err := git(dir, "rev-parse", "--git-path", name)
		if err != nil {
			continue
		}
		p = strings.TrimSpace(p)
		if !filepath.IsAbs(p) {
			p = filepath.Join(dir, p)
		}
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

func unmergedFiles(dir string) []string {
	out, err := git(dir, "diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return nil
	}
	return strings.Fields(out)
}

// rebaseOnto ребейзит ворктри на ref (с --autostash). При конфликте либо
// оставляет rebase незавершённым (keep — разрешать в VS Code), либо откатывает.
func rebaseOnto(dir, ref string, keep bool) (stopped bool, err error) {
	_, err = gitT(networkTimeout, dir, "rebase", "--autostash", ref)
	if err == nil {
		return false, nil
	}
	if !rebaseInProgress(dir) {
		return false, err
	}
	files := unmergedFiles(dir)
	if keep {
		return true, nil
	}
	_, _ = git(dir, "rebase", "--abort")
	return false, fmt.Errorf("конфликты в %s — rebase отменён", strings.Join(files, ", "))
}

// pull: fast-forward, если можно; если ветка разошлась с upstream — pull --rebase.
// При конфликтах rebase откатывается.
func pull(dir string) (string, error) {
	out, err := gitT(networkTimeout, dir, "pull", "--ff-only", "--autostash")
	if err == nil {
		return lastLine(out), nil
	}
	if !strings.Contains(err.Error(), "fast-forward") {
		return "", err
	}
	out, err = gitT(networkTimeout, dir, "pull", "--rebase", "--autostash")
	if err == nil {
		return "ветка разошлась с upstream — сделан pull --rebase", nil
	}
	if rebaseInProgress(dir) {
		files := unmergedFiles(dir)
		_, _ = git(dir, "rebase", "--abort")
		return "", fmt.Errorf("pull --rebase: конфликты в %s — отменено, разрешите вручную", strings.Join(files, ", "))
	}
	return "", err
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

const scanDepth = 3

// Config хранится в ~/Library/Application Support/worktrees/config.json.
type Config struct {
	// Roots — папки проектов, в которых ищутся репозитории.
	Roots []string `json:"roots"`
	// Root — старый формат (одна папка); при загрузке переносится в Roots.
	Root string `json:"root,omitempty"`
	// Names — свои названия ворктри: путь → название. Привязаны к папке,
	// поэтому переживают переключение веток внутри ворктри.
	Names map[string]string `json:"names,omitempty"`
	// Pinned — закреплённые ворктри (по пути), показываются выше остальных.
	Pinned map[string]bool `json:"pinned,omitempty"`
}

type App struct {
	ctx context.Context
	mu  sync.Mutex
	cfg Config

	// для меню-бара (guarded by mu)
	cache     []Repo   // последний результат ListRepos
	current   string   // папка активного окна VS Code
	menuPaths []string // tag пункта меню (1-based) → путь ворктри
}

func NewApp() *App { return &App{} }

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.cfg = loadConfig()
	a.startMenubar()
}

func configPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "worktrees", "config.json")
}

func loadConfig() Config {
	var c Config
	if b, err := os.ReadFile(configPath()); err == nil {
		_ = json.Unmarshal(b, &c)
	}
	if c.Root != "" {
		if !slices.Contains(c.Roots, c.Root) {
			c.Roots = append([]string{c.Root}, c.Roots...)
		}
		c.Root = ""
	}
	return c
}

func (a *App) saveConfig() error {
	p := configPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(a.cfg, "", "  ")
	return os.WriteFile(p, b, 0o644)
}

// ---- методы, доступные из фронта ----

// GetRoots — папки проектов в порядке добавления.
func (a *App) GetRoots() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.cfg.Roots)
}

// AddRoot открывает диалог выбора папки и добавляет её в список проектов.
// Возвращает добавленную папку ("" — если выбор отменён).
func (a *App) AddRoot() (string, error) {
	def := ""
	if roots := a.GetRoots(); len(roots) > 0 {
		def = filepath.Dir(roots[len(roots)-1])
	}
	dir, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title:                "Добавить папку проекта",
		DefaultDirectory:     def,
		CanCreateDirectories: false,
	})
	if err != nil || dir == "" {
		return "", err
	}
	return dir, a.addRoot(dir)
}

func (a *App) addRoot(dir string) error {
	dir = filepath.Clean(dir)
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, r := range a.cfg.Roots {
		switch {
		case samePath(r, dir):
			return fmt.Errorf("папка %s уже добавлена", dir)
		case isInside(dir, r):
			return fmt.Errorf("папка %s уже входит в проект %s", dir, r)
		}
	}
	a.cfg.Roots = append(a.cfg.Roots, dir)
	return a.saveConfig()
}

// RemoveRoot убирает папку из списка проектов (на диске ничего не трогает).
func (a *App) RemoveRoot(dir string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := len(a.cfg.Roots)
	a.cfg.Roots = slices.DeleteFunc(a.cfg.Roots, func(r string) bool { return samePath(r, dir) })
	if len(a.cfg.Roots) == n {
		return nil
	}
	return a.saveConfig()
}

// isInside: p лежит внутри dir (или совпадает с ней).
func isInside(p, dir string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// rootOf — проект, к которому относится путь (самая глубокая подходящая папка).
func (a *App) rootOf(p string) string {
	best := ""
	for _, r := range a.GetRoots() {
		if isInside(p, r) && len(r) > len(best) {
			best = r
		}
	}
	return best
}

// ListRepos — репозитории всех проектов; репо, попавший в несколько проектов,
// показывается один раз (в первом). Недоступная папка проекта — Repo с Error.
func (a *App) ListRepos() ([]Repo, error) {
	roots := a.GetRoots()
	if len(roots) == 0 {
		return nil, nil
	}
	var repos []Repo
	seen := map[string]bool{}
	for _, root := range roots {
		paths, err := discoverRepos(root, scanDepth)
		if err != nil {
			repos = append(repos, Repo{Name: filepath.Base(root), Path: root, Project: root, Error: err.Error()})
			continue
		}
		for _, p := range paths {
			if seen[p] {
				continue
			}
			seen[p] = true
			name, _ := filepath.Rel(root, p)
			if name == "." {
				name = filepath.Base(p)
			}
			repos = append(repos, Repo{Name: name, Path: p, Project: root})
		}
	}
	var wg sync.WaitGroup
	for i := range repos {
		if repos[i].Error != "" {
			continue
		}
		wg.Add(1)
		go func(r *Repo) {
			defer wg.Done()
			wts, err := listWorktrees(r.Path)
			if err != nil {
				r.Error = err.Error()
			}
			r.Worktrees = wts
		}(&repos[i])
	}
	wg.Wait()

	a.mu.Lock()
	for i := range repos {
		for j := range repos[i].Worktrees {
			wt := &repos[i].Worktrees[j]
			wt.Name = a.cfg.Names[filepath.Clean(wt.Path)]
			wt.Pinned = a.cfg.Pinned[filepath.Clean(wt.Path)]
		}
	}
	a.cache = repos
	a.mu.Unlock()
	a.renderMenu() // держать меню-бар в актуальном состоянии
	return repos, nil
}

func (a *App) ListBranches(repoPath string) ([]Branch, error) {
	return listBranches(repoPath)
}

var slugRe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// SuggestPath — путь по умолчанию: <проект>/.worktrees/<repo>/<название или ветка>.
func (a *App) SuggestPath(repoPath, branch, name string) string {
	root := a.rootOf(repoPath)
	if root == "" {
		root = filepath.Dir(repoPath)
	}
	rel, err := filepath.Rel(root, repoPath)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		rel = filepath.Base(repoPath)
	}
	repoSlug := slugRe.ReplaceAllString(strings.ReplaceAll(rel, string(filepath.Separator), "-"), "-")
	branch = strings.TrimSpace(branch)
	if _, short, ok := strings.Cut(branch, "/"); ok && isRemoteBranch(repoPath, branch) {
		branch = short
	}
	if n := strings.TrimSpace(name); n != "" {
		branch = strings.ToLower(translit(n))
	}
	branchSlug := strings.Trim(slugRe.ReplaceAllString(strings.ReplaceAll(branch, "/", "-"), "-"), "-")
	if branchSlug == "" {
		branchSlug = "new"
	}
	return filepath.Join(root, ".worktrees", repoSlug, branchSlug)
}

func (a *App) AddWorktree(r AddRequest) error {
	if err := addWorktree(r); err != nil {
		return err
	}
	if strings.TrimSpace(r.Name) != "" {
		return a.SetName(r.Path, r.Name)
	}
	return nil
}

func (a *App) RemoveWorktree(r RemoveRequest) error {
	err := removeWorktree(r)
	if err == nil || !dirExists(r.Path) {
		// ворктри удалён — название и закреп больше не нужны
		_ = a.SetName(r.Path, "")
		_ = a.SetPinned(r.Path, false)
	}
	return err
}

// SetName задаёт своё название ворктри; пустое — убрать.
func (a *App) SetName(path, name string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	key := filepath.Clean(path)
	name = strings.TrimSpace(name)
	if name == "" {
		if _, ok := a.cfg.Names[key]; !ok {
			return nil
		}
		delete(a.cfg.Names, key)
	} else {
		if a.cfg.Names == nil {
			a.cfg.Names = map[string]string{}
		}
		a.cfg.Names[key] = name
	}
	return a.saveConfig()
}

// SetPinned закрепляет ворктри наверху списка или снимает закреп.
func (a *App) SetPinned(path string, pinned bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	key := filepath.Clean(path)
	if a.cfg.Pinned[key] == pinned {
		return nil
	}
	if pinned {
		if a.cfg.Pinned == nil {
			a.cfg.Pinned = map[string]bool{}
		}
		a.cfg.Pinned[key] = true
	} else {
		delete(a.cfg.Pinned, key)
	}
	return a.saveConfig()
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

var translitMap = map[rune]string{
	'а': "a", 'б': "b", 'в': "v", 'г': "g", 'д': "d", 'е': "e", 'ё': "e", 'ж': "zh",
	'з': "z", 'и': "i", 'й': "y", 'к': "k", 'л': "l", 'м': "m", 'н': "n", 'о': "o",
	'п': "p", 'р': "r", 'с': "s", 'т': "t", 'у': "u", 'ф': "f", 'х': "h", 'ц': "ts",
	'ч': "ch", 'ш': "sh", 'щ': "sch", 'ъ': "", 'ы': "y", 'ь': "", 'э': "e", 'ю': "yu", 'я': "ya",
}

// translit — кириллица → латиница для имени папки («всякая всячина» → vsyakaya-vsyachina).
func translit(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if t, ok := translitMap[r]; ok {
			b.WriteString(t)
		} else if r == ' ' {
			b.WriteByte('-')
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Fetch обновляет remote-ветки репо (для подсчёта отставания).
func (a *App) Fetch(repoPath string) error {
	_, err := gitT(networkTimeout, repoPath, "fetch", "--all", "--prune")
	return err
}

// Pull подтягивает upstream в ворктри (ff-only, при расхождении — --rebase).
func (a *App) Pull(path string) (string, error) { return pull(path) }

// Rebase ребейзит ворктри на onto. Возвращает true, если rebase остановился
// на конфликтах и оставлен для ручного разрешения (keepOnConflict).
func (a *App) Rebase(path, onto string, keepOnConflict bool) (bool, error) {
	return rebaseOnto(path, onto, keepOnConflict)
}

func (a *App) RebaseContinue(path string) error {
	if _, err := git(path, "add", "-u"); err != nil {
		return err
	}
	if out, code, _ := gitRun(gitTimeout, path, "diff", "--cached", "--check"); code != 0 &&
		strings.Contains(out, "conflict marker") {
		return fmt.Errorf("в файлах остались маркеры конфликта:\n%s", strings.TrimSpace(out))
	}
	_, err := gitT(networkTimeout, path, "rebase", "--continue")
	return err
}

func (a *App) RebaseAbort(path string) error {
	_, err := git(path, "rebase", "--abort")
	return err
}

func (a *App) Prune(repoPath string) error {
	_, err := git(repoPath, "worktree", "prune", "-v")
	return err
}

func (a *App) OpenInFinder(path string) error   { return start("open", path) }
func (a *App) OpenInTerminal(path string) error { return start("open", "-a", "Terminal", path) }

func (a *App) CopyText(s string) error {
	return runtime.ClipboardSetText(a.ctx, s)
}

// ---- вспомогательное ----

func start(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

func findVSCode() string {
	if p, err := exec.LookPath("code"); err == nil {
		return p
	}
	for _, p := range []string{
		"/Applications/Visual Studio Code.app/Contents/Resources/app/bin/code",
		filepath.Join(os.Getenv("HOME"), "Applications/Visual Studio Code.app/Contents/Resources/app/bin/code"),
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// fixPath: приложение, запущенное из Finder, получает урезанный PATH
// (/usr/bin:/bin:...), без Homebrew и `code`. Добавляем типовые места.
func fixPath() {
	extra := []string{"/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin", "/usr/sbin", "/sbin"}
	parts := filepath.SplitList(os.Getenv("PATH"))
	seen := map[string]bool{}
	for _, p := range parts {
		seen[p] = true
	}
	for _, p := range extra {
		if !seen[p] {
			parts = append(parts, p)
		}
	}
	_ = os.Setenv("PATH", strings.Join(parts, string(os.PathListSeparator)))
}

// OpenInVSCode всегда открывает папку в последнем активном окне VS Code (`code -r`);
// если VS Code не запущен, он просто стартует с этой папкой.
// Строка — пояснение для пользователя (сейчас всегда пустая).
func (a *App) OpenInVSCode(path string) (string, error) {
	if bin := findVSCode(); bin != "" {
		return "", start(bin, "-r", path)
	}
	return "", start("open", "-a", "Visual Studio Code", path)
}

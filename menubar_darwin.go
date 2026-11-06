package main

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Cocoa
#include <stdlib.h>
extern void statusbarStart(void);
extern void statusbarSetTitle(const char *s);
extern void statusbarClearMenu(void);
extern void statusbarAddSeparator(void);
extern void statusbarAddItem(const char *label, int tag, int checked, int enabled);
*/
import "C"

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"
	"unsafe"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// Спецтеги пунктов меню (обычные ворктри имеют tag ≥ 1).
const (
	tagShow    = -1
	tagRefresh = -2
	tagQuit    = -3
)

var menubarApp *App

// startMenubar включает пункт в меню-баре и цикл слежения за VS Code.
func (a *App) startMenubar() {
	menubarApp = a
	C.statusbarStart()
	go a.refreshMenubar() // первичное наполнение списка
	go a.menubarLoop()
}

// menubarLoop отслеживает, какая папка открыта в активном окне VS Code,
// и обновляет заголовок/галочку, когда она меняется.
func (a *App) menubarLoop() {
	for {
		cur := currentVSCodeFolder()
		a.mu.Lock()
		changed := cur != a.current
		a.current = cur
		a.mu.Unlock()
		if changed {
			a.renderMenu()
		}
		time.Sleep(2 * time.Second)
	}
}

// refreshMenubar перечитывает репозитории (git) и перестраивает меню.
func (a *App) refreshMenubar() {
	repos, err := a.ListRepos()
	if err != nil {
		return
	}
	a.mu.Lock()
	a.cache = repos
	a.mu.Unlock()
	a.renderMenu()
}

type menuItem struct {
	label                string
	tag, checked, enable int
	sep                  bool
}

var menuRenderMu sync.Mutex

// renderMenu строит меню из кэша репозиториев (без git) и ставит заголовок.
func (a *App) renderMenu() {
	menuRenderMu.Lock()
	defer menuRenderMu.Unlock()

	a.mu.Lock()
	repos := a.cache
	current := a.current
	a.menuPaths = a.menuPaths[:0]
	var items []menuItem
	curDisplay := ""
	projects := map[string]bool{}
	for _, repo := range repos {
		projects[repo.Project] = true
	}
	for _, repo := range repos {
		if len(repo.Worktrees) == 0 {
			continue
		}
		head := repo.Name
		if len(projects) > 1 { // одинаковые имена репо в разных проектах
			head = filepath.Base(repo.Project) + " / " + repo.Name
		}
		items = append(items, menuItem{label: head})
		for _, wt := range repo.Worktrees {
			a.menuPaths = append(a.menuPaths, wt.Path)
			tag := len(a.menuPaths) // 1-based
			disp := wtDisplay(wt)
			label := "    " + disp
			if wt.Name != "" && wt.Branch != "" {
				label += "  ·  " + wt.Branch
			}
			checked := 0
			if current != "" && wt.Exists && samePath(wt.Path, current) {
				checked = 1
				curDisplay = disp
			}
			items = append(items, menuItem{label: label, tag: tag, checked: checked, enable: 1})
		}
		items = append(items, menuItem{sep: true})
	}
	items = append(items,
		menuItem{label: "Открыть Worktrees", tag: tagShow, enable: 1},
		menuItem{label: "Обновить список", tag: tagRefresh, enable: 1},
		menuItem{sep: true},
		menuItem{label: "Выйти", tag: tagQuit, enable: 1},
	)
	title := "Worktrees"
	if curDisplay != "" {
		title = "⎇ " + truncate(curDisplay, 28)
	}
	a.mu.Unlock()

	C.statusbarClearMenu()
	for _, it := range items {
		if it.sep {
			C.statusbarAddSeparator()
			continue
		}
		cl := C.CString(it.label)
		C.statusbarAddItem(cl, C.int(it.tag), C.int(it.checked), C.int(it.enable))
		C.free(unsafe.Pointer(cl))
	}
	ct := C.CString(title)
	C.statusbarSetTitle(ct)
	C.free(unsafe.Pointer(ct))
}

func wtDisplay(wt Worktree) string {
	switch {
	case wt.Name != "":
		return wt.Name
	case wt.Branch != "":
		return wt.Branch
	case wt.Bare:
		return "(bare)"
	default:
		return "detached"
	}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

//export goMenubarClicked
func goMenubarClicked(tag C.int) {
	a := menubarApp
	if a == nil {
		return
	}
	switch t := int(tag); {
	case t == tagShow:
		runtime.WindowShow(a.ctx)
		runtime.WindowUnminimise(a.ctx)
	case t == tagRefresh:
		go a.refreshMenubar()
	case t == tagQuit:
		runtime.Quit(a.ctx)
	case t >= 1:
		a.mu.Lock()
		var path string
		if i := t - 1; i < len(a.menuPaths) {
			path = a.menuPaths[i]
		}
		a.mu.Unlock()
		if path != "" {
			_, _ = a.OpenInVSCode(path)
		}
	}
}

// currentVSCodeFolder — папка активного окна VS Code из его storage.json.
// Прав доступа не требует: читаем файл настроек VS Code.
func currentVSCodeFolder() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(home,
		"Library/Application Support/Code/User/globalStorage/storage.json"))
	if err != nil {
		return ""
	}
	var d struct {
		WindowsState struct {
			LastActiveWindow struct {
				Folder string `json:"folder"`
			} `json:"lastActiveWindow"`
		} `json:"windowsState"`
	}
	if err := json.Unmarshal(b, &d); err != nil {
		return ""
	}
	folder := d.WindowsState.LastActiveWindow.Folder
	if folder == "" {
		return ""
	}
	u, err := url.Parse(folder)
	if err != nil || u.Scheme != "file" {
		return ""
	}
	return filepath.Clean(u.Path)
}

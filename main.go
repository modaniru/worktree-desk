package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	fixPath()

	app := NewApp()

	err := wails.Run(&options.App{
		Title:            "Worktrees",
		Width:            1100,
		Height:           760,
		MinWidth:         760,
		MinHeight:        480,
		AssetServer:      &assetserver.Options{Assets: assets},
		BackgroundColour: &options.RGBA{R: 24, G: 24, B: 27, A: 1},
		// закрытие окна прячет его — приложение живёт в меню-баре
		HideWindowOnClose: true,
		OnStartup:         app.startup,
		Bind:              []any{app},
		Mac: &mac.Options{
			TitleBar: mac.TitleBarHiddenInset(),
			About: &mac.AboutInfo{
				Title:   "Worktrees",
				Message: "Управление git worktree во всех репозиториях ваших проектов",
			},
		},
	})
	if err != nil {
		log.Fatal(err)
	}
}

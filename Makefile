WAILS := $(shell go env GOPATH)/bin/wails

.PHONY: dev build install test

dev:      ## запуск с hot-reload фронта
	$(WAILS) dev

build:    ## собрать build/bin/Worktrees.app
	$(WAILS) build

install: build  ## положить в /Applications
	rm -rf /Applications/Worktrees.app
	cp -R build/bin/Worktrees.app /Applications/

test:
	go test ./...

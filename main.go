package main

import (
	"embed"
	"log"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	app := application.New(application.Options{
		Name: "Karte",
	})

	// Resolve local index.html for development/bundled run
	indexPath := resolveIndexPath()
	mainWin := application.NewWindow(application.WebviewWindowOptions{
		Title:  "Karte",
		Width:  1400,
		Height: 900,
		URL:    indexPath,
	})

	backend := NewApp()
	backend.InitApp(app, mainWin)
	// v3 alpha.38ではBindは未提供のため、イベント経由に移行する

	// Close main window to quit app
	mainWin.OnWindowEvent(events.Common.WindowClosing, func(event *application.WindowEvent) {
		app.Quit()
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

func resolveIndexPath() string {
	// Try to find frontend/dist/index.html relative to executable or cwd
	paths := []string{
		filepath.Join("frontend", "dist", "index.html"),
		filepath.Join("..", "frontend", "dist", "index.html"),
	}
	exe, _ := os.Executable()
	if exe != "" {
		exeDir := filepath.Dir(exe)
		paths = append(paths,
			filepath.Join(exeDir, "frontend", "dist", "index.html"),
			filepath.Join(filepath.Dir(exeDir), "frontend", "dist", "index.html"),
		)
	}
	for _, p := range paths {
		if abs, err := filepath.Abs(p); err == nil {
			if _, err := os.Stat(abs); err == nil {
				return "file://" + filepath.ToSlash(abs)
			}
		}
	}
	// Fallback to embedded index.html (served by file URL won't work) - as last resort, try relative URL
	return "index.html"
}

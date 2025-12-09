package main

import (
	"context"
	"embed"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	// Create an instance of the app structure
	app := NewApp()

	// Create custom HTTP handler for media files
	assetHandler := app.createAssetHandler()

	// Create application with options
	err := wails.Run(&options.App{
		Title:  "Karte",
		Width:  1400,
		Height: 900,
		AssetServer: &assetserver.Options{
			Assets:  assets,
			Handler: assetHandler,
		},
		BackgroundColour: &options.RGBA{R: 255, G: 255, B: 255, A: 1},
		OnStartup:        app.startup,
		OnBeforeClose: func(ctx context.Context) (prevent bool) {
			// Emit event to JS to check unsaved changes
			// JS will show modal and call AllowClose() if user confirms
			app.ctx = ctx
			app.checkUnsavedBeforeClose()
			return true // Prevent closing by default, JS will handle it
		},
		Bind: []interface{}{
			app,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}

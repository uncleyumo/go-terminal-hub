package main

import "github.com/wailsapp/wails/v3/pkg/application"

type AppService struct {
}

func (g *AppService) QuitApp() {
	app := application.Get()
	app.Quit()
}

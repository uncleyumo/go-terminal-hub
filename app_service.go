package main

import (
	"os"

	"github.com/uncleyumo/go-terminal-hub/internal/exec/store"
	"github.com/wailsapp/wails/v3/pkg/application"
)

type AppService struct {
}

func (a *AppService) QuitApp() {
	app := application.Get()
	app.Quit()
}

func (a *AppService) GetStartOnBootStatus() bool {
	storeInstance, err := store.GetStore()
	if err != nil {
		return false
	}
	settings := storeInstance.GetSettings()
	return settings.StartOnBoot
}

func (a *AppService) SetStartOnBoot(enabled bool) error {
	storeInstance, err := store.GetStore()
	if err != nil {
		return err
	}
	settings := storeInstance.GetSettings()
	settings.StartOnBoot = enabled
	if err := storeInstance.UpdateSettings(settings); err != nil {
		return err
	}
	app := application.Get()
	if enabled {
		err = app.Autostart.EnableWithOptions(application.AutostartOptions{
			Arguments: []string{"--autostart"},
		})
	} else {
		err = app.Autostart.Disable()
	}
	return err
}

func (a *AppService) GetAppWorkDir() (string, error) {
	return os.Getwd()
}

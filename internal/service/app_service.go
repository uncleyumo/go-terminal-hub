package service

import (
	"os"
	"os/exec"
	"path/filepath"

	"github.com/pkg/errors"
	"github.com/uncleyumo/go-terminal-hub/internal/exec/executor"
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

// ResolveWorkDir 用于复制工作目录路径
func (a *AppService) ResolveWorkDir(id string) (string, error) {
	storeInstance, err := store.GetStore()
	if err != nil {
		return "", err
	}
	session, ok := storeInstance.GetOne(id)
	if !ok {
		return "", errors.Errorf("session not found for id when resolving work dir: %s", id)
	}
	return session.WorkDir, nil
}

// ResolveScriptPath 用于复制脚本文件绝对路径

func (a *AppService) ResolveScriptPath(id string) (string, error) {
	storeInstance, err := store.GetStore()
	if err != nil {
		return "", err
	}
	session, ok := storeInstance.GetOne(id)
	if !ok {
		return "", errors.Errorf("session not found for id when resolving script path: %s", id)
	}
	kind := session.Kind
	if !executor.CheckKindInWhiteList(kind) {
		return "", errors.Errorf("kind is not in white list: %s", kind)
	}
	if kind == "terminal-shell" || kind == "shell" {
		return "", errors.Errorf("kind is not supported when resolving script path: %s", kind)
	}
	return session.Target, nil
}

// OpenWorkDir 用于打开工作目录

func (a *AppService) OpenWorkDir(id string) error {
	storeInstance, err := store.GetStore()
	if err != nil {
		return err
	}
	session, ok := storeInstance.GetOne(id)
	if !ok {
		return errors.Errorf("session not found for id when opening work dir: %s", id)
	}
	if _, err := os.Stat(session.WorkDir); err != nil {
		return err
	}
	cmd := exec.Command("explorer.exe", session.WorkDir)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() {
		_ = cmd.Wait()
	}()
	return nil
}

// OpenScriptDir 用于打开脚本文件目录
func (a *AppService) OpenScriptDir(id string) error {
	storeInstance, err := store.GetStore()
	if err != nil {
		return err
	}
	session, ok := storeInstance.GetOne(id)
	if !ok {
		return errors.Errorf("session not found for id when opening script dir: %s", id)
	}
	kind := session.Kind
	if !executor.CheckKindInWhiteList(kind) {
		return errors.Errorf("kind is not in white list: %s", kind)
	}
	if kind == "terminal-shell" || kind == "shell" {
		return errors.Errorf("kind is not supported when opening script dir: %s", kind)
	}
	if _, err := os.Stat(session.Target); err != nil {
		return err
	}
	abs, err := filepath.Abs(session.Target)
	if err != nil {
		return err
	}
	dir := filepath.Dir(abs)
	cmd := exec.Command("explorer.exe", dir)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() {
		_ = cmd.Wait()
	}()
	return nil
}

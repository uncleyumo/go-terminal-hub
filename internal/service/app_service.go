package service

import (
	"os"
	"os/exec"
	"path/filepath"

	"github.com/pkg/errors"
	"github.com/uncleyumo/go-terminal-hub/internal/buildinfo"
	"github.com/uncleyumo/go-terminal-hub/internal/exec/executor"
	"github.com/uncleyumo/go-terminal-hub/internal/exec/hub"
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

	sessionWorkDir := session.WorkDir
	if sessionWorkDir == "" {
		return os.Getwd()
	}
	return sessionWorkDir, nil
}

// ResolveSpecCommandLine 用于回显实际的命令行
func (a *AppService) ResolveSpecCommandLine(id string) (string, error) {
	session, err := hub.GetHub().GetSession(id)
	if err != nil {
		return "", err
	}
	return session.GetLaunchSpec().Command, nil
}

// OpenWorkDir 用于打开工作目录
func (a *AppService) OpenWorkDir(id string) error {
	sessionWorkDir, err := a.ResolveWorkDir(id)
	if err != nil {
		return err
	}
	cmd := exec.Command("explorer.exe", sessionWorkDir)
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
	cmd := exec.Command("explorer.exe", filepath.Dir(abs))
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() {
		_ = cmd.Wait()
	}()
	return nil
}

func (a *AppService) GetBuildInfo() buildinfo.BuildInfo {
	return buildinfo.Info()
}

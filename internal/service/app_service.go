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
	"github.com/uncleyumo/go-terminal-hub/internal/paste"
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
	spec, err := hub.GetHub().LaunchSpec(id)
	if err != nil {
		return "", err
	}
	return spec.Command, nil
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

// OpenExeDir 在资源管理器里打开当前 exe 所在目录，并选中这个 exe。
// 「关于」弹窗用：用户想知道程序装在哪、或者更新后去翻备份文件。
//
// 不能直接用 os.Args[0]：那是启动时传进来的路径，可能带相对路径或用 8.3
// 短名；os.Executable() 给的是内核记下的真实路径。
func (a *AppService) OpenExeDir() error {
	exe, err := os.Executable()
	if err != nil {
		return errors.Errorf("locate current executable: %v", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	// explorer 的 /select 和路径要分开传，explorer 会自己拼回去。
	// 它打开成功后退出码也常是非 0，所以只关心 Start 是否失败。
	cmd := exec.Command("explorer.exe", "/select,", exe)
	if err := cmd.Start(); err != nil {
		return errors.Errorf("open exe dir: %v", err)
	}
	go func() {
		_ = cmd.Wait()
	}()
	return nil
}

// GetPasteCacheInfo 给「关于」弹窗显示手机发来的图片占了多少地方。
func (a *AppService) GetPasteCacheInfo() paste.Info {
	info, err := paste.Stat()
	if err != nil {
		// 数不出来就当空的：这是个只读的展示值，不值得把弹窗搞成错误页
		return paste.Info{}
	}
	return info
}

// ClearPasteCache 清掉图片缓存，返回清掉了多少。
func (a *AppService) ClearPasteCache() (paste.Info, error) {
	return paste.Clear()
}

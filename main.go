package main

import (
	"embed"
	"flag"
	"log/slog"
	"os"
	"path/filepath"

	"log"

	"github.com/uncleyumo/go-terminal-hub/internal/exec/sink"
	"github.com/uncleyumo/go-terminal-hub/internal/exec/store"
	"github.com/uncleyumo/go-terminal-hub/internal/remote"
	"github.com/uncleyumo/go-terminal-hub/internal/service"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// Wails uses Go's `embed` package to embed the frontend files into the binary.
// Any files in the frontend/dist folder will be embedded into the binary and
// made available to the frontend.
// See https://pkg.go.dev/embed for more information.

//go:embed all:frontend/dist
var assets embed.FS

func init() {
	// Register a custom event whose associated data type is string.
	// This is not required, but the binding generator will pick up registered events
	// and provide a strongly typed JS/TS API for them.
	application.RegisterEvent[string]("time")
	application.RegisterEvent[sink.OutputPayload]("session:output")
	application.RegisterEvent[sink.ExitPayload]("session:exited")
	application.RegisterEvent[string]("session:started")
	// 注册的类型必须和 Emit 传的完全一致，否则 wails 会把事件 Cancel 掉，
	// 前端一条也收不到——见 sink_emit.go 里 OutputPayload 的注释。
	application.RegisterEvent[sink.LeasePayload]("session:lease")
	application.RegisterEvent[sink.SizePayload]("session:size")
	application.RegisterEvent[remote.Status]("remote:status")
}

// main function serves as the application's entry point. It initializes the application, creates a window,
// and starts a goroutine that emits a time-based event every second. It subsequently runs the application and
// logs any error that might occur.
func main() {

	logLevel := flag.String("log-level", "info", "add use lowercase level to set log level, default is 'info'")
	autostartBool := flag.Bool("autostart", false, "start the application on system startup (window hidden)")

	flag.Parse()

	initLogger(*logLevel)

	var window *application.WebviewWindow

	// 服务对象先建好再登记：StoreService 保存设置后要通知 RemoteService 重读配置，
	// 让远程桥热生效（启用/停用/换地址/换令牌都不用重启）。用注入回调而不是
	// 让两个 service 互相 import，免得出现 import 环。
	remoteService := &service.RemoteService{}
	storeService := &service.StoreService{OnSettingsSaved: remoteService.Reload}

	// Create a new Wails application by providing the necessary options.
	// Variables 'Name' and 'Description' are for application metadata.
	// 'Assets' configures the asset server with the 'FS' variable pointing to the frontend files.
	// 'Bind' is a list of Go struct instances. The frontend has access to the methods of these instances.
	// 'Mac' options tailor the application when running an macOS.
	app := application.New(application.Options{
		Name:        "go-terminal-hub",
		Description: "Manage scripts and CLI programs as terminal sessions from one window",
		Services: []application.Service{
			application.NewService(storeService),
			application.NewService(&service.HubService{}),
			application.NewService(&service.AppService{}),
			application.NewService(remoteService),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: "https://github.com/uncleyumo/go-terminal-hub",
			OnSecondInstanceLaunch: func(data application.SecondInstanceData) {
				window.Show()
			},
			AdditionalData: nil,
			ExitCode:       0,
			EncryptionKey:  [32]byte{},
		},
	})

	// Create a new window with the necessary options.
	// 'Title' is the title of the window.
	// 'Mac' options tailor the window when running on macOS.
	// 'BackgroundColour' is the background color of the window.
	// 'URL' is the URL that will be loaded into the webview.
	window = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title: "go-terminal-hub",
		// Window sized to the golden ratio (1000 / 618 ≈ 1.618).
		Width:  1000,
		Height: 618,
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 50,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
		BackgroundColour: application.NewRGB(255, 255, 255),
		Hidden:           *autostartBool,
		URL:              "/",
	})

	tray := app.SystemTray.New()
	tray.SetTooltip("go-terminal-hub")
	tray.OnClick(func() {
		if window.IsVisible() {
			window.Hide()
		} else {
			window.Show()
			window.Focus()
		}
	})

	window.RegisterHook(
		events.Common.WindowClosing,
		func(e *application.WindowEvent) {
			window.Hide()
			e.Cancel()
		},
	)

	if storeInstance, err := store.GetStore(); err == nil {
		if storeInstance.GetSettings().StartOnBoot {
			if err := app.Autostart.EnableWithOptions(application.AutostartOptions{
				Arguments: []string{"--autostart"},
			}); err != nil {
				slog.Error("failed to enable autostart", "error", err)
			}
		}
	} else {
		slog.Error("failed to get store when enabling autostart", "error", err)
	}

	// Run the application. This blocks until the application has been exited.
	err := app.Run()

	// If an error occurred while running the application, log it and exit.
	if err != nil {
		log.Fatal(err)
	}
}

func initLogger(logLevel string) {
	raw := logLevel
	var level slog.Level
	if raw == "" {
		level = slog.LevelInfo
	} else if err := level.UnmarshalText([]byte(raw)); err != nil {
		slog.SetDefault(newTextLogger(slog.LevelInfo))
		slog.Warn("LOGGER_LEVEL invalid, using info", "value", raw)
		return
	}
	slog.SetDefault(newTextLogger(level))
}

func newTextLogger(level slog.Level) *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level:     level,
		AddSource: true,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.SourceKey {
				if src, ok := a.Value.Any().(*slog.Source); ok {
					src.File = filepath.Base(src.File)
				}
			}
			return a
		},
	}))
}

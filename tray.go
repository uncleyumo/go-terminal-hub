package main

import (
	"fmt"

	"github.com/uncleyumo/go-terminal-hub/internal/buildinfo"
	"github.com/uncleyumo/go-terminal-hub/internal/exec/hub"
	"github.com/uncleyumo/go-terminal-hub/internal/remote"
	"github.com/uncleyumo/go-terminal-hub/internal/service"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// 托盘菜单发给前端的两个动作。
//
// 回调跑在后台 goroutine 里（menuitem.go:243-258）。退出不能在这儿直接
// app.Quit()——那就绕过了前端「还有会话在跑，先确认」那一步。所以两个动作
// 都是「先把主窗口叫出来，再发个事件」，剩下的交给前端现有的流程。
const (
	trayActionCheckUpdate = "check-update"
	trayActionQuit        = "quit"

	// trayActionEvent 是发这些动作的事件名。前端只订阅这一条，
	// 按 data 里的字符串分流。
	trayActionEvent = "tray:action"
)

// trayMenu 管托盘右键菜单上会变的那几行。
//
// 菜单是 Windows 原生菜单：改文字用 MenuItem.SetLabel，它直接改那一项，
// 不用重建整个菜单；只有增删项才需要 Menu.Update()。
// SetLabel 自己不切主线程，所以统一用 application.InvokeAsync 递过去。
//
// 用 InvokeAsync 而不是 InvokeSync：InvokeSync 会等主线程跑完才返回，
// 而托盘第一次刷新发生在 ServiceStartup 里（主循环还没转起来），
// 那时候同步等就是死等。异步丢一次最多是文字晚一拍，下一个事件会补上。
type trayMenu struct {
	sessions *application.MenuItem
	relay    *application.MenuItem
	version  *application.MenuItem
	update   *application.MenuItem

	remote *service.RemoteService
	app    *application.App
}

// newTrayMenu 建菜单、挂到托盘上，返回一个可以反复刷新的句柄。
//
// showWindow 是「把主窗口叫出来」——托盘动作都先做这一步，
// 因为窗口平时是藏起来的（关闭即隐藏），不叫出来用户看不到任何反馈。
func newTrayMenu(
	app *application.App,
	tray *application.SystemTray,
	remoteService *service.RemoteService,
	showWindow func(),
) *trayMenu {
	menu := app.NewMenu()

	t := &trayMenu{app: app, remote: remoteService}

	// 三行只读信息。SetEnabled(false) 让它们灰掉、点不动——
	// 它们的作用是「看一眼就知道现在什么情况」，不是动作。
	t.sessions = menu.Add("运行中会话：0")
	t.relay = menu.Add("relay：未启用")
	t.version = menu.Add("版本：" + displayVersion())
	for _, item := range []*application.MenuItem{t.sessions, t.relay, t.version} {
		item.SetEnabled(false)
	}

	menu.AddSeparator()

	// 「检查更新」这一行是复用的：有新版时它的文字变成「新版本 vX 可用」，
	// 点击都走同一条路——叫出窗口、打开更新弹窗。
	// （设计里「新版本可用」是单独一项、只在有新版时出现；那需要增删菜单项
	// 再 Menu.Update()，这里改成改文字，少一次重建。）
	t.update = menu.Add("检查更新")
	t.update.OnClick(func(*application.Context) {
		showWindow()
		app.Event.Emit(trayActionEvent, trayActionCheckUpdate)
	})

	menu.Add("显示主窗口").OnClick(func(*application.Context) {
		showWindow()
	})

	menu.Add("退出").OnClick(func(*application.Context) {
		showWindow()
		app.Event.Emit(trayActionEvent, trayActionQuit)
	})

	tray.SetMenu(menu)
	return t
}

// refresh 重算所有会变的文字。
//
// 会话数、relay 状态、更新状态都是「读一下就知道」的值，直接现算，
// 不缓存——缓存就得管失效，而这三处读起来都很便宜。
func (t *trayMenu) refresh(st service.UpdateState) {
	running := 0
	for _, r := range hub.GetHub().ListRecordStatus() {
		if r.Running {
			running++
		}
	}

	relayLabel := "relay：未启用"
	if t.remote != nil {
		relayLabel = "relay：" + relayText(t.remote.GetRemoteStatus().State)
	}

	updateLabel, updateEnabled := updateItemText(st)

	sessionsLabel := fmt.Sprintf("运行中会话：%d", running)

	application.InvokeAsync(func() {
		t.sessions.SetLabel(sessionsLabel)
		t.relay.SetLabel(relayLabel)
		t.update.SetLabel(updateLabel)
		t.update.SetEnabled(updateEnabled)
	})
}

// updateItemText 决定「更新」那一行显示什么、能不能点。
func updateItemText(st service.UpdateState) (string, bool) {
	switch st.State {
	case service.UpdateStateDisabled:
		// dev 构建（版本号为空）或更新器起不来。留着这一行但点不动，
		// 免得用户以为「检查更新」这个功能被删了。
		return "检查更新（此构建不可用）", false
	case service.UpdateStateChecking:
		return "正在检查更新…", false
	case service.UpdateStateAvailable:
		if st.Skipped {
			return "检查更新", true
		}
		return "新版本 v" + st.Version + " 可用", true
	case service.UpdateStateDownloading:
		return "正在下载更新…", false
	case service.UpdateStateReady:
		return "更新已就绪，重启生效", true
	default:
		// idle / latest / error：都回到「检查更新」，点了就有反馈
		// （要么转圈、要么报错），比按状态灰掉更好懂。
		return "检查更新", true
	}
}

// relayText 把 relay 的状态取值翻成中文。取值定义在 internal/remote/agent.go。
func relayText(state string) string {
	switch state {
	case remote.StateConnecting:
		return "连接中"
	case remote.StateConnected:
		return "已连接"
	case remote.StateError:
		return "错误"
	default:
		return "未启用"
	}
}

// displayVersion 是给托盘和弹窗看的版本号。dev 构建里它是空的，
// 空着显示不如写个 dev。
func displayVersion() string {
	if v := buildinfo.Info().Version; v != "" {
		return v
	}
	return "dev"
}

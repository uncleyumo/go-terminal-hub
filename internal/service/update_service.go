package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/uncleyumo/go-terminal-hub/internal/buildinfo"
	"github.com/uncleyumo/go-terminal-hub/internal/exec/store"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/updater"
	"github.com/wailsapp/wails/v3/pkg/updater/providers/github"
)

// UpdateService 管「应用内更新」这条链路：查版本 → 下载校验 → 重启替换。
//
// 三段里只有「替换」是平台相关的，而它整个在 Wails 更新器里（helper 进程、
// 改名换文件、失败回滚），这里只负责前两段和对外说话。
//
// 为什么不直接把 `wails:updater:*` 那十几条事件透给前端：
// 它们是「机制」，不是「状态」。前端要拿它们拼出一个状态机（收到 verifying
// 之后要记住之前 available 的版本号是多少），托盘也要拼一遍。这里拼一次，
// 推一条 `update:state` 出去，两边都只看这一条。
type UpdateService struct {
	mu      sync.Mutex
	state   UpdateState
	release *updater.Release
	// cancel 取消正在进行的下载。下载是 `DownloadAndInstall(ctx)`，
	// 取消就是取消这个 ctx——更新器没有单独的 Cancel 方法。
	cancel context.CancelFunc

	// OnStateChange 每次状态变化时调用，托盘用它改菜单文字。
	// 由 main.go 注入，和 StoreService.OnSettingsSaved 一个路子。
	OnStateChange func(UpdateState)
}

// 更新源。目前是 GitHub Releases；换自有服务器时只动这里和下面那个 provider。
const (
	updateRepository    = "uncleyumo/go-terminal-hub"
	updateChecksumAsset = "SHA256SUMS"
	updateReleasesPage  = "https://github.com/uncleyumo/go-terminal-hub/releases"
	// updateStateEvent 是推给前端的唯一一条事件名。
	updateStateEvent = "update:state"
	// updateCheckTimeout 是查版本这一步的上限。下载不设上限——12MB 的包在慢网下
	// 可能要几分钟，掐掉反而不如让用户自己点「取消」。
	updateCheckTimeout = 45 * time.Second
)

// UpdateState 的状态取值。前端按这个字符串切界面，托盘按它决定显示哪一行。
const (
	// UpdateStateDisabled：dev 构建（版本号为空）或更新器初始化失败。
	// 这种情况下一律不检查——拿空版本号去比，任何 tag 都会被判成「有新版本」。
	UpdateStateDisabled    = "disabled"
	UpdateStateIdle        = "idle"
	UpdateStateChecking    = "checking"
	UpdateStateLatest      = "latest"
	UpdateStateAvailable   = "available"
	UpdateStateDownloading = "downloading"
	UpdateStateReady       = "ready"
	UpdateStateError       = "error"
)

// UpdateState 是推给前端和托盘的那一份状态快照。
//
// 每次变化推的是**整份**，不是增量：前端只维护一个对象，不用自己拼。
type UpdateState struct {
	State          string `json:"state"`
	CurrentVersion string `json:"currentVersion"`
	// Version / Notes / Size 只在 State 是 available / downloading / ready 时有意义。
	Version string `json:"version"`
	Notes   string `json:"notes"`
	Size    int64  `json:"size"`
	// 下载进度。Written / Total 是字节，Rate 是更新器算好的每秒字节数（近 1 秒平滑值）。
	Written int64   `json:"written"`
	Total   int64   `json:"total"`
	Rate    float64 `json:"rate"`
	// Error 是给用户看的一句话，直接用底层报错原文——里面是 HTTP 状态码、
	// 校验不匹配这类具体信息，比「更新失败」有用。
	Error string `json:"error"`
	// Skipped 为真表示这个版本用户点过「跳过这个版本」。
	Skipped bool `json:"skipped"`
}

// ServiceStartup 在 app.Run() 里跑，这时 application.Get() 已经有效、
// app.Updater 也已经建好（application.New 里建的）。
func (s *UpdateService) ServiceStartup(_ context.Context, _ application.ServiceOptions) error {
	version := buildinfo.Info().Version
	if version == "" {
		// `wails3 dev` 和裸 `go build` 都没有 -ldflags 注入，版本号是空的。
		// 官方更新器对空 CurrentVersion 的处理是「任何合法 tag 都算新」，
		// 那会在开发时每次都弹「发现新版本」。直接关掉。
		slog.Info("updater disabled: build carries no version")
		s.setState(UpdateState{State: UpdateStateDisabled})
		return nil
	}

	provider, err := github.New(github.Config{
		Repository: updateRepository,
		// 配了它，provider 才会去读 Release 里的 SHA256SUMS 并填 Release.Verification。
		// 不配的话两边都是「静默跳过校验」——见 Check 里的第一道断言。
		ChecksumAsset: updateChecksumAsset,
	})
	if err != nil {
		slog.Error("updater: build provider failed", "error", err)
		s.setState(UpdateState{State: UpdateStateDisabled, Error: err.Error()})
		return nil
	}

	app := application.Get()
	if app == nil {
		s.setState(UpdateState{State: UpdateStateDisabled, Error: "application not ready"})
		return nil
	}

	if err := app.Updater.Init(updater.Config{
		CurrentVersion: version,
		Providers:      []updater.Provider{provider},
		// 关掉更新器自带的那个窗口：界面用我们自己的 Vue 弹窗（D61）。
		Window: updater.WindowNone,
	}); err != nil {
		slog.Error("updater: init failed", "error", err)
		s.setState(UpdateState{State: UpdateStateDisabled, Error: err.Error()})
		return nil
	}

	s.subscribe(app)
	slog.Info("updater ready", "version", version, "repository", updateRepository)
	s.setState(UpdateState{State: UpdateStateIdle, CurrentVersion: version})
	return nil
}

// ServiceShutdown 把还在跑的下载掐掉。不掐的话进程退不干净，
// helper 会一直等不到旧进程退出（它有 30 秒超时，超时就放弃替换）。
func (s *UpdateService) ServiceShutdown() error {
	s.CancelDownload()
	return nil
}

// subscribe 把更新器那十几条事件收成一条 update:state。
//
// 事件是 Go 侧发出来的，用 app.Event.On 订阅；回调里拿到的是 event.Data，
// 具体类型按 updater/events.go 的注释：UpdateAvailable / DownloadStarted /
// DownloadComplete / Verifying / Installing / UpdateReady 是 *updater.Release，
// DownloadProgress 是 updater.Progress，Error 是 updater.ErrorInfo，
// CheckStarted / NoUpdate 没有负载。
func (s *UpdateService) subscribe(app *application.App) {
	events := app.Event

	events.On(updater.EventCheckStarted, func(*application.CustomEvent) {
		s.patch(func(st *UpdateState) {
			st.State = UpdateStateChecking
			st.Error = ""
		})
	})

	events.On(updater.EventNoUpdate, func(*application.CustomEvent) {
		s.patch(func(st *UpdateState) {
			st.State = UpdateStateLatest
			st.Version = ""
			st.Notes = ""
			st.Size = 0
			st.Error = ""
		})
	})

	events.On(updater.EventUpdateAvailable, func(e *application.CustomEvent) {
		rel, ok := releaseOf(e)
		if !ok {
			return
		}
		s.patch(func(st *UpdateState) {
			st.State = UpdateStateAvailable
			st.Version = rel.Version
			st.Notes = rel.Notes
			st.Size = rel.Artifact.Size
			st.Skipped = rel.Version == readSkippedVersion()
			st.Error = ""
		})
	})

	events.On(updater.EventDownloadStarted, func(e *application.CustomEvent) {
		rel, ok := releaseOf(e)
		if !ok {
			return
		}
		s.patch(func(st *UpdateState) {
			st.State = UpdateStateDownloading
			st.Version = rel.Version
			st.Size = rel.Artifact.Size
			st.Total = rel.Artifact.Size
			st.Written = 0
			st.Rate = 0
			st.Error = ""
		})
	})

	events.On(updater.EventDownloadProgress, func(e *application.CustomEvent) {
		p, ok := e.Data.(updater.Progress)
		if !ok {
			return
		}
		s.patch(func(st *UpdateState) {
			st.State = UpdateStateDownloading
			st.Written = p.Written
			st.Total = p.Total
			st.Rate = p.Rate
		})
	})

	// 下载完 → 校验 → 装到暂存区，这三步各发一条。前端这时候只需要知道
	// 「还在忙，别点重试」，所以统一显示成 downloading，进度条走满。
	events.On(updater.EventDownloadComplete, func(*application.CustomEvent) {
		s.patch(func(st *UpdateState) {
			st.State = UpdateStateDownloading
			st.Written = st.Total
		})
	})

	events.On(updater.EventVerifying, func(*application.CustomEvent) {
		s.patch(func(st *UpdateState) { st.State = UpdateStateDownloading })
	})

	events.On(updater.EventInstalling, func(*application.CustomEvent) {
		s.patch(func(st *UpdateState) { st.State = UpdateStateDownloading })
	})

	events.On(updater.EventUpdateReady, func(e *application.CustomEvent) {
		rel, ok := releaseOf(e)
		if !ok {
			return
		}
		s.patch(func(st *UpdateState) {
			st.State = UpdateStateReady
			st.Version = rel.Version
			st.Error = ""
		})
	})

	events.On(updater.EventError, func(e *application.CustomEvent) {
		info, ok := e.Data.(updater.ErrorInfo)
		if !ok {
			return
		}
		slog.Warn("updater error", "stage", info.Stage, "message", info.Message)
		s.patch(func(st *UpdateState) {
			st.State = UpdateStateError
			st.Error = info.Message
		})
	})
}

// Check 查一次有没有新版本。manual 只影响日志：自动检查失败是静默的
// （前端拿到 error 状态自己决定要不要弹），手动检查要把错误显示出来。
func (s *UpdateService) Check(manual bool) (UpdateState, error) {
	if s.snapshot().State == UpdateStateDisabled {
		return s.snapshot(), errors.New("updater is disabled in this build")
	}

	app := application.Get()
	if app == nil {
		return s.snapshot(), errors.New("application not ready")
	}

	ctx, cancel := context.WithTimeout(context.Background(), updateCheckTimeout)
	defer cancel()

	rel, err := app.Updater.Check(ctx)
	if err != nil {
		// 断网、GitHub 限流、DNS 挂了都走这儿。自动检查时不该拿这个打扰用户。
		slog.Warn("update check failed", "error", err, "manual", manual)
		return s.patch(func(st *UpdateState) {
			st.State = UpdateStateError
			st.Error = err.Error()
		}), err
	}

	if rel == nil {
		// (nil, nil) = 已是最新。
		s.mu.Lock()
		s.release = nil
		s.mu.Unlock()
		return s.patch(func(st *UpdateState) {
			st.State = UpdateStateLatest
			st.Version = ""
			st.Notes = ""
			st.Size = 0
			st.Error = ""
		}), nil
	}

	// —— 第一道断言（ARCHITECTURE §12.4）——
	// GitHub 源在两种情况下会**静默**把 Verification 留空并返回成功：
	//   1. Release 里根本没有 SHA256SUMS 这个资产（fetchChecksumFor 返回 nil, nil）；
	//   2. 有文件，但里面没有目标 exe 那一行（parseChecksumLine 返回 nil, nil）。
	// 官方文档把 ChecksumAsset 说成「可选的额外校验」，所以这两种情况不算错。
	// 但我们要的是「没校验 = 拒绝」——所以在这里自己把它变成错误。
	if rel.Verification == nil || len(rel.Verification.Digest) == 0 {
		msg := "release " + rel.Version + " carries no SHA-256 checksum, refusing to update"
		slog.Error("update rejected", "version", rel.Version, "reason", msg)
		s.mu.Lock()
		s.release = nil
		s.mu.Unlock()
		return s.patch(func(st *UpdateState) {
			st.State = UpdateStateError
			st.Error = msg
		}), errors.New(msg)
	}

	skipped := readSkippedVersion()
	s.mu.Lock()
	s.release = rel
	s.mu.Unlock()

	return s.patch(func(st *UpdateState) {
		st.State = UpdateStateAvailable
		st.Version = rel.Version
		st.Notes = rel.Notes
		st.Size = rel.Artifact.Size
		st.Skipped = rel.Version == skipped
		st.Error = ""
	}), nil
}

// Install 下载并校验，把新文件装到暂存区。**不替换**——替换在 Restart 里。
//
// 拆开是因为「下载」可以慢慢来、可以取消，「替换」是一瞬间的事但要退出应用。
// 用户点「立即更新」之后还能继续用，点「立即重启并应用」才真的走。
func (s *UpdateService) Install() (UpdateState, error) {
	s.mu.Lock()
	rel := s.release
	s.mu.Unlock()
	if rel == nil {
		return s.snapshot(), errors.New("no release checked yet, call Check first")
	}

	app := application.Get()
	if app == nil {
		return s.snapshot(), errors.New("application not ready")
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.cancel = cancel
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.cancel = nil
		s.mu.Unlock()
		cancel()
	}()

	if err := app.Updater.DownloadAndInstall(ctx); err != nil {
		if errors.Is(err, context.Canceled) {
			// 用户自己点的取消，不是错误。
			slog.Info("update download canceled by user")
			return s.patch(func(st *UpdateState) {
				st.State = UpdateStateAvailable
				st.Written = 0
				st.Error = ""
			}), nil
		}
		slog.Error("update download failed", "error", err)
		return s.patch(func(st *UpdateState) {
			st.State = UpdateStateError
			st.Error = err.Error()
		}), err
	}

	// —— 第二道断言（ARCHITECTURE §12.4）——
	// 更新器下完已经按 Verification 校验过一遍了。这里把**落到暂存区的那份文件**
	// 再读出来自己算一遍 SHA-256。
	//
	// 目的不是「不信任更新器」，而是把两件事钉在一起：校验过的字节，和接下来
	// helper 要替换上去的那个文件，是同一个。中间任何一步（暂存被换、路径搞错、
	// 并发装了两次）在这里都会对不上。
	if err := verifyStagedFile(app.Updater.DownloadedPath(), rel); err != nil {
		slog.Error("staged file failed second verification", "error", err)
		return s.patch(func(st *UpdateState) {
			st.State = UpdateStateError
			st.Error = err.Error()
		}), err
	}

	return s.patch(func(st *UpdateState) {
		st.State = UpdateStateReady
		st.Version = rel.Version
		st.Error = ""
	}), nil
}

// CancelDownload 取消正在进行的下载。
func (s *UpdateService) CancelDownload() {
	s.mu.Lock()
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Restart 起 helper、退出当前进程，让 helper 换掉 exe 再启动新版。
//
// 这个方法**返回之后进程就不受控了**：更新器内部是「先 spawn helper，
// 再 app.Quit()」，Quit 会把整个应用关掉。所以调用方（前端）必须在调它之前
// 把会话停干净——停不干净就不要调（D60）。
//
// 注意它绕过窗口的 WindowClosing 钩子（那个钩子把关闭改成隐藏窗口），
// 走的是 app.Quit()，所以不会被「关闭即隐藏」挡住。
func (s *UpdateService) Restart() error {
	app := application.Get()
	if app == nil {
		return errors.New("application not ready")
	}
	return app.Updater.Restart(context.Background())
}

// SkipVersion 记住「跳过这个版本」，写进 settings.json。
//
// 更新器自己有个 SkipVersion，但它只记在内存里，重启就忘。跨重启要靠自己存。
func (s *UpdateService) SkipVersion(version string) error {
	if version == "" {
		return errors.New("skip version: empty version")
	}
	storeInstance, err := store.GetStore()
	if err != nil {
		return errors.Errorf("skip version: get store: %v", err)
	}
	settings := storeInstance.GetSettings()
	settings.SkippedUpdateVersion = version
	if err := storeInstance.UpdateSettings(settings); err != nil {
		return errors.Errorf("skip version: save settings: %v", err)
	}
	// 也让更新器自己知道，免得它内置的状态和我们对不上。
	if app := application.Get(); app != nil {
		app.Updater.SkipVersion(version)
	}
	s.patch(func(st *UpdateState) { st.Skipped = true })
	return nil
}

// ClearSkippedVersion 让「跳过」失效。目前没接界面，留给「关于」弹窗以后用。
func (s *UpdateService) ClearSkippedVersion() error {
	storeInstance, err := store.GetStore()
	if err != nil {
		return errors.Errorf("clear skipped version: get store: %v", err)
	}
	settings := storeInstance.GetSettings()
	settings.SkippedUpdateVersion = ""
	if err := storeInstance.UpdateSettings(settings); err != nil {
		return errors.Errorf("clear skipped version: save settings: %v", err)
	}
	return nil
}

// GetState 给前端和托盘读当前状态。前端每次打开弹窗先拉一次，
// 免得在它没打开的时候错过的事件导致界面是空的。
func (s *UpdateService) GetState() UpdateState {
	return s.snapshot()
}

// GetReleasesPage 返回 Releases 页地址，前端拿它做「打开下载页」的兜底。
func (s *UpdateService) GetReleasesPage() string {
	return updateReleasesPage
}

// OpenReleasesPage 用系统默认浏览器打开 Releases 页。
// 目录能用 explorer.exe 打开，URL 不行——那个走 rundll32 的 FileProtocolHandler。
func (s *UpdateService) OpenReleasesPage() error {
	cmd := exec.Command("rundll32", "url.dll,FileProtocolHandler", updateReleasesPage)
	if err := cmd.Start(); err != nil {
		return errors.Errorf("open releases page: %v", err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// —— 内部 ——

// snapshot 读一份状态副本。
func (s *UpdateService) snapshot() UpdateState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// patch 基于当前状态改几个字段，存下来并推出去。
func (s *UpdateService) patch(fn func(*UpdateState)) UpdateState {
	s.mu.Lock()
	st := s.state
	s.mu.Unlock()

	fn(&st)
	s.setState(st)
	return st
}

// setState 存一份状态，然后发事件 + 通知托盘。
//
// 推出去的动作放在**锁外面**：事件回调（前端、托盘）可能回头读状态，
// 持着锁发事件就是自己等自己。
func (s *UpdateService) setState(st UpdateState) {
	s.mu.Lock()
	if st.CurrentVersion == "" {
		st.CurrentVersion = s.state.CurrentVersion
	}
	s.state = st
	s.mu.Unlock()

	if app := application.Get(); app != nil {
		app.Event.Emit(updateStateEvent, st)
	}
	if s.OnStateChange != nil {
		s.OnStateChange(st)
	}
}

// releaseOf 从事件里取 Release。取不到就当这条事件没用——
// 类型对不上说明 wails 换了负载，宁可不更新界面也不要崩。
func releaseOf(e *application.CustomEvent) (*updater.Release, bool) {
	if e == nil {
		return nil, false
	}
	rel, ok := e.Data.(*updater.Release)
	if !ok || rel == nil {
		return nil, false
	}
	return rel, true
}

// readSkippedVersion 从 settings.json 读「跳过这个版本」。读不到就当没跳过。
func readSkippedVersion() string {
	storeInstance, err := store.GetStore()
	if err != nil {
		return ""
	}
	return storeInstance.GetSettings().SkippedUpdateVersion
}

// verifyStagedFile 是第二道断言：把暂存区那个文件自己算一遍 SHA-256，
// 和 Check 时拿到的 Verification.Digest 比对。
func verifyStagedFile(path string, rel *updater.Release) error {
	if rel.Verification == nil || len(rel.Verification.Digest) == 0 {
		return errors.New("no checksum to verify against")
	}
	if path == "" {
		return errors.New("updater produced no staged file")
	}

	f, err := os.Open(path)
	if err != nil {
		return errors.Errorf("open staged file: %v", err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return errors.Errorf("hash staged file: %v", err)
	}
	got := h.Sum(nil)
	if !bytes.Equal(got, rel.Verification.Digest) {
		return errors.Errorf(
			"staged file digest mismatch: got %s, want %s",
			hex.EncodeToString(got), hex.EncodeToString(rel.Verification.Digest),
		)
	}

	slog.Info("update verified",
		"file", filepath.Base(path),
		"sha256", hex.EncodeToString(got),
	)
	return nil
}

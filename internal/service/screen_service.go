package service

import (
	"bytes"
	"encoding/base64"
	"image/png"
	"log/slog"
	"time"

	"github.com/kbinani/screenshot"
	"github.com/pkg/errors"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// screenHideSettle 是「隐藏窗口 → 截图」之间等的那一下。
// 隐藏之后桌面要把那一块重画，立刻截会截到还没画完的半截。
const screenHideSettle = 160 * time.Millisecond

// ScreenService 给「屏幕取色」用。
//
// 为什么不能在网页里直接取：浏览器的 `EyeDropper` API 只有 Chrome/Edge **浏览器**
// 实现了——取色镜那一层 UI 要靠「宿主程序」提供，WebView2 没接这个口子。
// 所以 `new EyeDropper().open()` 在 WebView2 里会**毫秒级**抛 AbortError，
// 镜面根本不出来（接口在、功能废：`window.EyeDropper` 是 defined 的，
// 靠 typeof 做特性检测查不出来）。`<input type="color">` 的取色镜同理，
// 它走的是浏览器自己那套 UI，WebView2 一样没有。
//
// 所以只能自己来：
//   1. Go 侧把屏幕抓成一张 PNG 交给前端；
//   2. 主窗口铺满屏幕、置顶，前端把这张图铺上去；
//   3. 用户点到哪，前端从图里读那个像素——图就是屏幕，读图等于读屏。
//
// 第 3 步在前端做（canvas getImageData），因为缩放 / DPI 的换算在网页里
// 天然是「CSS 像素 → 图片像素」一次乘法，比绕回 Go 再换一次坐标系简单得多。
type ScreenService struct {
	// Window 由 main.go 注入：窗口是在 `application.New` 之后才建的，
	// 而 Services 是 `New` 的参数，所以只能建完窗口再回填。
	Window *application.WebviewWindow
}

// BeginScreenPick 抓一张屏幕图、把主窗口铺满屏幕并置顶，返回图片的 data URL。
//
// 用 data URL 而不是临时文件：这张图用完就丢，落盘还要管清理，
// 而且前端的 canvas 加一个 img 标签就能读像素。
func (s *ScreenService) BeginScreenPick() (string, error) {
	if s.Window == nil {
		return "", errors.New("screen pick: window not ready")
	}

	// 先把自己的窗口藏起来再截。
	//
	// 不藏的话，截到的是「被取色弹窗和它的半透明遮罩盖住的那一屏」——
	// 中间一大片是压暗、模糊过的，在那儿取出来的颜色是错的。
	// 等一小会儿是给桌面一点时间重绘干净（窗口隐藏之后那一块要重画）。
	s.Window.Hide()
	time.Sleep(screenHideSettle)

	// 只抓「主显示器」。多显示器：窗口的 Fullscreen 也只铺一个显示器，
	// 两边取同一个范围才对得上；要支持多屏得先解决「窗口怎么跨屏铺」。
	bounds := screenshot.GetDisplayBounds(0)
	img, err := screenshot.CaptureRect(bounds)

	// 不管截没截到，窗口都得回来——否则用户面前什么都不剩。
	s.Window.Show()
	if err != nil {
		return "", errors.Errorf("capture screen: %v", err)
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return "", errors.Errorf("encode screen: %v", err)
	}

	// 抓完再铺满：顺序反过来的话，抓到的就是「铺满之后」的那一屏，
	// 图里全是自己的窗口，什么也取不到。
	s.Window.Fullscreen()
	s.Window.SetAlwaysOnTop(true)

	slog.Info("screen pick started", "width", bounds.Dx(), "height", bounds.Dy())
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

// EndScreenPick 还原窗口。取色完、按 Esc、按右键都要走这里——
// 不还原的话用户会被留在一个全屏置顶的窗口里，只能杀进程。
func (s *ScreenService) EndScreenPick() error {
	if s.Window == nil {
		return nil
	}
	s.Window.SetAlwaysOnTop(false)
	s.Window.UnFullscreen()
	return nil
}

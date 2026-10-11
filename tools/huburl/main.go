// huburl 打印「带令牌的远程访问地址」，给手机端用。
//
// 为什么需要它：手机浏览器和「把网页包成 App」的工具都记不住令牌，每次打开都要
// 手输一长串。把令牌放进 URL 的 fragment（#token=...）就免输了 —— fragment 不会
// 发给服务器，所以不会进 Caddy 的访问日志，也不会出现在 Referer 里。
//
// 为什么是 fragment 而不是查询串（?token=...）：查询串会随请求发给服务器，
// 上面那两处就都躲不掉了。查询串前端也认，那是给会吃掉 fragment 的封装工具留的后路。
//
// 令牌从桌面端的设置里读，不另存一份：settings.json 的 remote.token 是唯一真源，
// 在桌面端换过令牌之后这里自动跟上。
// ⚠️ 路径规则和 internal/exec/store/store.go 的 loadSettingsPath 是同一套。
// 没直接 import 那个包，是因为 GetStore() 会起后台写盘的 goroutine ——
// 一个只读的 CLI 不该顺手改写桌面端的设置文件。
//
// 用法（工作目录不限，路径相对模块根解析）：
//
//	go run ./tools/huburl
//	go run ./tools/huburl -token xxxx                  # 临时换一个令牌
//	go run ./tools/huburl -base https://example.com/   # 临时换一台服务器
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type settings struct {
	Remote struct {
		URL   string `json:"url"`
		Token string `json:"token"`
	} `json:"remote"`
}

func main() {
	base := flag.String("base", "", "覆盖服务器地址（默认从桌面端的「远程访问」里推）")
	token := flag.String("token", "", "覆盖令牌（默认从桌面端的设置里读）")
	flag.Parse()

	s, path := loadSettings()

	tok := *token
	if tok == "" {
		tok = s.Remote.Token
	}
	if tok == "" {
		fail("桌面端的设置里没有令牌。\n  文件：%s\n  先在桌面端打开「远程访问」填好令牌，再跑一次。", path)
	}

	b := *base
	if b == "" {
		var err error
		b, err = browserBase(s.Remote.URL)
		if err != nil {
			fail("%v\n  文件：%s", err, path)
		}
	}

	fmt.Printf("%s#token=%s\n", b, tok)
}

// browserBase 把 agent 用的 WebSocket 地址换成浏览器要打开的网页地址。
//
// 两者只差协议和路径：agent 拨的是 wss://host/ws/agent，网页挂在 https://host/。
func browserBase(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("桌面端的「远程访问」里还没填服务器地址")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("服务器地址解析不了：%s", raw)
	}
	switch u.Scheme {
	case "wss":
		u.Scheme = "https"
	case "ws":
		u.Scheme = "http"
	case "https", "http":
	default:
		return "", fmt.Errorf("服务器地址要以 wss:// 或 ws:// 开头，现在是 %q", u.Scheme)
	}
	if u.Host == "" {
		return "", fmt.Errorf("服务器地址里没有主机名：%s", raw)
	}
	// 路径丢掉：网页挂在根上，/ws/agent 是 agent 专用的
	return u.Scheme + "://" + u.Host + "/", nil
}

func settingsPath() string {
	if dir := os.Getenv("GO_TERMINAL_HUB_DIR"); dir != "" {
		return filepath.Join(dir, "settings.json")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".go-terminal-hub", "settings.json")
	}
	return filepath.Join(".go-terminal-hub", "settings.json")
}

func loadSettings() (settings, string) {
	path := settingsPath()
	raw, err := os.ReadFile(path)
	if err != nil {
		fail("读不到桌面端的设置：%v\n  文件：%s\n  先在桌面端打开一次「远程访问」，填好地址和令牌。", err, path)
	}
	var s settings
	if err := json.Unmarshal(raw, &s); err != nil {
		fail("设置文件解析不了：%v\n  文件：%s", err, path)
	}
	return s, path
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "huburl: "+format+"\n", args...)
	os.Exit(1)
}

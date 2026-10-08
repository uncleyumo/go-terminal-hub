package remote

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/pkg/errors"
)

// ProbeTimeout 是一次试拨的总时限。手机上等超过十秒就会以为卡死了。
const ProbeTimeout = 10 * time.Second

// NormalizeURL 把用户可能写错的地址收拢成 WebSocket 地址。
//
// 界面上那一栏叫「relay 地址」，很容易被填成 https://域名 —— 那是个网页地址，
// 拨不过去，报出来的错又只有一句握手失败。这里顺手换掉。
func NormalizeURL(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", errors.New("地址是空的")
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", errors.Errorf("地址解析不了: %s", raw)
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	case "ws", "wss":
	default:
		return "", errors.Errorf("地址要以 wss:// 或 ws:// 开头，现在是 %q", u.Scheme)
	}
	if u.Host == "" {
		return "", errors.New("地址里没有域名")
	}
	return u.String(), nil
}

// Probe 拨一次 relay 看能不能连上，连上就立刻正常关闭。
//
// 拨得动 = 地址通 + 令牌对。relay 是**先校验令牌再升级连接**的
// （remote/relay/main.go 里鉴权在 websocket.Accept 之前），
// 令牌不对会直接回 401，Dial 当场报错。
func Probe(ctx context.Context, rawURL, token string) error {
	target, err := NormalizeURL(rawURL)
	if err != nil {
		return err
	}
	if strings.TrimSpace(token) == "" {
		return errors.New("令牌是空的")
	}

	ctx, cancel := context.WithTimeout(ctx, ProbeTimeout)
	defer cancel()

	ws, _, err := websocket.Dial(ctx, target, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + token}},
	})
	if err != nil {
		return err
	}
	// 用正常关闭码，别用 CloseNow：后者在对端日志里会记成异常断开。
	// Close 自带超时，不会把这里挂住。
	_ = ws.Close(websocket.StatusNormalClosure, "probe")
	return nil
}

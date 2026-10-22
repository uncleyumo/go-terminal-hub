package remote

import (
	"strings"
	"testing"
	"time"

	"github.com/uncleyumo/go-terminal-hub/internal/exec/hub"
	"github.com/uncleyumo/go-terminal-hub/remote/protocol"
)

// TestBrowserOnlineReachesAgentStatus 中心站说「浏览器端有人」，
// 桌面界面上那行「远程端在线」就得跟着变。
//
// 少了这一步，那行字不是偶尔不准，是从来没准过——
// 桥那边明明知道有人连着，却没往上报。
func TestBrowserOnlineReachesAgentStatus(t *testing.T) {
	h := hub.NewHub()
	srv, conns := startPeer(t)

	agent := NewAgent(h, nil)
	t.Cleanup(agent.Stop)

	agent.Apply(Config{
		Enabled: true,
		URL:     "ws" + strings.TrimPrefix(srv.URL, "http"),
		Key:     "irrelevant-in-this-test",
	})

	var p *peer
	select {
	case p = <-conns:
	case <-time.After(5 * time.Second):
		t.Fatal("5s 内没等到 agent 拨入")
	}

	p.send(protocol.Message{Type: protocol.TypeBrowserOnline})

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if agent.Status().BrowserOnline {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !agent.Status().BrowserOnline {
		t.Fatal("收到 browser_online 之后，状态里 BrowserOnline 还是 false")
	}

	// 人走了也得能改回去，不然「在线」这两个字会一直亮着
	p.send(protocol.Message{Type: protocol.TypeBrowserOffline})

	for time.Now().Before(deadline) {
		if !agent.Status().BrowserOnline {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("收到 browser_offline 之后，状态里 BrowserOnline 还是 true")
}

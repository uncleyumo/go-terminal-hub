package remote

import "testing"

func TestNormalizeURL(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"原样的 wss", "wss://hub.example.com/ws/agent", "wss://hub.example.com/ws/agent", false},
		{"原样的 ws", "ws://127.0.0.1:8080/ws/agent", "ws://127.0.0.1:8080/ws/agent", false},
		// 界面上那栏叫「relay 地址」，很容易被填成网页地址，得顺手换掉
		{"https 换成 wss", "https://hub.example.com/ws/agent", "wss://hub.example.com/ws/agent", false},
		{"http 换成 ws", "http://127.0.0.1:8080/ws/agent", "ws://127.0.0.1:8080/ws/agent", false},
		{"两头空格", "  wss://hub.example.com/ws/agent  ", "wss://hub.example.com/ws/agent", false},
		{"没写协议", "hub.example.com/ws/agent", "", true},
		{"空的", "", "", true},
		{"只有空格", "   ", "", true},
		{"别的协议", "ftp://hub.example.com", "", true},
		{"有协议没域名", "wss://", "", true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := NormalizeURL(c.in)
			if c.wantErr {
				if err == nil {
					t.Fatalf("NormalizeURL(%q) = %q, want error", c.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeURL(%q): %v", c.in, err)
			}
			if got != c.want {
				t.Errorf("NormalizeURL(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestProbeRejectsEmptyToken(t *testing.T) {
	// 令牌为空时不该真的去拨——那会白等十秒超时，用户还看不出是为什么
	if err := Probe(t.Context(), "wss://hub.example.com/ws/agent", "  "); err == nil {
		t.Fatal("空令牌应当当场报错")
	}
}

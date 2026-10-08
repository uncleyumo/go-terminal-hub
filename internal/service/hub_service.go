package service

import (
	"errors"

	"github.com/uncleyumo/go-terminal-hub/internal/exec/hub"
)

type HubService struct {
}

func (x *HubService) ListStatus() []hub.RecordStatus {
	h := hub.GetHub()
	return h.ListRecordStatus()
}

func (x *HubService) StartSession(id string) error {
	h := hub.GetHub()
	return h.StartSession(id)
}

func (x *HubService) StopSession(id string) error {
	h := hub.GetHub()
	return h.StopSession(id)
}

func (x *HubService) RestartSession(id string) error {
	h := hub.GetHub()
	return h.RestartSession(id)
}

func (x *HubService) WriteSession(id string, data string) (int, error) {
	h := hub.GetHub()
	return h.WriteSession(id, data)
}

func (x *HubService) ResizeSession(id string, cols, rows uint16) error {
	h := hub.GetHub()
	return h.ResizeSession(id, cols, rows)
}

func (x *HubService) StopAllSessions() error {
	h := hub.GetHub()
	return h.StopAllSessions()
}

// Controller 返回会话当前的控制方："desktop" 或 "web"。
func (x *HubService) Controller(id string) (string, error) {
	h := hub.GetHub()
	who, ok := h.Controller(id)
	if !ok {
		return "", errors.New("record not found for id: " + id)
	}
	return who, nil
}

// TakeControl 是桌面端「收回控制」按钮走的路径。
//
// cols/rows 必须显式传：桌面在旁观状态下容器是按手机的网格撑开的，
// 得先恢复自己的布局、量出真实尺寸，再连同请求一起交过来。
func (x *HubService) TakeControl(id string, cols, rows uint16) error {
	h := hub.GetHub()
	return h.SetController(id, hub.SourceDesktop, cols, rows)
}

// ReleaseControl 把控制权让给 web 端，PTY 切到 web 上次报的尺寸。
// 主要给测试和调试用；正常流程里是手机端点「接管」触发的。
func (x *HubService) ReleaseControl(id string) error {
	h := hub.GetHub()
	return h.SetController(id, hub.SourceWeb, 0, 0)
}

package sink

import (
	"log/slog"

	"github.com/uncleyumo/go-terminal-hub/internal/exec/executor"
	"github.com/wailsapp/wails/v3/pkg/application"
)

type EmitSink struct {
	App *application.App
	Id  string // which session this sink belongs to
}

// OutputPayload 是 "session:output" 事件的数据类型。
// 导出是因为 main.go 的 init() 里要把它注册给 RegisterEvent——
// wails 的事件数据是精确类型匹配的，注册的类型必须和 Emit 传的类型一模一样，
// 否则事件会被 Cancel 掉，前端一条也收不到。
type OutputPayload struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// ExitPayload 是 "session:exited" 事件的数据类型。
// 不能直接发 executor.ExitResult——它的 Err 字段是 error 接口，不能过 JSON。
type ExitPayload struct {
	ID     string `json:"id"`
	Code   int    `json:"code"`
	ErrMsg string `json:"errMsg"`
}

func (e *EmitSink) OnOutput(chunk []byte) {
	slog.Debug("process output", "text", string(chunk))
	e.App.Event.Emit("session:output", OutputPayload{
		ID:   e.Id,
		Text: string(chunk),
	})
}

func (e *EmitSink) OnProcessExited(res executor.ExitResult) {
	if res.Err != nil {
		slog.Error("process exited with error", "err", res.Err)
		e.App.Event.Emit("session:exited", ExitPayload{
			ID:     e.Id,
			Code:   res.Code,
			ErrMsg: res.Err.Error(),
		})
		return
	}
	slog.Info("process exited", "code", res.Code)
	e.App.Event.Emit("session:exited", ExitPayload{
		ID:     e.Id,
		Code:   res.Code,
		ErrMsg: "",
	})
}

func (e *EmitSink) OnStarted(id string) {
	slog.Debug("process started")
	e.App.Event.Emit("session:started", id)
}

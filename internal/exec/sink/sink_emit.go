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

type outputPayload struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type exitPayload struct {
	ID     string `json:"id"`
	Code   int    `json:"code"`
	ErrMsg string `json:"errMsg"`
}

func (e *EmitSink) OnOutput(chunk []byte) {
	slog.Debug("process output", "text", string(chunk))
	e.App.Event.Emit("session:output", outputPayload{
		ID:   e.Id,
		Text: string(chunk),
	})
}

func (e *EmitSink) OnProcessExited(res executor.ExitResult) {
	if res.Err != nil {
		slog.Error("process exited with error", "err", res.Err)
		e.App.Event.Emit("session:exited", exitPayload{
			ID:     e.Id,
			Code:   res.Code,
			ErrMsg: res.Err.Error(),
		})
		return
	}
	slog.Info("process exited", "code", res.Code)
	e.App.Event.Emit("session:exited", exitPayload{
		ID:     e.Id,
		Code:   res.Code,
		ErrMsg: "",
	})
}

func (e *EmitSink) OnStarted(id string) {
	slog.Debug("process started")
	e.App.Event.Emit("session:started", id)
}

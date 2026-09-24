package main

import (
	"log/slog"

	"github.com/uncleyumo/go-terminal-hub/internal/exec/executor"
	"github.com/wailsapp/wails/v3/pkg/application"
)

type emitSink struct {
	app *application.App
	id  string // which session this sink belongs to
}

type outputPayload struct {
	ID   string
	Text string
}

type exitPayload struct {
	ID     string
	Code   int
	ErrMsg string
}

func (e *emitSink) OnOutput(chunk []byte) {
	slog.Info("process output", "text", string(chunk))
	e.app.Event.Emit("session:output", outputPayload{
		ID:   e.id,
		Text: string(chunk),
	})
}

func (e *emitSink) OnProcessExited(res executor.ExitResult) {
	if res.Err != nil {
		slog.Error("process exited with error", "err", res.Err)
		e.app.Event.Emit("session:exited", exitPayload{
			ID:     e.id,
			Code:   res.Code,
			ErrMsg: res.Err.Error(),
		})
		return
	}
	slog.Info("process exited", "code", res.Code)
	e.app.Event.Emit("session:exited", exitPayload{
		ID:     e.id,
		Code:   res.Code,
		ErrMsg: "",
	})
}

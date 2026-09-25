package main

import (
	"fmt"
	"log/slog"
	"sync"

	"github.com/uncleyumo/go-terminal-hub/internal/exec/conpty"
	"github.com/uncleyumo/go-terminal-hub/internal/exec/executor"
	"github.com/uncleyumo/go-terminal-hub/internal/exec/session"
	"github.com/wailsapp/wails/v3/pkg/application"
)

type XtermDemoService struct {
	sess *session.Session
	mu   sync.Mutex
}

func (x *XtermDemoService) StartSession() error {
	x.mu.Lock()
	defer x.mu.Unlock()

	if x.sess != nil {
		slog.Warn("Session already started")
		return nil
	}

	app := application.Get()
	sink := NewBatchingSink(&emitSink{app, "demo"})
	s := session.NewSession("demo", &conpty.ConPTYExecutor{}, sink)
	command, err := executor.BuildCommand(
		executor.Entry{
			Kind:   "bat",
			Target: "E:\\Dev_work\\Go_Dev\\go_projects\\go-terminal-hub\\test\\demo\\test.bat",
		})
	if err != nil {
		slog.Error("Failed to build command", "error", err)
		return err
	}
	err = s.Start(executor.LaunchSpec{
		Path:    command.Path,
		Command: command.CmdLine,
		WorkDir: "",
		Env:     nil,
		Cols:    80,
		Rows:    25,
	})
	if err != nil {
		slog.Error("Failed to start session", "error", err)
		return err
	}
	x.sess = s
	return nil
}

func (x *XtermDemoService) StopSession() error {
	x.mu.Lock()
	defer x.mu.Unlock()

	if x.sess == nil {
		slog.Warn("Session is nil")
		return fmt.Errorf("you have not started a session")
	}

	x.sess.Stop()
	x.sess = nil
	return nil
}

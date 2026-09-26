package main

import "github.com/uncleyumo/go-terminal-hub/internal/exec/hub"

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

package main

import (
	"github.com/google/uuid"
	"github.com/pkg/errors"
	"github.com/uncleyumo/go-terminal-hub/internal/exec/hub"
	"github.com/uncleyumo/go-terminal-hub/internal/exec/store"
)

type StoreService struct {
}

func (s *StoreService) ListSessions() ([]store.DataStore, error) {
	useStore, err := store.GetStore()
	if err != nil {
		return nil, err
	}
	return useStore.List(), nil
}

func (s *StoreService) GetSession(id string) (store.DataStore, error) {
	useStore, err := store.GetStore()
	if err != nil {
		return store.DataStore{}, err
	}
	dataStore, b := useStore.GetOne(id)
	if !b {
		return store.DataStore{}, errors.Errorf("can't find session %s", id)
	}
	return dataStore, nil
}

func (s *StoreService) CreateSession(d store.DataStore) (string, error) {
	useStore, err := store.GetStore()
	if err != nil {
		return "", errors.Errorf("get store error when create it: %v", err)
	}
	uuid7, err := uuid.NewV7()
	if err != nil {
		return "", errors.Errorf("create uuid v7 error: %v", err)
	}
	d.ID = uuid7.String()
	if err := useStore.Add(d); err != nil {
		return "", errors.Errorf("add session error: %v", err)
	}
	return d.ID, nil
}

func (s *StoreService) UpdateSession(id string, d store.DataStore) (string, error) {
	useStore, err := store.GetStore()
	if err != nil {
		return "", errors.Errorf("get store error when update it: %v", err)
	}
	if err := useStore.Update(id, d); err != nil {
		return "", errors.Errorf("update session error: %v", err)
	}
	return d.ID, nil
}

func (s *StoreService) DeleteSession(id string) error {
	err := hub.GetHub().Remove(id)
	if err != nil {
		return errors.Errorf("remove session from hub error: %v", err)
	}
	useStore, err := store.GetStore()
	if err != nil {
		return errors.Errorf("get store error when delete it: %v", err)
	}
	if err := useStore.Remove(id); err != nil {
		return errors.Errorf("delete session error: %v", err)
	}
	return nil
}

func (s *StoreService) GetSettings() (store.Settings, error) {
	useStore, err := store.GetStore()
	if err != nil {
		return store.Settings{}, errors.Errorf("get store error when get settings: %v", err)
	}
	settings := useStore.GetSettings()
	return settings, nil
}

func (s *StoreService) SaveSettings(settings store.Settings) error {
	useStore, err := store.GetStore()
	if err != nil {
		return errors.Errorf("get store error when save settings: %v", err)
	}
	if err := useStore.UpdateSettings(settings); err != nil {
		return errors.Errorf("update settings error: %v", err)
	}
	return nil
}

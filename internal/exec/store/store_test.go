package store

import (
	"os"
	"testing"
)

const testDir = "E:\\Dev_work\\Go_Dev\\go_projects\\go-terminal-hub\\tmp"

func TestGetStore(t *testing.T) {
	if err := os.Setenv("GO_TERMINAL_HUB_DIR", testDir); err != nil {
		t.Fatal("Failed to set environment variable", err)
	}
	store, err := GetStore()
	if err != nil {
		t.Fatal("Failed to get store", err)
	}
	t.Log("Store data file path:", store.dataFilePath)
	for key := range store.dataList {
		t.Log("Store data:", key, store.dataList[key])
	}
}

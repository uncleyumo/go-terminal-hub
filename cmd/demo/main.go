package main

import (
	"io"
	"log"
	"os"
	"time"

	"github.com/aymanbagabas/go-pty"
)

const (
	terminalExecutor = "cmd.exe"
	grandchildPath   = "E:\\Dev_work\\Go_Dev\\go_projects\\go-terminal-hub\\cmd\\demo\\grandchild.bat"
)

func main() {
	log.Println("demo is starting.......")
	p, err := pty.New()
	if err != nil {
		log.Fatal("failed to create pty:", err)
	}
	cmd := p.Command(terminalExecutor, "/c", grandchildPath)
	log.Printf("starting %v\n", cmd.Args)
	// the command actually begins to be executed here(cmd.Start)
	if err := cmd.Start(); err != nil {
		log.Fatal("failed to start command:", err)
	}
	log.Printf("process started with pid %d\n", cmd.Process.Pid)
	go func() {
		_, err2 := io.Copy(os.Stdout, p)
		if err2 != nil {
			log.Println("err occurred when copying pty output:", err2)
		}
	}()
	time.Sleep(2 * time.Second)
	log.Printf("closing pty\n")
	if err := p.Close(); err != nil {
		log.Println("err occurred when closing pty:", err)
	}
	log.Printf("process exited with status %d\n", cmd.ProcessState.ExitCode())
}

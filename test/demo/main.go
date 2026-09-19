package main

import (
	"flag"
	"io"
	"log"
	"os"

	"github.com/aymanbagabas/go-pty"
	"golang.org/x/sys/windows"
)

const (
	terminalExecutor = "cmd.exe"
	grandchildPath   = "E:\\Dev_work\\Go_Dev\\go_projects\\go-terminal-hub\\cmd\\demo\\grandchild.bat"
)

func main() {
	resizeFlag := flag.Bool("resize", false, "resize the terminal")
	flag.Parse()
	log.Println("demo is starting...")
	p, _ := pty.New()
	command := p.Command(terminalExecutor, "/c", "mode", "con")
	ch := make(chan int, 1)
	go func(ch chan int) {
		written, err := io.Copy(os.Stdout, p)
		if err != nil {
			log.Println("[io.Copy]: occurred an error:", err)
		}
		log.Println("[io.Copy]: written", written, "bytes")
		ch <- 1
	}(ch)
	if *resizeFlag {
		if err := p.Resize(110, 24); err != nil {
			log.Println("[p.Resize]: occurred an error:", err)
		}
	}
	if err := command.Start(); err != nil {
		log.Println("[command.Start]: occurred an error:", err)
	}
	if err := command.Wait(); err != nil {
		log.Println("[command.Wait]: occurred an error:", err)
	}
	log.Println("command has finished")

	// uintptr -> windows handle
	handle := windows.Handle(p.Fd())
	windows.ClosePseudoConsole(handle)
	<-ch
	v, ok := p.(pty.ConPty)
	if !ok {
		log.Println("[p.(pty.ConPty)]: occurred an error: p is not a ConPty")
		return
	}
	_ = v.InputPipe().Close()
	_ = v.OutputPipe().Close()
	log.Println("demo is ended.")
}

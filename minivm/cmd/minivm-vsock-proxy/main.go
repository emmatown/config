package main

import (
	"github.com/emmatown/config/minivm/internal/vsock"
	"flag"
	"fmt"
	"io"
	"os"
)

func run() error {
	socket := flag.String("socket", "", "Cloud Hypervisor vsock mux socket")
	flag.Parse()
	c, reader, err := vsock.Connect(*socket)
	if err != nil {
		return err
	}
	defer c.Close()
	done := make(chan error, 2)
	go func() {
		_, err := io.Copy(c, os.Stdin)
		done <- err
	}()
	go func() {
		_, err := io.Copy(os.Stdout, reader)
		done <- err
	}()
	return <-done
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

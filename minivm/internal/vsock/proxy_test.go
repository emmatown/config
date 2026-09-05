package vsock

import (
	"bufio"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestHandshakePreservesBufferedSSHBytes(t *testing.T) {
	// Keep below macOS's short Unix-socket path limit.
	directory, err := os.MkdirTemp("", "mv-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	socket := filepath.Join(directory, "vsock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan error, 1)
	go func() {
		c, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer c.Close()
		request, err := bufio.NewReader(c).ReadString('\n')
		if err != nil {
			done <- err
			return
		}
		if request != "CONNECT 22\n" {
			t.Errorf("unexpected request %q", request)
		}
		_, err = io.WriteString(c, "OK 1234\nSSH-2.0-test\r\n")
		done <- err
	}()
	c, reader, err := Connect(socket)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	banner, err := reader.ReadString('\n')
	if err != nil || banner != "SSH-2.0-test\r\n" {
		t.Fatalf("banner lost: %q, %v", banner, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

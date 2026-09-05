package vsock

import (
	"bufio"
	"errors"
	"io"
	"net"
	"strings"
	"time"
)

// Connect consumes the Cloud Hypervisor mux handshake without consuming SSH data.
func Connect(socket string) (net.Conn, *bufio.Reader, error) {
	c, err := net.DialTimeout("unix", socket, 10*time.Second)
	if err != nil {
		return nil, nil, err
	}
	if err := c.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		c.Close()
		return nil, nil, err
	}
	if _, err := io.WriteString(c, "CONNECT 22\n"); err != nil {
		c.Close()
		return nil, nil, err
	}
	reader := bufio.NewReaderSize(c, 128)
	line, err := reader.ReadSlice('\n')
	if err != nil || !strings.HasPrefix(string(line), "OK ") {
		c.Close()
		return nil, nil, errors.New("guest SSH connection rejected")
	}
	if err := c.SetDeadline(time.Time{}); err != nil {
		c.Close()
		return nil, nil, err
	}
	return c, reader, nil
}

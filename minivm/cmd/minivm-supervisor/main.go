package main

import (
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/emmatown/config/minivm/internal/model"
	"github.com/emmatown/config/minivm/internal/supervisor"
)

func run() error {
	socket := flag.String("socket", "", "root-owned Unix socket path")
	catalogPath := flag.String("catalog", "", "trusted catalog JSON")
	root := flag.String("state-dir", "/var/lib/vm-state", "root-owned state directory")
	flag.Parse()
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		return errors.New("supervisor requires root on a Linux Btrfs/KVM host")
	}
	if *socket == "" || *catalogPath == "" {
		return errors.New("socket and catalog required")
	}
	catalog, err := model.LoadCatalog(*catalogPath)
	if err != nil {
		return err
	}
	host := &supervisor.Host{Root: *root, Catalog: catalog}
	if err := host.Init(); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(*root, "supervisor.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errors.New("another supervisor owns this state directory")
	}
	// The systemd RuntimeDirectory is root-owned; never remove a non-socket file.
	if info, err := os.Lstat(*socket); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return errors.New("refusing to replace non-socket")
		}
		if err := os.Remove(*socket); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	listener, err := net.Listen("unix", *socket)
	if err != nil {
		return err
	}
	defer listener.Close()
	group, err := user.LookupGroup("minivm-control")
	if err != nil {
		return err
	}
	gid, err := strconv.Atoi(group.Gid)
	if err != nil {
		return err
	}
	if err := os.Chown(*socket, 0, gid); err != nil {
		return err
	}
	if err := os.Chmod(*socket, 0660); err != nil {
		return err
	}
	server := &http.Server{Handler: host.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384}
	return server.Serve(listener)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

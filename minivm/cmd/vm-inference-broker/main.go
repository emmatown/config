package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/emmatown/config/minivm/internal/broker"
)

func main() {
	listen := flag.String("listen", "192.168.126.1:8080", "dedicated development-link listener")
	state := flag.String("state-dir", "/var/lib/codex-broker", "private broker state directory")
	peer := flag.String("peer", "192.168.126.2", "allowed development VM source IP")
	concurrency := flag.Int("concurrency", 2, "maximum simultaneous requests")
	flag.Parse()
	lock, err := os.OpenFile(filepath.Join(*state, "broker.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		log.Fatal("cannot open broker lock")
	}
	defer lock.Close()
	if syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		log.Fatal("another broker owns this state directory")
	}
	handler, err := broker.New(broker.Config{
		AuthFile: filepath.Join(*state, "auth.json"),
		PeerIP:   *peer, Concurrency: *concurrency,
	})
	if err != nil {
		log.Fatal("invalid broker configuration or state")
	}
	server := &http.Server{
		Addr: *listen, Handler: handler, ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 30 * time.Second, WriteTimeout: 16 * time.Minute,
		IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16384,
	}
	log.Print("inference broker listening; no request bodies or credentials are logged")
	log.Fatal(server.ListenAndServe())
}

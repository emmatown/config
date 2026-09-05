package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/emmatown/config/minivm/internal/api"
	"github.com/emmatown/config/minivm/internal/model"
	"github.com/emmatown/config/minivm/internal/store"
)

func run() error {
	listen := flag.String("listen", "127.0.0.1:9080", "loopback listen address")
	database := flag.String("database", "", "SQLite database path")
	catalogPath := flag.String("catalog", "", "trusted template catalog JSON")
	tokenPath := flag.String("token-file", "", "bootstrap token file")
	socket := flag.String("supervisor-socket", "", "optional restricted host socket")
	budget := flag.Uint("memory-budget-mib", 20480, "instance memory budget including VMM overhead")
	flag.Parse()
	if *database == "" || *catalogPath == "" || *tokenPath == "" || *budget > 20480 {
		return errors.New("database, catalog and token-file required; memory budget must be <=20480 MiB")
	}
	host, _, err := net.SplitHostPort(*listen)
	if err != nil || !net.ParseIP(host).IsLoopback() {
		return errors.New("bootstrap API requires a numeric loopback address")
	}
	data, err := os.ReadFile(*tokenPath)
	if err != nil {
		return err
	}
	token := strings.TrimSpace(string(data))
	if len(token) < 32 {
		return errors.New("token must contain at least 32 characters")
	}
	catalog, err := model.LoadCatalog(*catalogPath)
	if err != nil {
		return err
	}
	db, err := store.Open(*database, uint32(*budget))
	if err != nil {
		return err
	}
	defer db.Close()
	app := &api.App{Store: db, Catalog: catalog, Token: token}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	workerDone := make(chan struct{})
	if *socket != "" {
		go func() {
			defer close(workerDone)
			app.Work(ctx, *socket)
		}()
	} else {
		close(workerDone)
	}
	server := &http.Server{Addr: *listen, Handler: app.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	log.Printf("bootstrap API listening on %s", *listen)
	err = server.ListenAndServe()
	stop()
	<-workerDone
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

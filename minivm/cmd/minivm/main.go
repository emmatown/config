package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/emmatown/config/minivm/internal/model"
)

func run() error {
	global := flag.NewFlagSet("minivm", flag.ContinueOnError)
	base := global.String("url", "http://127.0.0.1:9080", "controller URL")
	tokenFile := global.String("token-file", os.Getenv("MINIVM_TOKEN_FILE"), "token file")
	if err := global.Parse(os.Args[1:]); err != nil {
		return err
	}
	args := global.Args()
	if len(args) == 0 {
		return errors.New("expected templates, list, inspect, operation, create, start, stop or delete")
	}
	u, err := url.Parse(*base)
	if err != nil {
		return err
	}
	ip := net.ParseIP(u.Hostname())
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" || !(u.Scheme == "https" || u.Scheme == "http" && (u.Hostname() == "localhost" || ip.IsLoopback())) {
		return errors.New("use HTTPS or loopback HTTP without URL credentials/query")
	}
	data, err := os.ReadFile(*tokenFile)
	if err != nil {
		return err
	}
	method, path := "GET", ""
	var body []byte
	key, match := "", ""
	name := args[0]
	args = args[1:]
	if name == "templates" || name == "list" {
		if len(args) != 0 {
			return errors.New("unexpected arguments")
		}
		path = "/api/v1/templates"
		if name == "list" {
			path = "/api/v1/machines"
		}
	} else {
		if len(args) == 0 {
			return errors.New("expected machine name or UUID")
		}
		id := args[0]
		args = args[1:]
		options := flag.NewFlagSet(name, flag.ContinueOnError)
		switch name {
		case "inspect", "operation":
			if !model.ValidID(id) || len(args) > 0 {
				return errors.New("expected a canonical UUID")
			}
			path = "/api/v1/machines/" + id
			if name == "operation" {
				path = "/api/v1/operations/" + id
			}
		case "create":
			template := options.String("template", "", "template revision")
			memory := options.Uint("memory-mib", 2048, "memory MiB")
			cpus := options.Uint("vcpus", 2, "virtual CPUs")
			k := options.String("key", "", "idempotency key")
			if err := options.Parse(args); err != nil {
				return err
			}
			if options.NArg() != 0 || *memory > 16384 || *cpus > 8 {
				return errors.New("invalid create options")
			}
			spec := model.CreateMachine{Name: id, TemplateRevision: *template, MemoryMiB: uint32(*memory), VCPUs: uint32(*cpus)}
			if err := spec.Validate(); err != nil {
				return err
			}
			key = *k
			body, err = json.Marshal(spec)
			if err != nil {
				return err
			}
			method = "POST"
			path = "/api/v1/machines"
		case "start", "stop", "delete":
			if !model.ValidID(id) {
				return errors.New("expected a canonical UUID")
			}
			rev := options.Int64("revision", 0, "revision from inspect")
			k := options.String("key", "", "idempotency key")
			if err := options.Parse(args); err != nil {
				return err
			}
			if *rev < 1 || options.NArg() != 0 {
				return errors.New("positive revision required")
			}
			key = *k
			match = strconv.Quote(strconv.FormatInt(*rev, 10))
			method = "POST"
			path = "/api/v1/machines/" + id + "/actions/" + name
			if name == "delete" {
				method = "DELETE"
				path = "/api/v1/machines/" + id
			}
		default:
			return errors.New("unknown command")
		}
	}
	if method != "GET" && (key == "" || len(key) > 128) {
		return errors.New("--key is required for mutations")
	}
	request, err := http.NewRequest(method, strings.TrimRight(*base, "/")+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(data)))
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	if match != "" {
		request.Header.Set("If-Match", match)
	}
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if _, err := io.Copy(os.Stdout, io.LimitReader(response.Body, 4<<20)); err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("API returned %s", response.Status)
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

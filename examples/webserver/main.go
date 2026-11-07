// Command webserver is a bounded local HTTP host for the public html2pdf API.
// It provides no external asset resolver.
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
	"runtime"
	"syscall"
	"time"
)

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
func run() error {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	concurrency := flag.Int("max-concurrent", 1, "Maximum concurrent renders (1..16)")
	timeout := flag.Duration("render-timeout", 30*time.Second, "Cooperative render deadline")
	flag.Parse()
	if *concurrency < 1 || *concurrency > 16 || *timeout <= 0 || *timeout > 5*time.Minute {
		return fmt.Errorf("invalid concurrency or render timeout")
	}
	engine, err := exampleEngine()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	server := &http.Server{Addr: *addr, Handler: newHandler(engine, *concurrency, *timeout), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: *timeout + 20*time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10, BaseContext: func(net.Listener) context.Context { return ctx }}
	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	log.Printf("html2pdf example listening on %s (%s/%s %s)", *addr, runtime.GOOS, runtime.GOARCH, runtime.Version())
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			_ = server.Close()
			return err
		}
		err := <-done
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

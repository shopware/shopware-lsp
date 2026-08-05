package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	_ "net/http/pprof"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/shopware/shopware-lsp/internal/app"
)

func (r *Runner) runServe(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(r.errOut)
	listen := flags.String("listen", "", "TCP address or unix;<path> for one remote client")
	listenTimeout := flags.Duration("listen.timeout", 0, "timeout waiting for a remote client")
	logFile := flags.String("logfile", "", "write server logs to this file")
	debugAddress := flags.String("debug", "", "serve runtime profiling endpoints on this address")
	rpcTrace := flags.Bool("rpc.trace", false, "write raw JSON-RPC traffic to stderr")
	// Transport flags are accepted here as well as globally because an editor
	// can append them after the explicit `serve` subcommand.
	flags.Bool("stdio", false, "use standard input/output transport")
	flags.Int("clientProcessId", 0, "parent language-client process ID")
	if err := flags.Parse(args); err != nil {
		return usageError(err.Error())
	}
	if flags.NArg() != 0 {
		return usageError("serve takes no positional arguments")
	}
	if *logFile != "" {
		file, err := os.OpenFile(*logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return fmt.Errorf("open log file: %w", err)
		}
		defer closeIgnoringError(file)
		log.SetOutput(file)
	}
	if r.options.GCPolicyApplied {
		log.Printf("Using balanced Go GC target: GOGC=%d", r.options.GCPercent)
	}
	log.Printf("Shopware LSP version: %s", r.options.Version)
	var debugServer *http.Server
	if *debugAddress != "" {
		debugServer = &http.Server{
			Addr:              *debugAddress,
			Handler:           http.DefaultServeMux,
			ReadHeaderTimeout: 5 * time.Second,
		}
		debugListener, err := net.Listen("tcp", *debugAddress)
		if err != nil {
			return fmt.Errorf("start debug server: %w", err)
		}
		go func() {
			if err := debugServer.Serve(debugListener); err != nil && err != http.ErrServerClosed {
				log.Printf("Debug server: %v", err)
			}
		}()
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = debugServer.Shutdown(shutdownCtx)
		}()
		log.Printf("Debug server listening on http://%s/debug/pprof/", debugListener.Addr())
	}
	if *listen == "" {
		in, out := r.in, r.out
		if *rpcTrace {
			in = io.TeeReader(in, &rpcTraceWriter{destination: r.errOut, direction: "client -> server"})
			out = &tracedWriter{
				destination: out,
				trace:       &rpcTraceWriter{destination: r.errOut, direction: "server -> client"},
			}
		}
		application := app.New(r.options.Version)
		defer closeIgnoringError(application)
		serverDone := make(chan error, 1)
		go func() { serverDone <- application.Run(in, out) }()
		select {
		case err := <-serverDone:
			return err
		case <-ctx.Done():
			_ = application.Close()
			if closer, ok := r.in.(io.Closer); ok {
				_ = closer.Close()
			}
			select {
			case <-serverDone:
			case <-time.After(5 * time.Second):
			}
			return ctx.Err()
		}
	}

	network, address := parseListenAddress(*listen)
	listener, err := net.Listen(network, address)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", *listen, err)
	}
	defer closeIgnoringError(listener)
	if network == "unix" {
		defer func() { _ = os.Remove(address) }()
	}
	if *listenTimeout > 0 {
		if deadline, ok := listener.(interface{ SetDeadline(time.Time) error }); ok {
			if err := deadline.SetDeadline(time.Now().Add(*listenTimeout)); err != nil {
				return fmt.Errorf("set listen timeout: %w", err)
			}
		}
	}
	if r.verbose {
		if err := writeFormatted(r.errOut, "Listening on %s;%s\n", network, address); err != nil {
			return err
		}
	}
	type acceptResult struct {
		connection net.Conn
		err        error
	}
	accepted := make(chan acceptResult, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		accepted <- acceptResult{connection: connection, err: acceptErr}
	}()
	var result acceptResult
	select {
	case result = <-accepted:
	case <-ctx.Done():
		_ = listener.Close()
		result = <-accepted
		if result.connection != nil {
			_ = result.connection.Close()
		}
		return ctx.Err()
	}
	connection, err := result.connection, result.err
	if err != nil {
		if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
			return fmt.Errorf("listen timeout after %s", *listenTimeout)
		}
		return fmt.Errorf("accept connection: %w", err)
	}
	defer closeIgnoringError(connection)
	var in io.Reader = connection
	var out io.Writer = connection
	if *rpcTrace {
		in = io.TeeReader(in, &rpcTraceWriter{destination: r.errOut, direction: "client -> server"})
		out = &tracedWriter{
			destination: out,
			trace:       &rpcTraceWriter{destination: r.errOut, direction: "server -> client"},
		}
	}
	application := app.New(r.options.Version)
	defer closeIgnoringError(application)
	serverDone := make(chan error, 1)
	go func() { serverDone <- application.Run(in, out) }()
	select {
	case err := <-serverDone:
		return err
	case <-ctx.Done():
		_ = connection.Close()
		_ = application.Close()
		select {
		case <-serverDone:
		case <-time.After(5 * time.Second):
		}
		return ctx.Err()
	}
}

func parseListenAddress(value string) (network, address string) {
	if strings.HasPrefix(value, "unix;") {
		return "unix", strings.TrimPrefix(value, "unix;")
	}
	if strings.HasPrefix(value, "tcp;") {
		return "tcp", strings.TrimPrefix(value, "tcp;")
	}
	return "tcp", value
}

type rpcTraceWriter struct {
	mu          sync.Mutex
	destination io.Writer
	direction   string
}

func (w *rpcTraceWriter) Write(content []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, err := fmt.Fprintf(w.destination, "\n[%s]\n%s", w.direction, content)
	if err != nil {
		return 0, err
	}
	return len(content), nil
}

type tracedWriter struct {
	destination io.Writer
	trace       io.Writer
}

func (w *tracedWriter) Write(content []byte) (int, error) {
	written, err := w.destination.Write(content)
	if written > 0 {
		_, _ = w.trace.Write(content[:written])
	}
	return written, err
}

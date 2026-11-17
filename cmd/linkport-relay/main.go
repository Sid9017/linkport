// linkport-relay forwards encrypted iroh datagrams; it is not a device directory.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/Sid9017/linkport/internal/relayservice"
)

func run(ctx context.Context, args []string, output io.Writer) error {
	cfg := relayservice.DefaultConfig()
	flags := flag.NewFlagSet("linkport-relay", flag.ContinueOnError)
	flags.SetOutput(output)
	addr := flags.String("listen", "127.0.0.1:3340", "TCP listen address (plaintext must use loopback)")
	flags.StringVar(&cfg.CertFile, "tls-cert", "", "PEM certificate chain")
	flags.StringVar(&cfg.KeyFile, "tls-key", "", "PEM certificate private key")
	flags.IntVar(&cfg.MaxConnections, "max-connections", cfg.MaxConnections, "maximum TCP connections, including pending authentication")
	flags.Int64Var(&cfg.ClientBytesPerSecond, "client-bytes-per-second", cfg.ClientBytesPerSecond, "per-client receive bytes per second")
	flags.DurationVar(&cfg.ReadHeaderTimeout, "read-header-timeout", cfg.ReadHeaderTimeout, "HTTP header deadline")
	flags.DurationVar(&cfg.IdleTimeout, "idle-timeout", cfg.IdleTimeout, "HTTP keep-alive timeout (not relay session lifetime)")
	flags.DurationVar(&cfg.ShutdownTimeout, "shutdown-timeout", cfg.ShutdownTimeout, "maximum shutdown wait")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	server, err := relayservice.New(cfg)
	if err != nil {
		return err
	}
	// Require a literal loopback address so DNS cannot widen plaintext binding.
	host, _, err := net.SplitHostPort(*addr)
	if err != nil {
		return err
	}
	if cfg.CertFile == "" && !net.ParseIP(host).IsLoopback() {
		return fmt.Errorf("plaintext listener requires a literal loopback IP; configure TLS for public binding")
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	defer ln.Close()
	fmt.Fprintf(output, "linkport-relay listening on %s (TLS=%t)\n", ln.Addr(), cfg.CertFile != "")
	err = server.Serve(ctx, ln)
	fmt.Fprintf(output, "linkport-relay stopped: connections=%d counters=%v\n", server.Connections(), server.Snapshot())
	return err
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stderr); err != nil && !errors.Is(err, flag.ErrHelp) {
		log.Print(err)
		os.Exit(1)
	}
}

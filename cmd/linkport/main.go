package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"path/filepath"
	"text/tabwriter"
	"time"

	"github.com/Sid9017/linkport/docs/design"
	"github.com/Sid9017/linkport/internal/api"
	"github.com/Sid9017/linkport/internal/config"
	"github.com/Sid9017/linkport/internal/engine"
	"github.com/Sid9017/linkport/internal/serialport"
	"github.com/Sid9017/linkport/internal/share"
	"github.com/Sid9017/linkport/internal/version"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, "linkport:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, output, errorOutput io.Writer) error {
	if len(args) == 0 {
		printHelp(output)
		return nil
	}
	switch args[0] {
	case "help", "-h", "--help":
		printHelp(output)
		return nil
	case "version", "--version":
		fmt.Fprintln(output, version.String())
		return nil
	case "design":
		flags := flag.NewFlagSet("design", flag.ContinueOnError)
		flags.SetOutput(errorOutput)
		listen := flags.String("listen", "127.0.0.1:7710", "loopback address for the desktop design preview")
		source := flags.String("source", "", "read a local design fragment on each request during design review")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return errors.New("design accepts flags only")
		}
		handler := design.Handler()
		if *source != "" {
			handler = design.SourceHandler(*source)
		}
		return serve(ctx, *listen, handler, output, "Desktop design preview (demo data)")
	case "ports", "share", "unshare", "watch", "serve":
		return runLocal(ctx, args, output, errorOutput)
	case "identity", "devices", "invite", "pair", "forget", "network":
		return runDevices(ctx, args, output, errorOutput)
	default:
		return fmt.Errorf("unknown command %q; run linkport help", args[0])
	}
}

func printHelp(output io.Writer) {
	fmt.Fprintf(output, `Linkport %s — serial sharing between paired devices

Usage:
  linkport version
  linkport design [--listen 127.0.0.1:7710]
  linkport ports [--config FILE] [--json]
  linkport share [--config FILE] ID_OR_PATH
  linkport unshare [--config FILE] ID_OR_PATH
  linkport watch [--config FILE]
  linkport serve [--config FILE]
  linkport serve --local-api [--config FILE] [--listen 127.0.0.1:7700]
  linkport identity [--directory DIR]
  linkport devices [--directory DIR]
  linkport invite [--directory DIR]
  linkport pair [--directory DIR] INVITATION
  linkport forget [--directory DIR] DEVICE_ID
  linkport network [--directory DIR] [--relay-mode default|disabled]
                   [--relay-urls URL,URL] [--discovery-url URL]

All ports start unshared. watch emits shared-port snapshots as JSON.
serve runs authenticated iroh sharing for explicitly paired devices.
invite prints an eight-hour single-use invitation and stays online for pairing.
identity --reset --confirm DEVICE_ID clears trusted devices and generates a new key.
serve --local-api exposes read-only snapshots and SSE on loopback for development.
The browser design preview uses demo data. The native Wails host uses real data.
`, version.Version)
}

func runLocal(ctx context.Context, args []string, output, errorOutput io.Writer) error {
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(errorOutput)
	configPath := flags.String("config", "", "sharing choices JSON file (default: user config directory/linkport/shares.json)")
	asJSON := flags.Bool("json", false, "print local ports as JSON")
	listen := flags.String("listen", "127.0.0.1:7700", "loopback address for the read-only development API")
	localAPI := flags.Bool("local-api", false, "serve the loopback read-only development API instead of iroh")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	wantsPort := args[0] == "share" || args[0] == "unshare"
	if (wantsPort && flags.NArg() != 1) || (!wantsPort && flags.NArg() != 0) {
		return fmt.Errorf("%s: unexpected arguments; flags precede the port ID or path", args[0])
	}
	if *configPath == "" {
		directory, err := os.UserConfigDir()
		if err != nil {
			return err
		}
		*configPath = filepath.Join(directory, "linkport", "shares.json")
	}
	if args[0] == "serve" && !*localAPI {
		if *listen != "127.0.0.1:7700" {
			return errors.New("--listen requires --local-api; iroh manages its own network endpoint")
		}
		live, err := engine.New(ctx, engine.Options{Directory: filepath.Dir(*configPath), SharingConfig: *configPath})
		if err != nil {
			return err
		}
		fmt.Fprintln(output, "Linkport iroh device:", live.Network().Status().NodeID)
		fmt.Fprintln(output, "Only paired devices can access explicitly shared ports; no account or VPN required.")
		live.Run()
		return ctx.Err()
	}
	store := config.NewFileStore(*configPath)
	manager, err := share.NewManager(ctx, serialport.NewEnumerator(), store, share.Options{})
	if err != nil {
		return err
	}
	if err := manager.Refresh(ctx); err != nil {
		return err
	}
	switch args[0] {
	case "ports":
		ports := manager.LocalPorts()
		if *asJSON {
			return json.NewEncoder(output).Encode(ports)
		}
		writer := tabwriter.NewWriter(output, 0, 4, 2, ' ', 0)
		fmt.Fprintln(writer, "PATH\tSHARED\tIDENTITY")
		for _, port := range ports {
			state := "no"
			if port.Shared {
				state = "yes"
			}
			if port.Ambiguous {
				state = "needs confirmation"
			}
			fmt.Fprintf(writer, "%s\t%s\t%s\n", port.Path, state, port.ID)
		}
		return writer.Flush()
	case "share", "unshare":
		identity, err := resolveIdentity(ctx, manager, store, flags.Arg(0))
		if err != nil {
			return err
		}
		enabled := args[0] == "share"
		if err := manager.SetShared(ctx, identity, enabled); err != nil {
			return err
		}
		fmt.Fprintf(output, "%s: shared=%t\n", identity, enabled)
		return nil
	case "watch":
		watchCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		updates, unsubscribe, err := manager.Subscribe(watchCtx)
		if err != nil {
			return err
		}
		defer unsubscribe()
		go manager.Run(watchCtx)
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case snapshot, open := <-updates:
				if !open {
					return nil
				}
				if err := json.NewEncoder(output).Encode(snapshot); err != nil {
					return err
				}
			case err := <-manager.Errors():
				fmt.Fprintln(errorOutput, "serial watcher:", err)
			}
		}
	case "serve":
		handler, err := api.NewHandler(manager, api.Options{Version: version.Version, Auth: loopbackOnly})
		if err != nil {
			return err
		}
		watchCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		go manager.Run(watchCtx)
		go func() {
			for {
				select {
				case <-watchCtx.Done():
					return
				case err := <-manager.Errors():
					fmt.Fprintln(errorOutput, "serial watcher:", err)
				}
			}
		}()
		return serve(watchCtx, *listen, handler, output, "Read-only development API (remote data bridge unavailable)")
	default:
		return errors.New("invalid local command")
	}
}

func resolveIdentity(ctx context.Context, manager *share.Manager, store config.Store, value string) (string, error) {
	matches := make(map[string]struct{})
	for _, port := range manager.LocalPorts() {
		if port.ID == value || port.Path == value {
			matches[port.ID] = struct{}{}
		}
	}
	state, err := store.Load(ctx)
	if err != nil {
		return "", err
	}
	for _, choice := range state.Shares {
		if choice.Port.ID == value || choice.Port.Path == value {
			matches[choice.Port.ID] = struct{}{}
		}
	}
	if len(matches) > 1 {
		return "", errors.New("ambiguous port path; select its explicit identity")
	}
	for identity := range matches {
		return identity, nil
	}
	return "", fmt.Errorf("port %q not found; run linkport ports", value)
}

func serve(ctx context.Context, address string, handler http.Handler, output io.Writer, label string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || !ip.IsLoopback() {
		return errors.New("this preview build only supports explicit loopback listeners, such as 127.0.0.1:7700")
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	defer listener.Close()
	server := &http.Server{Handler: loopbackOnly(handler), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	finished := make(chan error, 1)
	go func() { finished <- server.Serve(listener) }()
	fmt.Fprintf(output, "%s: http://%s\n", label, listener.Addr())
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
			return err
		}
		return ctx.Err()
	case err := <-finished:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func loopbackOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		host, _, err := net.SplitHostPort(request.RemoteAddr)
		ip, parseErr := netip.ParseAddr(host)
		if err != nil || parseErr != nil || !ip.IsLoopback() {
			http.Error(writer, "loopback clients only", http.StatusForbidden)
			return
		}
		next.ServeHTTP(writer, request)
	})
}

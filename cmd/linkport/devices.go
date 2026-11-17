package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Sid9017/linkport/internal/engine"
	"github.com/Sid9017/linkport/internal/p2p"
)

func runDevices(ctx context.Context, args []string, output, errorOutput io.Writer) error {
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(errorOutput)
	directory := flags.String("directory", "", "Linkport device configuration directory")
	reset := flags.Bool("reset", false, "replace this device identity and clear its trusted devices")
	confirm := flags.String("confirm", "", "full current device ID, required for identity reset")
	mode := flags.String("relay-mode", "", "default or disabled")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	wantsArgument := args[0] == "pair" || args[0] == "forget"
	if (wantsArgument && flags.NArg() != 1) || (!wantsArgument && flags.NArg() != 0) {
		return errors.New("unexpected arguments; flags precede the invitation or device ID")
	}
	if *reset && args[0] != "identity" {
		return errors.New("--reset requires the identity command")
	}
	if *directory == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return err
		}
		*directory = filepath.Join(base, "linkport")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	live, err := engine.New(ctx, engine.Options{
		Directory: *directory, DisableNetworkListener: args[0] != "pair" && args[0] != "invite",
	})
	if err != nil {
		return err
	}
	defer live.Close()
	network := live.Network()
	switch args[0] {
	case "identity":
		if *reset {
			if err := network.ResetIdentity(*confirm); err != nil {
				return err
			}
		}
		return json.NewEncoder(output).Encode(network.Status())
	case "devices":
		return json.NewEncoder(output).Encode(network.Peers())
	case "forget":
		return live.RemoveDevice(flags.Arg(0))
	case "network":
		if *mode != "" {
			if err := network.SaveConfig(p2p.NetworkConfig{
				RelayMode: *mode, RelayURLs: []string{},
			}); err != nil {
				return err
			}
		}
		return json.NewEncoder(output).Encode(network.Status().Config)
	case "pair", "invite":
		go live.Run()
		if args[0] == "pair" {
			peer, err := live.AddDevice(ctx, flags.Arg(0))
			if err != nil {
				return err
			}
			return json.NewEncoder(output).Encode(peer)
		}
		invitation, err := network.CreateInvitation()
		if err != nil {
			return err
		}
		fmt.Fprintln(output, invitation.Code)
		fmt.Fprintln(errorOutput, "Keep this process running. Invitation expires:", invitation.ExpiresAt.Format("15:04:05 MST"))
		<-ctx.Done()
		return ctx.Err()
	}
	return errors.New("unknown device command")
}

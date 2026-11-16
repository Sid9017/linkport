package main

import (
	"bytes"
	"context"
	"net/url"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Sid9017/linkport/internal/tailnet"
)

type tailscaleStatus struct {
	tailnet.Status
	LoggingIn bool `json:"loggingIn"`
}

type tailscaleClient struct {
	ctx     context.Context
	openURL func(string) error
	mu      sync.Mutex
	login   bool
	lastErr string
	source  func() tailnet.Status
	changed func()
}

func tailscaleBinary() (string, error) {
	return tailnet.Binary()
}

func (client *tailscaleClient) status(ctx context.Context) tailscaleStatus {
	client.mu.Lock()
	status := tailscaleStatus{LoggingIn: client.login}
	lastErr := client.lastErr
	client.mu.Unlock()
	if client.source != nil {
		status.Status = client.source()
	} else {
		status.Status = (&tailnet.Client{}).Status(ctx)
	}
	if lastErr != "" {
		status.Error = lastErr
	}
	return status
}

func (client *tailscaleClient) beginLogin() error {
	binary, err := tailscaleBinary()
	if err != nil {
		return client.openURL("https://tailscale.com/download")
	}
	client.mu.Lock()
	if client.login {
		client.mu.Unlock()
		return nil
	}
	client.login, client.lastErr = true, ""
	client.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(client.ctx, 2*time.Minute)
		defer cancel()
		output := &loginOutput{openURL: client.openURL}
		operation := "login"
		if client.source != nil && client.source().State == "Stopped" {
			operation = "up"
		}
		arguments := []string{operation}
		if operation == "login" {
			arguments = append(arguments, "--timeout=90s")
		}
		command := exec.CommandContext(ctx, binary, arguments...)
		command.Stdout, command.Stderr = output, output
		err := command.Run()
		output.flush()
		client.mu.Lock()
		client.login = false
		if err != nil && client.ctx.Err() == nil {
			client.lastErr = "Tailscale login was not completed"
		}
		client.mu.Unlock()
		if client.changed != nil {
			client.changed()
		}
	}()
	return nil
}

var loginURLPattern = regexp.MustCompile(`https://[^\s<>"']+`)

type loginOutput struct {
	mu      sync.Mutex
	buffer  bytes.Buffer
	opened  bool
	openURL func(string) error
}

func trustedLoginURL(value string) bool {
	address, err := url.Parse(value)
	return err == nil && address.Scheme == "https" && address.User == nil &&
		(address.Host == "login.tailscale.com" || address.Host == "tailscale.com") &&
		strings.HasPrefix(address.Path, "/a/")
}

func (output *loginOutput) consume(line string) {
	if output.opened {
		return
	}
	for _, candidate := range loginURLPattern.FindAllString(line, -1) {
		if trustedLoginURL(candidate) {
			if err := output.openURL(candidate); err == nil {
				output.opened = true
			}
			return
		}
	}
}

func (output *loginOutput) Write(content []byte) (int, error) {
	output.mu.Lock()
	defer output.mu.Unlock()
	output.buffer.Write(content)
	for {
		line, err := output.buffer.ReadString('\n')
		if err != nil {
			output.buffer.WriteString(line)
			break
		}
		output.consume(line)
	}
	if output.buffer.Len() > 64*1024 {
		output.buffer.Reset()
	}
	return len(content), nil
}

func (output *loginOutput) flush() {
	output.mu.Lock()
	defer output.mu.Unlock()
	output.consume(output.buffer.String())
	output.buffer.Reset()
}

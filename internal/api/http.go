package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Sid9017/linkport/internal/share"
)

type Middleware func(http.Handler) http.Handler

type Info struct {
	NodeID           string `json:"node_id,omitempty"`
	Name             string `json:"name"`
	Version          string `json:"version"`
	APIVersion       string `json:"api_version"`
	RemoteDataBridge bool   `json:"remote_data_bridge"`
}

type Options struct {
	NodeID            string
	Bridge            http.Handler
	Name              string
	Version           string
	Auth              Middleware
	HeartbeatInterval time.Duration
	WriteTimeout      time.Duration
}

func NewHandler(manager *share.Manager, options Options) (http.Handler, error) {
	if manager == nil {
		return nil, errors.New("sharing manager is required")
	}
	if options.Auth == nil {
		return nil, errors.New("authentication middleware is required")
	}
	if options.HeartbeatInterval < 0 || options.WriteTimeout < 0 {
		return nil, errors.New("HTTP event intervals must not be negative")
	}
	if options.HeartbeatInterval == 0 {
		options.HeartbeatInterval = 15 * time.Second
	}
	if options.WriteTimeout == 0 {
		options.WriteTimeout = 10 * time.Second
	}
	if options.Name == "" {
		options.Name = "linkport"
	}
	info := Info{NodeID: options.NodeID, Name: options.Name, Version: options.Version, APIVersion: "v1", RemoteDataBridge: options.Bridge != nil}
	mux := http.NewServeMux()
	if options.Bridge != nil {
		mux.Handle("/v1/connect", options.Bridge)
	}
	mux.HandleFunc("/v1/info", func(writer http.ResponseWriter, request *http.Request) {
		if !getOnly(writer, request) {
			return
		}
		writeJSON(writer, info)
	})
	mux.HandleFunc("/v1/ports", func(writer http.ResponseWriter, request *http.Request) {
		if !getOnly(writer, request) {
			return
		}
		writeJSON(writer, manager.Snapshot())
	})
	mux.HandleFunc("/v1/events", func(writer http.ResponseWriter, request *http.Request) {
		if !getOnly(writer, request) {
			return
		}
		updates, unsubscribe, err := manager.Subscribe(request.Context())
		if err != nil {
			http.Error(writer, "event subscription unavailable", http.StatusServiceUnavailable)
			return
		}
		defer unsubscribe()
		controller := http.NewResponseController(writer)
		if err := controller.SetWriteDeadline(time.Now().Add(options.WriteTimeout)); err != nil {
			http.Error(writer, "streaming deadlines unavailable", http.StatusInternalServerError)
			return
		}
		defer controller.SetWriteDeadline(time.Time{})
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.Header().Set("Cache-Control", "no-cache")
		writer.Header().Set("X-Accel-Buffering", "no")
		heartbeat := time.NewTicker(options.HeartbeatInterval)
		defer heartbeat.Stop()
		for {
			select {
			case <-request.Context().Done():
				return
			case snapshot, open := <-updates:
				if !open {
					return
				}
				contents, err := json.Marshal(snapshot)
				if err != nil {
					return
				}
				if err := controller.SetWriteDeadline(time.Now().Add(options.WriteTimeout)); err != nil {
					return
				}
				if _, err := fmt.Fprintf(writer, "id: %d\nevent: snapshot\ndata: %s\n\n", snapshot.Revision, contents); err != nil {
					return
				}
			case <-heartbeat.C:
				if err := controller.SetWriteDeadline(time.Now().Add(options.WriteTimeout)); err != nil {
					return
				}
				if _, err := fmt.Fprint(writer, ": heartbeat\n\n"); err != nil {
					return
				}
			}
			if err := controller.Flush(); err != nil {
				return
			}
			if err := controller.SetWriteDeadline(time.Time{}); err != nil {
				return
			}
		}
	})
	return options.Auth(mux), nil
}

func getOnly(writer http.ResponseWriter, request *http.Request) bool {
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	return true
}

func writeJSON(writer http.ResponseWriter, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(writer).Encode(value)
}

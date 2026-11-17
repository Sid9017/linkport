package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/Sid9017/linkport/internal/engine"
	"github.com/Sid9017/linkport/internal/p2p"
	"github.com/Sid9017/linkport/internal/relayservice"
	"github.com/Sid9017/linkport/internal/vport"
)

type nativeAPI struct {
	ctx                  context.Context
	token                string
	preferences          *preferenceStore
	updates              *updateController
	relayUpdates         *relayUpdateController
	engine               *engine.Engine
	onPreferencesChanged func(preferences)
	exportLog            func(string) (string, error)
}

func (api *nativeAPI) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /native/v1/monitor/local", func(writer http.ResponseWriter, request *http.Request) {
		var target struct {
			ID       string `json:"id"`
			Path     string `json:"path"`
			BaudRate int    `json:"baudRate"`
		}
		if !decodeNative(writer, request, &target) {
			return
		}
		if api.engine == nil {
			http.Error(writer, "serial service unavailable", http.StatusServiceUnavailable)
			return
		}
		state, err := api.engine.MonitorLocal(request.Context(), target.ID, target.Path, target.BaudRate)
		if err != nil {
			http.Error(writer, err.Error(), http.StatusConflict)
			return
		}
		nativeJSON(writer, state)
	})
	mux.HandleFunc("POST /native/v1/export", func(writer http.ResponseWriter, request *http.Request) {
		var command struct {
			ID string `json:"id"`
		}
		if !decodeNative(writer, request, &command) {
			return
		}
		if api.exportLog == nil {
			http.Error(writer, "log export unavailable", http.StatusServiceUnavailable)
			return
		}
		name, err := api.exportLog(command.ID)
		if err != nil {
			http.Error(writer, err.Error(), http.StatusInternalServerError)
			return
		}
		nativeJSON(writer, struct {
			Saved bool   `json:"saved"`
			Name  string `json:"name"`
		}{Saved: name != "", Name: name})
	})
	mux.HandleFunc("GET /native/v1/state", func(writer http.ResponseWriter, request *http.Request) {
		if api.engine == nil {
			http.Error(writer, "serial service unavailable", http.StatusServiceUnavailable)
			return
		}
		nativeJSON(writer, api.engine.State())
	})
	mux.HandleFunc("POST /native/v1/refresh", func(writer http.ResponseWriter, request *http.Request) {
		if api.engine == nil {
			http.Error(writer, "serial service unavailable", http.StatusServiceUnavailable)
			return
		}
		api.engine.Refresh()
		writer.WriteHeader(http.StatusAccepted)
	})
	mux.HandleFunc("PUT /native/v1/share", func(writer http.ResponseWriter, request *http.Request) {
		var change struct {
			ID     string `json:"id"`
			Shared bool   `json:"shared"`
		}
		if !decodeNative(writer, request, &change) {
			return
		}
		if api.engine == nil {
			http.Error(writer, "serial service unavailable", http.StatusServiceUnavailable)
			return
		}
		if err := api.engine.SetShared(request.Context(), change.ID, change.Shared); err != nil {
			http.Error(writer, err.Error(), http.StatusConflict)
			return
		}
		nativeJSON(writer, api.engine.State())
	})
	mux.HandleFunc("POST /native/v1/connect", func(writer http.ResponseWriter, request *http.Request) {
		var target struct {
			NodeID     string `json:"nodeID"`
			PortID     string `json:"portID"`
			BaudRate   int    `json:"baudRate"`
			Path       string `json:"path"`
			PairDevice string `json:"pairDevice"`
		}
		if !decodeNative(writer, request, &target) {
			return
		}
		if api.engine == nil {
			http.Error(writer, "serial service unavailable", http.StatusServiceUnavailable)
			return
		}
		state, err := api.engine.Connect(request.Context(), target.NodeID, target.PortID, target.BaudRate, vport.Options{Path: target.Path, PairDevice: target.PairDevice})
		if err != nil {
			http.Error(writer, err.Error(), http.StatusConflict)
			return
		}
		nativeJSON(writer, state)
	})
	for _, operation := range []string{"disconnect", "send", "clear"} {
		mux.HandleFunc("POST /native/v1/"+operation, func(writer http.ResponseWriter, request *http.Request) {
			var command struct {
				ID   string `json:"id"`
				Data []byte `json:"data,omitempty"`
			}
			if !decodeNative(writer, request, &command) {
				return
			}
			if api.engine == nil {
				http.Error(writer, "serial service unavailable", http.StatusServiceUnavailable)
				return
			}
			var err error
			switch operation {
			case "disconnect":
				err = api.engine.Sessions().Disconnect(command.ID)
			case "send":
				err = api.engine.Sessions().Send(command.ID, command.Data)
			case "clear":
				err = api.engine.Sessions().Clear(command.ID)
			}
			if err != nil {
				http.Error(writer, err.Error(), http.StatusConflict)
				return
			}
			writer.WriteHeader(http.StatusNoContent)
		})
	}
	mux.HandleFunc("GET /native/v1/preferences", func(writer http.ResponseWriter, request *http.Request) {
		nativeJSON(writer, api.preferences.get())
	})
	mux.HandleFunc("PUT /native/v1/preferences", func(writer http.ResponseWriter, request *http.Request) {
		decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 16*1024))
		decoder.DisallowUnknownFields()
		var settings preferences
		if err := decoder.Decode(&settings); err != nil {
			http.Error(writer, "invalid preferences", http.StatusBadRequest)
			return
		}
		if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
			http.Error(writer, "expected one preferences object", http.StatusBadRequest)
			return
		}
		if err := settings.validate(); err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		if err := api.preferences.save(settings); err != nil {
			http.Error(writer, "unable to save preferences", http.StatusInternalServerError)
			return
		}
		if api.onPreferencesChanged != nil {
			api.onPreferencesChanged(settings)
		}
		nativeJSON(writer, api.preferences.get())
	})
	for _, pattern := range []string{
		"GET /native/v1/network", "GET /native/v1/devices",
		"GET /native/v1/relay", "PUT /native/v1/relay",
		"POST /native/v1/devices/invite", "POST /native/v1/devices",
		"DELETE /native/v1/devices", "PUT /native/v1/network",
		"POST /native/v1/identity/reset",
	} {
		mux.HandleFunc(pattern, api.manageDevices)
	}
	mux.HandleFunc("GET /native/v1/relay-update", func(w http.ResponseWriter, r *http.Request) { nativeJSON(w, api.relayUpdates.snapshot()) })
	for _, action := range []string{"check", "apply"} {
		mux.HandleFunc("POST /native/v1/relay-update/"+action, func(w http.ResponseWriter, r *http.Request) {
			var err error
			if action == "check" {
				err = api.relayUpdates.check()
			} else {
				err = api.relayUpdates.apply()
			}
			if err != nil {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			w.WriteHeader(http.StatusAccepted)
		})
	}
	mux.HandleFunc("GET /native/v1/update", func(writer http.ResponseWriter, request *http.Request) {
		nativeJSON(writer, api.updates.snapshot())
	})
	for _, operation := range []string{"check", "download", "restart"} {
		mux.HandleFunc("POST /native/v1/update/"+operation, func(writer http.ResponseWriter, request *http.Request) {
			var err error
			switch operation {
			case "check":
				err = api.updates.runJob(false)
			case "download":
				err = api.updates.runJob(true)
			case "restart":
				err = api.updates.restart()
			}
			if err != nil {
				http.Error(writer, err.Error(), http.StatusConflict)
				return
			}
			writer.WriteHeader(http.StatusAccepted)
		})
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if api.token == "" || subtle.ConstantTimeCompare([]byte(request.Header.Get("X-Linkport-Token")), []byte(api.token)) != 1 {
			http.Error(writer, "unauthorized", http.StatusForbidden)
			return
		}
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		mux.ServeHTTP(writer, request)
	})
}

func (api *nativeAPI) manageDevices(writer http.ResponseWriter, request *http.Request) {
	if api.engine == nil {
		http.Error(writer, "device service unavailable", http.StatusServiceUnavailable)
		return
	}
	network := api.engine.Network()
	var result any
	var err error
	switch request.Method + " " + request.URL.Path {
	case "GET /native/v1/relay":
		result = network.Status()
	case "PUT /native/v1/relay":
		var cfg relayservice.HostConfig
		if !decodeNative(writer, request, &cfg) {
			return
		}
		err = network.ConfigureHostedRelay(cfg)
		result = network.Status()
	case "GET /native/v1/network":
		result = network.Status()
	case "GET /native/v1/devices":
		result = network.Peers()
	case "POST /native/v1/devices/invite":
		result, err = network.CreateInvitation()
	case "POST /native/v1/devices":
		var command struct {
			Code string `json:"code"`
		}
		if !decodeNative(writer, request, &command) {
			return
		}
		result, err = api.engine.AddDevice(request.Context(), command.Code)
	case "DELETE /native/v1/devices":
		var command struct {
			ID string `json:"id"`
		}
		if !decodeNative(writer, request, &command) {
			return
		}
		err = api.engine.RemoveDevice(command.ID)
		result = network.Peers()
	case "PUT /native/v1/network":
		var cfg p2p.NetworkConfig
		if !decodeNative(writer, request, &cfg) {
			return
		}
		err = network.SaveConfig(cfg)
		result = network.Status()
	case "POST /native/v1/identity/reset":
		var command struct {
			Confirmation string `json:"confirmation"`
		}
		if !decodeNative(writer, request, &command) {
			return
		}
		err = network.ResetIdentity(command.Confirmation)
		result = network.Status()
	}
	if err != nil {
		http.Error(writer, err.Error(), http.StatusConflict)
		return
	}
	nativeJSON(writer, result)
}

func decodeNative(writer http.ResponseWriter, request *http.Request, value any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 128*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		http.Error(writer, "invalid request", http.StatusBadRequest)
		return false
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		http.Error(writer, "expected one JSON object", http.StatusBadRequest)
		return false
	}
	return true
}

func nativeJSON(writer http.ResponseWriter, value any) {
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(value)
}

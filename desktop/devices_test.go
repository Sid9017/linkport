package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Sid9017/linkport/internal/engine"
)

func TestDeviceManagementRequiresNativeTokenAndNeverExposesSeed(t *testing.T) {
	live, err := engine.New(context.Background(), engine.Options{
		Directory: t.TempDir(), DeviceDirectory: t.TempDir(), DisableNetworkListener: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	api := &nativeAPI{engine: live, token: "local-device-management-token"}
	handler := api.handler()
	for _, endpoint := range []string{
		"/network", "/devices", "/devices/invite", "/identity/reset",
	} {
		request := httptest.NewRequest(http.MethodPost, "/native/v1"+endpoint, strings.NewReader("{}"))
		request.Header.Set("X-Linkport-Token", "forged")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusForbidden {
			t.Fatalf("unauthorized device management: %s: %d", endpoint, response.Code)
		}
	}
	request := httptest.NewRequest(http.MethodGet, "/native/v1/network", nil)
	request.Header.Set("X-Linkport-Token", api.token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var status map[string]any
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &status) != nil {
		t.Fatal("device status unavailable")
	}
	if status["nodeID"] == "" || len(status["nodeID"].(string)) != 64 || strings.Contains(response.Body.String(), `"seed"`) {
		t.Fatal("device status does not expose only the public identity")
	}
	for _, confirmation := range []string{"wrong", status["nodeID"].(string)} {
		body, _ := json.Marshal(map[string]string{"confirmation": confirmation})
		request := httptest.NewRequest(http.MethodPost, "/native/v1/identity/reset", strings.NewReader(string(body)))
		request.Header.Set("X-Linkport-Token", api.token)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if confirmation == "wrong" && response.Code != http.StatusConflict {
			t.Fatal("identity reset bypassed full-ID confirmation")
		}
		if confirmation != "wrong" && (response.Code != http.StatusOK || live.Network().Status().NodeID == confirmation) {
			t.Fatal("confirmed identity reset failed")
		}
	}
}

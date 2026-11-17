package design

import (
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
)

func TestPreviewUsesHostPlatformAndOfflineAssets(t *testing.T) {
	handler := Handler()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("preview response: %d", response.Code)
	}
	body := response.Body.String()
	if !strings.Contains(body, `name="linkport-host-platform" content="`+runtime.GOOS+`"`) {
		t.Fatal("preview does not declare the actual host platform")
	}
	if strings.Contains(body, "https://unpkg") || strings.Contains(body, "data-platform>") {
		t.Fatal("preview still depends on an external icon script or manual OS selection")
	}
	for _, asset := range []string{"SpaceGrotesk.ttf", "IBMPlexMono.ttf"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/assets/fonts/"+asset, nil))
		if response.Code != http.StatusOK || !strings.HasPrefix(response.Body.String(), "\x00\x01\x00\x00") {
			t.Fatalf("%s is not a bundled TrueType font: %d", asset, response.Code)
		}
	}
}

func TestNativeDocumentInitializesWindowDragRuntime(t *testing.T) {
	for _, desktop := range []bool{false, true} {
		handler := Handler()
		if desktop {
			handler = DesktopHandlerWithToken("test-token")
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
		body := response.Body.String()
		runtimeIncluded := strings.Contains(body, `<script type="module" src="/wails/runtime.js"></script>`)
		if runtimeIncluded != desktop {
			t.Fatalf("runtime included=%v for desktop=%v", runtimeIncluded, desktop)
		}
		if desktop && (!strings.Contains(body, "--wails-draggable:drag") || !strings.Contains(body, "--wails-draggable:no-drag")) {
			t.Fatal("native document must distinguish title bar dragging from button interaction")
		}
	}
}

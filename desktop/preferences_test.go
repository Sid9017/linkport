package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestPreferencesPersistAndValidate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", "desktop.json")
	store, err := newPreferenceStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if store.get() != defaultPreferences() {
		t.Fatal("unexpected defaults")
	}
	settings := preferences{FontSize: 17, Language: "en", Theme: "dark", CloseToTray: true, AutoUpdate: false}
	if err := store.save(settings); err != nil {
		t.Fatal(err)
	}
	reloaded, err := newPreferenceStore(path)
	if err != nil || reloaded.get() != settings {
		t.Fatalf("preferences not persisted: %v", err)
	}
	invalid := settings
	invalid.FontSize = 99
	if err := store.save(invalid); err == nil {
		t.Fatal("invalid font size accepted")
	}
	if store.get() != settings {
		t.Fatal("invalid update changed preferences")
	}
}

func TestLanguageForSystemLocale(t *testing.T) {
	for _, test := range []struct{ locale, want string }{
		{"zh-CN", "zh-CN"}, {"zh-Hant-TW", "zh-CN"}, {"zh_HK.UTF-8", "zh-CN"},
		{" ZH_sg ", "zh-CN"}, {"zh_CN:en_US", "zh-CN"},
		{"en-US", "en"}, {"en_GB.UTF-8", "en"}, {"fr-FR", "en"},
		{"ja-JP", "en"}, {"de_DE:zh_CN", "en"}, {"C.UTF-8", "en"},
		{"", "en"}, {"zho", "en"},
	} {
		t.Run(test.locale, func(t *testing.T) {
			if got := languageForLocale(test.locale); got != test.want {
				t.Fatalf("locale %q: got %q, want %q", test.locale, got, test.want)
			}
		})
	}
}

func TestSavedLanguageOverridesSystemDefault(t *testing.T) {
	for _, language := range []string{"zh-CN", "en"} {
		t.Run(language, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "desktop.json")
			store, err := newPreferenceStore(path)
			if err != nil {
				t.Fatal(err)
			}
			settings := store.get()
			settings.Language = language
			if err := store.save(settings); err != nil {
				t.Fatal(err)
			}
			reloaded, err := newPreferenceStore(path)
			if err != nil || reloaded.get().Language != language {
				t.Fatalf("saved language was replaced by system default: %v", err)
			}
		})
	}
}

func TestNativeAPIRequiresLaunchToken(t *testing.T) {
	store, err := newPreferenceStore(filepath.Join(t.TempDir(), "desktop.json"))
	if err != nil {
		t.Fatal(err)
	}
	api := &nativeAPI{token: "test-launch-token", preferences: store}
	for _, supplied := range []string{"", "wrong-token", "test-launch-token"} {
		request := httptest.NewRequest(http.MethodGet, "/native/v1/preferences", nil)
		request.Header.Set("X-Linkport-Token", supplied)
		response := httptest.NewRecorder()
		api.handler().ServeHTTP(response, request)
		want := http.StatusForbidden
		if supplied == api.token {
			want = http.StatusOK
		}
		if response.Code != want {
			t.Fatalf("got %d, want %d", response.Code, want)
		}
	}
}

func TestNativeAPIRejectsUnknownPreferenceFields(t *testing.T) {
	store, err := newPreferenceStore(filepath.Join(t.TempDir(), "desktop.json"))
	if err != nil {
		t.Fatal(err)
	}
	api := &nativeAPI{token: "test-token", preferences: store}
	request := httptest.NewRequest(http.MethodPut, "/native/v1/preferences",
		bytes.NewBufferString(`{"fontSize":13,"language":"en","theme":"system","closeToTray":true,"autoUpdate":true,"command":"unexpected"}`))
	request.Header.Set("X-Linkport-Token", api.token)
	response := httptest.NewRecorder()
	api.handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || store.get() != defaultPreferences() {
		t.Fatal("unknown setting was accepted")
	}
}

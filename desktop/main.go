package main

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"

	"github.com/Sid9017/linkport/docs/design"
	"github.com/Sid9017/linkport/internal/engine"
	"github.com/Sid9017/linkport/internal/version"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/updater"
)

//go:embed assets/app.png
var appIcon []byte

//go:embed assets/tray.png
var trayIcon []byte

func main() {
	updater.HandleHelperMode()
	printVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *printVersion {
		fmt.Println(version.String())
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	directory, err := os.UserConfigDir()
	if err != nil {
		log.Fatal(err)
	}
	settings, err := newPreferenceStore(filepath.Join(directory, "linkport", "desktop.json"))
	if err != nil {
		log.Fatal(err)
	}
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		log.Fatal(err)
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	api := &nativeAPI{ctx: ctx, token: token, preferences: settings}
	handler := http.NewServeMux()
	handler.Handle("/native/v1/", api.handler())
	handler.Handle("/", design.DesktopHandlerWithToken(token))
	var window *application.WebviewWindow
	showWindow := func() {
		if window != nil {
			window.Show()
			window.Focus()
		}
	}
	app := application.New(application.Options{
		Name:        "Linkport",
		Description: "Private serial sharing between your paired devices",
		Icon:        appIcon,
		Assets: application.AssetOptions{
			Handler:        handler,
			DisableLogging: true,
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
		OnShutdown: cancel,
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: "com.linkport.desktop",
			OnSecondInstanceLaunch: func(application.SecondInstanceData) {
				showWindow()
			},
		},
	})
	// Wails forwards a second launch to the existing instance in New. Acquire
	// the device-store lock only after that check, so reopening still focuses
	// the existing window instead of failing on its identity lock.
	live, err := engine.New(ctx, engine.Options{Directory: filepath.Join(directory, "linkport")})
	if err != nil {
		log.Fatal(err)
	}
	defer live.Close()
	api.engine = live
	api.exportLog = func(identity string) (string, error) {
		contents, err := live.Sessions().Export(identity)
		if err != nil {
			return "", err
		}
		path, err := app.Dialog.SaveFile().SetFilename("linkport-"+identity+".log").AddFilter("Serial log", "*.log").PromptForSingleSelection()
		if err != nil || path == "" {
			return "", err
		}
		if err := os.WriteFile(path, contents, 0600); err != nil {
			return "", err
		}
		return filepath.Base(path), nil
	}
	api.updates, err = newUpdateController(ctx, app, settings)
	if err != nil {
		log.Fatal(err)
	}
	api.updates.start()
	api.relayUpdates = newRelayUpdateController(ctx, live.Network(), settings)
	api.relayUpdates.start()
	navigate := func(screen string) {
		showWindow()
		window.ExecJS(fmt.Sprintf("document.querySelector('[data-nav=\"%s\"]')?.click()", screen))
	}
	window = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "main",
		Title:            "Linkport",
		Width:            1120,
		Height:           780,
		MinWidth:         760,
		MinHeight:        540,
		URL:              "/",
		BackgroundColour: application.NewRGB(255, 255, 255),
		Mac: application.MacWindow{
			TitleBar: application.MacTitleBarHiddenInset,
		},
		KeyBindings: map[string]func(application.Window){
			"CmdOrCtrl+1": func(application.Window) { navigate("connect") },
			"CmdOrCtrl+2": func(application.Window) { navigate("share") },
			"CmdOrCtrl+3": func(application.Window) { navigate("monitor") },
			"CmdOrCtrl+4": func(application.Window) { navigate("plugins") },
		},
	})
	window.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		if settings.get().CloseToTray {
			event.Cancel()
			window.Hide()
		} else {
			app.Quit()
		}
	})
	app.Event.OnApplicationEvent(events.Mac.ApplicationShouldHandleReopen, func(*application.ApplicationEvent) {
		showWindow()
	})
	menu := app.NewMenu()
	if runtime.GOOS == "darwin" {
		menu.AddRole(application.AppMenu)
	}
	menu.AddRole(application.EditMenu)
	viewMenu := menu.AddSubmenu("视图")
	type menuLabel struct {
		item    *application.MenuItem
		chinese string
		english string
	}
	var labels []menuLabel
	rememberLabel := func(item *application.MenuItem, chinese, english string) {
		labels = append(labels, menuLabel{item: item, chinese: chinese, english: english})
	}
	for _, item := range []struct{ label, english, screen string }{
		{"连接", "Connect", "connect"}, {"共享", "Share", "share"}, {"监听", "Monitor", "monitor"}, {"插件", "Plugins", "plugins"},
	} {
		entry := viewMenu.Add(item.label).OnClick(func(*application.Context) { navigate(item.screen) })
		rememberLabel(entry, item.label, item.english)
	}
	menu.AddRole(application.WindowMenu)
	app.Menu.Set(menu)
	tray := app.SystemTray.New()
	if runtime.GOOS == "darwin" {
		tray.SetTemplateIcon(trayIcon)
	} else {
		tray.SetIcon(appIcon)
	}
	tray.SetTooltip("Linkport " + version.Version)
	trayMenu := app.NewMenu()
	trayMenu.Add("Linkport " + version.Version).SetEnabled(false)
	trayMenu.AddSeparator()
	entry := trayMenu.Add("设置").OnClick(func(*application.Context) {
		showWindow()
		window.ExecJS("document.querySelector('[data-settings-open]')?.click()")
	})
	rememberLabel(entry, "设置", "Settings")
	entry = trayMenu.Add("添加设备").OnClick(func(*application.Context) {
		showWindow()
		window.ExecJS("document.querySelector('[data-device-manager-open]')?.click()")
	})
	rememberLabel(entry, "添加设备", "Add device")
	entry = trayMenu.Add("检查更新").OnClick(func(*application.Context) {
		showWindow()
		window.ExecJS("document.querySelector('[data-settings-open]')?.click()")
		_ = api.updates.runJob(false)
	})
	rememberLabel(entry, "检查更新", "Check for updates")
	trayMenu.AddSeparator()
	entry = trayMenu.Add("显示窗口").OnClick(func(*application.Context) { showWindow() })
	rememberLabel(entry, "显示窗口", "Show window")
	for _, item := range []struct{ label, english, screen string }{
		{"连接", "Connect", "connect"}, {"共享", "Share", "share"}, {"监听", "Monitor", "monitor"},
	} {
		entry := trayMenu.Add(item.label).OnClick(func(*application.Context) { navigate(item.screen) })
		rememberLabel(entry, item.label, item.english)
	}
	trayMenu.AddSeparator()
	entry = trayMenu.Add("退出 Linkport").OnClick(func(*application.Context) { app.Quit() })
	rememberLabel(entry, "退出 Linkport", "Quit Linkport")
	tray.SetMenu(trayMenu)
	tray.OnDoubleClick(showWindow)
	localizeMenu := func(preferences preferences) {
		if preferences.Language == "en" {
			viewMenu.SetLabel("View")
		} else {
			viewMenu.SetLabel("视图")
		}
		for _, label := range labels {
			if preferences.Language == "en" {
				label.item.SetLabel(label.english)
			} else {
				label.item.SetLabel(label.chinese)
			}
		}
	}
	api.onPreferencesChanged = localizeMenu
	localizeMenu(settings.get())
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		if err := installBundledCLI(); err != nil {
			log.Printf("CLI installation failed: %v", err)
			app.Dialog.Error().SetTitle("Linkport CLI setup").SetMessage("Could not register the bundled CLI. Desktop remains available.\n\n" + err.Error()).Show()
		}
	})
	go live.Run()
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

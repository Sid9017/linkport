package design

import (
	"embed"
	"fmt"
	"html"
	"net/http"
	"os"
	"runtime"
	"strings"

	"github.com/Sid9017/linkport/internal/version"
)

//go:embed desktop-review.fragment.html
var fragment string

//go:embed assets/live.js
var liveScript string

//go:embed assets/monitor.js
var monitorScript string

//go:embed assets/fonts
var assets embed.FS

func Handler() http.Handler {
	return newHandler(func() (string, error) { return fragment, nil }, false, "")
}

func DesktopHandler() http.Handler {
	return DesktopHandlerWithToken("")
}

func DesktopHandlerWithToken(token string) http.Handler {
	return newHandler(func() (string, error) { return fragment, nil }, true, token)
}

func SourceHandler(source string) http.Handler {
	return newHandler(func() (string, error) {
		content, err := os.ReadFile(source)
		return string(content), err
	}, false, "")
}

func newHandler(readSource func() (string, error), desktop bool, token string) http.Handler {
	files := http.FileServer(http.FS(assets))
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, "/assets/fonts/") {
			files.ServeHTTP(writer, request)
			return
		}
		if request.URL.Path != "/" {
			http.NotFound(writer, request)
			return
		}
		if request.Method != http.MethodGet {
			writer.Header().Set("Allow", http.MethodGet)
			http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		content, err := readSource()
		if err != nil {
			http.Error(writer, "design source unavailable", http.StatusInternalServerError)
			return
		}
		content = strings.ReplaceAll(content, "{{LINKPORT_VERSION}}", html.EscapeString(version.Version))
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		policy := "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; font-src 'self'; img-src data:; connect-src 'none'; form-action 'none'; base-uri 'none'; frame-ancestors 'none'"
		if desktop {
			policy = "default-src 'none'; style-src 'unsafe-inline'; script-src 'self' 'unsafe-inline'; font-src 'self'; img-src 'self' data:; connect-src 'self'; form-action 'none'; base-uri 'none'; frame-ancestors 'none'"
		}
		writer.Header().Set("Content-Security-Policy", policy)
		_, _ = fmt.Fprintf(writer, `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="linkport-host-platform" content="%s"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Linkport %s</title><style>:root{color-scheme:light dark}html,body{margin:0;padding:0;width:100%%;height:100%%;overflow:hidden;background:light-dark(#fff,#1b1b1b)}</style>`, runtime.GOOS, html.EscapeString(version.Version))
		if desktop {
			_, _ = fmt.Fprintf(writer, `<meta name="linkport-desktop" content="true"><meta name="linkport-api-token" content="%s">`, html.EscapeString(token))
			_, _ = writer.Write([]byte(`<script type="module" src="/wails/runtime.js"></script>`))
		}
		_, _ = writer.Write([]byte(`</head><body>`))
		_, _ = fmt.Fprintf(writer, "<script>%s</script>", monitorScript)
		if desktop {
			_, _ = fmt.Fprintf(writer, "<script>%s</script>", liveScript)
		}
		_, _ = writer.Write([]byte(content))
		if desktop {
			_, _ = writer.Write([]byte(`<style>#linkport-review .lp-titlebar{--wails-draggable:drag;user-select:none;-webkit-user-select:none}#linkport-review .lp-brand{pointer-events:none}#linkport-review .lp-titlebar button{--wails-draggable:no-drag}</style>`))
			if runtime.GOOS == "darwin" {
				_, _ = writer.Write([]byte(`<style>#linkport-review .lp-titlebar{padding-left:108px;min-height:52px}</style>`))
			}
		}
		_, _ = writer.Write([]byte(`</body></html>`))
	})
}

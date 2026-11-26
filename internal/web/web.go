// Package web 内嵌前端构建产物（web/dist → internal/web/dist），提供
// 「单二进制同时服务 API 与前端」的 Gin NoRoute 处理器——裸机/VM 部署无
// 反向代理时同一进程直接承载 SPA，Docker 镜像也因此无需单独的前端容器。
//
// 构建链路（Makefile build-all / scripts/deploy）：npm run build →
// scripts/embed_web.go 拷贝 web/dist → internal/web/dist → go build。
// dist 为空（仅 .gitkeep 占位）时 NoRoute 返回构建指引页，不影响 API。
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

//go:embed all:dist
var distFS embed.FS

// appFS dist 根文件系统；fs.Sub 失败仅可能在编译期约束被破坏时发生，忽略。
var appFS, _ = fs.Sub(distFS, "dist")

// Handler SPA 静态资源处理器（NoRoute）：
//   - 真实存在的文件按 FileServer 直出（vite 产物带内容哈希 → 长缓存 immutable）；
//   - index.html 不缓存（发版即刻生效）；
//   - 其余路径（/login、/kbs/1 等前端路由）回退 index.html，由前端路由接管；
//   - /api/* 不会到达此处（已注册具体路由；未匹配的 /api 路径 404 由 gin 默认处理）。
func Handler() gin.HandlerFunc {
	fileServer := http.FileServer(http.FS(appFS))
	return func(c *gin.Context) {
		p := strings.TrimPrefix(c.Request.URL.Path, "/")
		if p == "" {
			p = "index.html"
		}
		if st, err := fs.Stat(appFS, p); err == nil && !st.IsDir() {
			if p != "index.html" {
				c.Header("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				c.Header("Cache-Control", "no-cache")
			}
			fileServer.ServeHTTP(c.Writer, c.Request)
			return
		}
		// 回退 index.html（SPA 路由 / 不存在的路径都交给前端路由）；
		// dist 未构建（无 index.html）时返回指引，避免「服务正常但页面空白」无从排查。
		if _, err := fs.Stat(appFS, "index.html"); err != nil {
			c.Header("Cache-Control", "no-cache")
			c.Data(http.StatusNotFound, "text/html; charset=utf-8", []byte(notBuiltNotice))
			return
		}
		c.Header("Cache-Control", "no-cache")
		c.Request.URL.Path = "/"
		fileServer.ServeHTTP(c.Writer, c.Request)
	}
}

// notBuiltNotice embed 产物为空时的指引页。
const notBuiltNotice = `<!doctype html><html lang="zh"><head><meta charset="utf-8"><title>Woodpecker</title></head>
<body style="font-family:system-ui,sans-serif;max-width:42rem;margin:18vh auto;padding:0 1rem;line-height:1.7;color:#333">
<h1>后端运行中，但前端资源未构建</h1>
<p>API 正常服务（<code>/api/v1/healthz</code>），但内嵌的前端产物（internal/web/dist）为空。</p>
<p>二选一：</p>
<ul>
<li>仓库根目录执行 <code>make build-all</code>（构建前端并内嵌后重编译），或部署脚本 install/upgrade（已自动执行该链路）；</li>
<li>或用 nginx 单独托管前端并反代 API：模板见 <code>scripts/deploy/nginx/woodpecker.conf.template</code>。</li>
</ul>
</body></html>`

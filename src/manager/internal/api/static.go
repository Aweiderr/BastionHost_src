package api

import (
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"bastion.local/shared/httpx"
)

// registerStaticRoutes 托管 Web 客户端构建产物。
//
// URL 形态：`http://<host>:<port>/<prefix>/`（prefix 默认 bastion）。
//
// 为什么不再占用根路径：
//   - 同一台服务器上常常还要并存其它服务，根路径被独占会互相打架；
//   - 从地址一眼能看出访问的是哪个系统，避免把其它服务的页面误当堡垒机；
//   - 根路径 / 保留一个 302 跳转到控制台，运维记不住前缀时也能进来。
//
// 前端为 SPA：真实存在的静态文件直接返回，其余路径回落 index.html；
// 但 /api、/internal、探针等前缀必须返回 JSON 404，不能被 SPA 接管，
// 否则前端会把接口错误当成 HTML 解析，排查困难。
func registerStaticRoutes(mux *http.ServeMux, dir, prefix, downloadDir string) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return
	}
	if st, err := os.Stat(abs); err != nil || !st.IsDir() {
		return
	}

	// 规范化为 "/bastion/" 形式
	base := "/" + strings.Trim(strings.TrimSpace(prefix), "/") + "/"
	if base == "//" {
		base = "/"
	}
	rootNoSlash := strings.TrimSuffix(base, "/")

	// 入口 HTML 带 mtime 缓存：既避免 FileServer 对 /index.html 做 301 回目录
	//（会造成重定向循环），又保证前端重新构建后**无需重启管理服务**即可生效。
	indexPath := filepath.Join(abs, "index.html")
	var (
		cacheMu   sync.RWMutex
		cacheData []byte
		cacheMod  time.Time
	)
	loadIndex := func() []byte {
		st, err := os.Stat(indexPath)
		if err == nil {
			cacheMu.RLock()
			fresh := cacheData != nil && !st.ModTime().After(cacheMod)
			data := cacheData
			cacheMu.RUnlock()
			if fresh {
				return data
			}
			if raw, rerr := os.ReadFile(indexPath); rerr == nil {
				cacheMu.Lock()
				cacheData, cacheMod = raw, st.ModTime()
				cacheMu.Unlock()
				return raw
			}
		}
		cacheMu.RLock()
		defer cacheMu.RUnlock()
		return cacheData
	}
	if loadIndex() == nil {
		return
	}
	fsys := os.DirFS(abs)
	fileServer := http.FileServer(http.FS(fsys))

	writeIndex := func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		// 入口不缓存，避免前端发版后用户仍拿到旧版本
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		_, _ = w.Write(loadIndex())
	}

	jsonNotFound := func(w http.ResponseWriter) { httpx.FailCode(w, "API_4040") }

	// 下载目录：独立托管，且**绝不回落 SPA**。
	//
	// 这里刻意**不在启动时探测一次**：安装包通常是部署完成之后才上传的，
	// 若启动时目录还不存在就把文件系统置空，会出现"文件明明传上去了、
	// 访问却一直 404，重启服务才好"的怪现象 —— 排查时很难联想到是启动顺序。
	// 因此只在启动时创建目录，是否提供文件由每次请求实时判断。
	var dlDir string
	if downloadDir != "" {
		if dabs, derr := filepath.Abs(downloadDir); derr == nil {
			_ = os.MkdirAll(dabs, 0o755) // 顺手建好，省去部署脚本手工 mkdir
			dlDir = dabs
		}
	}

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			jsonNotFound(w)
			return
		}
		p := r.URL.Path

		// 接口前缀始终返回 JSON，不被 SPA 或重定向接管
		for _, apiPrefix := range []string{"/api/", "/internal/", "/healthz", "/livez", "/readyz"} {
			if strings.HasPrefix(p, apiPrefix) {
				jsonNotFound(w)
				return
			}
		}

		// 根路径与不带尾斜杠的前缀都 302 到控制台
		if p == "/" || p == rootNoSlash {
			http.Redirect(w, r, base, http.StatusFound)
			return
		}

		// 前缀之外的一律 404：不把未知路径悄悄变成控制台页面
		if !strings.HasPrefix(p, base) {
			jsonNotFound(w)
			return
		}

		rel := strings.TrimPrefix(p, base)
		clean := strings.TrimPrefix(path.Clean("/"+rel), "/")
		if clean == "" || clean == "." {
			writeIndex(w)
			return
		}

		// 下载请求：只查文件，找不到就是 404，不回落 SPA。
		//
		// 若回落到 SPA，访问不存在的安装包会得到 200 + index.html ——
		// 浏览器"下载成功"一个 HTML 当客户端，打开是登录页而非程序，
		// 这种问题很难往"文件不存在"上想。
		if clean == "downloads" || strings.HasPrefix(clean, "downloads/") {
			dlPath := strings.TrimPrefix(strings.TrimPrefix(clean, "downloads"), "/")
			if dlDir == "" || dlPath == "" {
				jsonNotFound(w)
				return
			}
			// 实时判断文件是否存在（见上方关于启动顺序的说明）
			if _, serr := os.Stat(filepath.Join(dlDir, filepath.FromSlash(dlPath))); serr == nil {
				http.StripPrefix(base+"downloads/", http.FileServer(http.Dir(dlDir))).ServeHTTP(w, r)
				return
			}
			httpx.FailCode(w, "API_4040")
			return
		}

		// 只有真实文件才交给 FileServer：目录必须回落 SPA，
		// 否则前端路由 /assets 会与构建产物目录 assets/ 撞车，
		// 变成目录列表（信息泄露 + 页面打不开）。
		if f, err := fsys.Open(clean); err == nil {
			st, serr := f.Stat()
			_ = f.Close()
			if serr == nil && !st.IsDir() {
				// 必须改写 URL.Path 再交给 FileServer：
				// 它是拿 r.URL.Path 去 fsys 里找文件的，而 fsys 的根就是 dist，
				// 带着 /bastion 前缀会一律 404 —— 表现是入口 HTML 能打开、
				// 但 JS/CSS 全部加载失败，页面白屏。
				r2 := r.Clone(r.Context())
				r2.URL.Path = "/" + clean
				fileServer.ServeHTTP(w, r2)
				return
			}
		}
		writeIndex(w)
	})
}

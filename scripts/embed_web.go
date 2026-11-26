//go:build ignore

// embed_web 把 web/dist 拷贝到 internal/web/dist（go:embed 源目录），
// 是「单二进制内嵌前端」构建链路的一环：make build-all / scripts/deploy 调用。
// 用 Go 实现拷贝以保持 Makefile 跨平台（Windows 无 cp -r）。
// 用法：go run scripts/embed_web.go [--clean]
package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	src, dst := "web/dist", filepath.Join("internal", "web", "dist")
	if _, err := os.Stat(filepath.Join(src, "index.html")); err != nil {
		fmt.Fprintf(os.Stderr, "前端产物缺失：%s/index.html 不存在，请先执行 make web-build（cd web && npm run build）\n", src)
		os.Exit(1)
	}
	// 清空目标（保留 .gitkeep），避免上一次构建的哈希文件名残留
	if err := cleanExcept(dst, ".gitkeep"); err != nil {
		fmt.Fprintf(os.Stderr, "清理 %s 失败: %v\n", dst, err)
		os.Exit(1)
	}
	n, err := copyTree(src, dst)
	if err != nil {
		fmt.Fprintf(os.Stderr, "拷贝失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("已拷贝前端产物 %s → %s（%d 个文件），接下来 go build 即内嵌\n", src, dst, n)
}

// cleanExcept 删除 dir 下除 keep 外的全部内容（递归），dir 不存在则跳过。
func cleanExcept(dir, keep string) error {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return os.MkdirAll(dir, 0o755)
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Name() == keep {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// copyTree 递归拷贝目录，返回拷贝的文件数。
func copyTree(src, dst string) (int, error) {
	count := 0
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if strings.HasSuffix(rel, ".gitkeep") && rel != ".gitkeep" {
			return nil
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.Create(target)
		if err != nil {
			return err
		}
		if _, err = io.Copy(out, in); err != nil {
			return err
		}
		if err = out.Close(); err != nil {
			return err
		}
		count++
		return nil
	})
	return count, err
}

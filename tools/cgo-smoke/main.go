// cgo 编译冒烟（M4 风险前置）：验证 tree-sitter Go 绑定（依赖 cgo）能否正常编译与解析。
// 独立模块，不参与主模块构建；CI 在 ubuntu 上跑，Windows 本地无 gcc 时可忽略。
package main

import (
	"context"
	"fmt"
	"os"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/golang"
)

func main() {
	src := []byte("package main\n\nfunc main() {\n\tprintln(\"woodpecker\")\n}\n")

	parser := sitter.NewParser()
	parser.SetLanguage(golang.GetLanguage())
	tree, err := parser.ParseCtx(context.Background(), nil, src)
	if err != nil {
		fmt.Fprintln(os.Stderr, "parse:", err)
		os.Exit(1)
	}
	root := tree.RootNode()
	if root.Type() != "source_file" || root.HasError() {
		fmt.Fprintln(os.Stderr, "unexpected root:", root.Type(), "hasError:", root.HasError())
		os.Exit(1)
	}
	fmt.Println("cgo-smoke OK: root =", root.Type())
}

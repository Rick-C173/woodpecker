package diff

import (
	"os"
	"testing"
	"woodpecker/internal/model"
)

// 测试数据：典型的 git diff 输出
const testDiff = `diff --git a/main.go b/main.go
index 83db48f..a3a2c1b 100644
--- a/main.go
+++ b/main.go
@@ -1,6 +1,20 @@
 package main
 
-func main() {}
+import (
+	"fmt"
+	"os"
+)
+
+func main() {
+	name := os.Getenv("USER")
+	if name == "" {
+		fmt.Println("Hello, World!")
+	}
+	fmt.Println("Done!")
+}
 
-func unusedFunc() {}
\ No newline at end of file
+func newFunc() string {
+	return "hello"
+}
\ No newline at end of file
diff --git a/utils.go b/utils.go
new file mode 100644
index 0000000..c3d2a1b
--- /dev/null
+++ b/utils.go
@@ -0,0 +1,5 @@
+package main
+
+func add(a, b int) int {
+	return a + b
+}
`

func TestParse(t *testing.T) {
	files, err := Parse(testDiff)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(files) != 2 {
		t.Errorf("expected 2 files, got %d", len(files))
	}

	// 第一个文件
	f1 := files[0]
	if f1.NewPath != "main.go" {
		t.Errorf("expected main.go, got %s", f1.NewPath)
	}
	if f1.Status != "modified" {
		t.Errorf("expected modified, got %s", f1.Status)
	}
	if len(f1.Hunks) != 1 {
		t.Errorf("expected 1 hunk in main.go, got %d", len(f1.Hunks))
	}

	hunk := f1.Hunks[0]
	if hunk.OldStart != 1 {
		t.Errorf("expected OldStart=1, got %d", hunk.OldStart)
	}
	if hunk.NewStart != 1 {
		t.Errorf("expected NewStart=1, got %d", hunk.NewStart)
	}

	// 验证新增行和删除行
	addedCount := 0
	removedCount := 0
	for _, line := range hunk.Lines {
		if line.Type == "+" {
			addedCount++
		}
		if line.Type == "-" {
			removedCount++
		}
	}
	if addedCount == 0 {
		t.Error("expected some added lines")
	}
	if removedCount == 0 {
		t.Error("expected some removed lines")
	}

	// 第二个文件（新增文件）
	f2 := files[1]
	if f2.NewPath != "utils.go" {
		t.Errorf("expected utils.go, got %s", f2.NewPath)
	}
	if f2.Status != "added" {
		t.Errorf("expected added, got %s", f2.Status)
	}
}

func TestParseEmpty(t *testing.T) {
	_, err := Parse("")
	if err == nil {
		t.Error("expected error for empty diff")
	}

	_, err = Parse("   ")
	if err == nil {
		t.Error("expected error for whitespace-only diff")
	}
}

func TestParse_MultipleFiles(t *testing.T) {
	diff := `diff --git a/a.go b/a.go
--- a/a.go
+++ b/a.go
@@ -1,3 +1,3 @@
 package p
-var a = 1
+var a = 2
diff --git a/b.go b/b.go
--- a/b.go
+++ b/b.go
@@ -1,3 +1,3 @@
 package p
-var b = 1
+var b = 2
`

	files, err := Parse(diff)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(files) != 2 {
		t.Errorf("expected 2 files, got %d", len(files))
	}
}

func TestParse_RenamedFile(t *testing.T) {
	diff := `diff --git a/old.go b/new.go
similarity index 100%
rename from old.go
rename to new.go
`

	files, err := Parse(diff)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	if files[0].OldPath != "old.go" || files[0].NewPath != "new.go" {
		t.Errorf("expected old.go -> new.go, got %s -> %s", files[0].OldPath, files[0].NewPath)
	}
	if files[0].Status != "renamed" {
		t.Errorf("expected renamed, got %s", files[0].Status)
	}
}

func TestParse_RealDiff(t *testing.T) {
	// 使用项目自带的测试文件
	data, err := os.ReadFile("testdata/sample.diff")
	if err != nil {
		t.Skip("sample.diff not found, skipping real diff test")
	}

	files, err := Parse(string(data))
	if err != nil {
		t.Fatalf("Parse real diff failed: %v", err)
	}

	if len(files) == 0 {
		t.Error("expected at least 1 file from sample.diff")
	}
}

func TestInferStatus(t *testing.T) {
	tests := []struct {
		oldPath  string
		newPath  string
		expected string
	}{
		{"/dev/null", "new.go", "added"},
		{"old.go", "/dev/null", "deleted"},
		{"old.go", "new.go", "renamed"},
		{"main.go", "main.go", "modified"},
		{"", "new.go", "added"},
		{"old.go", "", "deleted"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			fd := &model.FileDiff{OldPath: tt.oldPath, NewPath: tt.newPath}
			got := inferStatus(fd)
			if got != tt.expected {
				t.Errorf("inferStatus(%q, %q) = %q, want %q",
					tt.oldPath, tt.newPath, got, tt.expected)
			}
		})
	}
}

// 确保 Parse 返回的 FileDiff 满足 model.FileDiff 接口
func TestParse_ReturnsPOType(t *testing.T) {
	files, err := Parse(testDiff)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		_ = f.OldPath
		_ = f.NewPath
		_ = f.Status
		_ = f.Hunks
	}
}

// 防止 Generate 文件名匹配错误
func TestParse_BinaryFile(t *testing.T) {
	// 二进制文件 diff 应该返回空 hunk 或被跳过
	diff := `diff --git a/image.png b/image.png
Binary files differ
`
	files, err := Parse(diff)
	if err != nil {
		t.Fatalf("should not error on binary diff: %v", err)
	}
	// 二进制文件没有 hunk，应被视为无变更文件
	for _, f := range files {
		if len(f.Hunks) > 0 {
			t.Logf("binary file has %d hunks (may be acceptable)", len(f.Hunks))
		}
	}
}

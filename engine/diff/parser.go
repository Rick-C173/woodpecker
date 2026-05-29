package diff

import (
	"bufio"
	"fmt"
	"regexp"
	"strings"

	"woodpecker/po"
)

var (
	// diff --git a/path b/path
	reDiffGit = regexp.MustCompile(`^diff --git a/(.+) b/(.+)$`)
	// --- a/path
	reOldFile = regexp.MustCompile(`^--- a/(.+)$`)
	// +++ b/path
	reNewFile = regexp.MustCompile(`^\+\+\+ b/(.+)$`)
	// @@ -oldStart,oldLines +newStart,newLines @@
	reHunkHeader = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@(.*)$`)
	// /dev/null indicates new or deleted file
	reDevNull = regexp.MustCompile(`^--- /dev/null$|^\+\+\+ /dev/null$`)
)

// Parse 解析 git diff 文本，返回 FileDiff 列表
func Parse(diffText string) ([]po.FileDiff, error) {
	if strings.TrimSpace(diffText) == "" {
		return nil, fmt.Errorf("diff text is empty")
	}

	scanner := bufio.NewScanner(strings.NewReader(diffText))
	var files []po.FileDiff
	var currentFile *po.FileDiff
	var currentHunk *po.Hunk
	var oldLineNo, newLineNo int

	for scanner.Scan() {
		line := scanner.Text()

		// 检测新文件开始
		if matches := reDiffGit.FindStringSubmatch(line); matches != nil {
			// 保存上一个文件
			if currentFile != nil {
				if currentHunk != nil {
					currentFile.Hunks = append(currentFile.Hunks, *currentHunk)
				}
				files = append(files, *currentFile)
			}

			currentFile = &po.FileDiff{
				OldPath: matches[1],
				NewPath: matches[2],
			}
			currentHunk = nil
			continue
		}

		if currentFile == nil {
			continue
		}

		// 解析 --- a/path
		if matches := reOldFile.FindStringSubmatch(line); matches != nil {
			currentFile.OldPath = matches[1]
			continue
		}

		// 解析 +++ b/path
		if matches := reNewFile.FindStringSubmatch(line); matches != nil {
			currentFile.NewPath = matches[1]
			continue
		}

		// 检测 /dev/null（新增或删除文件）
		if reDevNull.MatchString(line) {
			continue
		}

		// 解析 hunk header
		if matches := reHunkHeader.FindStringSubmatch(line); matches != nil {
			if currentHunk != nil {
				currentFile.Hunks = append(currentFile.Hunks, *currentHunk)
			}

			oldStart := parseInt(matches[1])
			oldLines := parseIntOrDefault(matches[2], 1)
			newStart := parseInt(matches[3])
			newLines := parseIntOrDefault(matches[4], 1)

			currentHunk = &po.Hunk{
				OldStart: oldStart,
				OldLines: oldLines,
				NewStart: newStart,
				NewLines: newLines,
			}
			oldLineNo = oldStart
			newLineNo = newStart
			continue
		}

		// 解析行内容
		if currentHunk != nil && len(line) > 0 {
			lineType := string(line[0])
			content := line[1:]

			switch lineType {
			case " ":
				// 上下文行（未变更）
				currentHunk.Lines = append(currentHunk.Lines, po.Line{
					Type:      " ",
					Content:   content,
					OldLineNo: oldLineNo,
					NewLineNo: newLineNo,
				})
				oldLineNo++
				newLineNo++
			case "+":
				// 新增行
				currentHunk.Lines = append(currentHunk.Lines, po.Line{
					Type:      "+",
					Content:   content,
					OldLineNo: 0,
					NewLineNo: newLineNo,
				})
				newLineNo++
			case "-":
				// 删除行
				currentHunk.Lines = append(currentHunk.Lines, po.Line{
					Type:      "-",
					Content:   content,
					OldLineNo: oldLineNo,
					NewLineNo: 0,
				})
				oldLineNo++
			case "\\":
				// \ No newline at end of file — 跳过
				continue
			default:
				// 可能是续行或其他，跳过
				continue
			}
		}
	}

	// 保存最后一个文件和 hunk
	if currentHunk != nil && currentFile != nil {
		currentFile.Hunks = append(currentFile.Hunks, *currentHunk)
	}
	if currentFile != nil {
		// 推断文件状态
		currentFile.Status = inferStatus(currentFile)
		files = append(files, *currentFile)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan diff text: %w", err)
	}

	if len(files) == 0 {
		return nil, fmt.Errorf("no files found in diff")
	}

	return files, nil
}

// inferStatus 根据路径推断文件变更状态
func inferStatus(fd *po.FileDiff) string {
	if fd.OldPath == "/dev/null" || fd.OldPath == "" {
		return "added"
	}
	if fd.NewPath == "/dev/null" || fd.NewPath == "" {
		return "deleted"
	}
	if fd.OldPath != fd.NewPath {
		return "renamed"
	}
	return "modified"
}

func parseInt(s string) int {
	var n int
	fmt.Sscanf(s, "%d", &n)
	return n
}

func parseIntOrDefault(s string, defaultVal int) int {
	if s == "" {
		return defaultVal
	}
	return parseInt(s)
}

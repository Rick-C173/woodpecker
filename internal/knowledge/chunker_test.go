package knowledge

import (
	"testing"
)

func TestDetectLanguage(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"test.go", "go"},
		{"test.py", "python"},
		{"test.js", "javascript"},
		{"test.ts", "javascript"},
		{"test.java", "java"},
		{"test.txt", "text"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := DetectLanguage(tt.path); got != tt.want {
				t.Errorf("DetectLanguage(%s) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestGoChunker_Chunk(t *testing.T) {
	chunker := NewGoChunker(DefaultConfig())

	input := `package main

import "fmt"

// Add is a function
func Add(a, b int) int {
	return a + b
}

type Person struct {
	Name string
	Age  int
}

func (p *Person) Greet() string {
	return fmt.Sprintf("Hello, %s", p.Name)
}

func main() {
	fmt.Println("Hello, World!")
}
`

	chunks, err := chunker.Chunk(input, "go")
	if err != nil {
		t.Fatalf("Chunk failed: %v", err)
	}

	if len(chunks) == 0 {
		t.Fatal("no chunks returned")
	}

	var hasFile, hasFunc bool
	for _, c := range chunks {
		switch c.ChunkType {
		case ChunkTypeFile:
			hasFile = true
		case ChunkTypeFunc:
			hasFunc = true
		}
	}

	if !hasFile {
		t.Error("no file chunk found")
	}
	if !hasFunc {
		t.Error("no func chunk found")
	}
}

func TestPythonChunker_Chunk(t *testing.T) {
	chunker := &PythonChunker{config: DefaultConfig()}

	input := `#!/usr/bin/env python3
"""Test module"""

class TestClass:
    """Test class docstring"""
    
    def method(self):
        return 42

def add(a, b):
    """Add two numbers"""
    return a + b

if __name__ == "__main__":
    print(add(1, 2))
`

	chunks, err := chunker.Chunk(input, "python")
	if err != nil {
		t.Fatalf("Chunk failed: %v", err)
	}

	if len(chunks) == 0 {
		t.Fatal("no chunks returned")
	}
}

func TestJSChunker_Chunk(t *testing.T) {
	chunker := &JSChunker{config: DefaultConfig()}

	input := `function test() {
    console.log("test");
}

class MyClass {
    constructor(name) {
        this.name = name;
    }
    
    greet() {
        return "Hello";
    }
}

const add = (a, b) => a + b;
`

	chunks, err := chunker.Chunk(input, "javascript")
	if err != nil {
		t.Fatalf("Chunk failed: %v", err)
	}

	if len(chunks) == 0 {
		t.Fatal("no chunks returned")
	}
}

func TestGenericChunker(t *testing.T) {
	chunker := NewGenericChunker(DefaultConfig())

	goInput := `
package test
func Test() {}
`

	chunks, err := chunker.Chunk(goInput, "go")
	if err != nil {
		t.Fatalf("Chunk go failed: %v", err)
	}
	if len(chunks) == 0 {
		t.Error("no go chunks")
	}

	pyInput := `
def test():
    pass
`

	chunks, err = chunker.Chunk(pyInput, "python")
	if err != nil {
		t.Fatalf("Chunk py failed: %v", err)
	}
	if len(chunks) == 0 {
		t.Error("no py chunks")
	}
}

func TestExtractFuncName(t *testing.T) {
	tests := []struct {
		line string
		want string
	}{
		{"func Test() {}", "Test"},
		{"func (r *Repo) Method() {}", ""},
	}

	for _, tt := range tests {
		got := extractFuncName(tt.line)
		if got != tt.want {
			t.Errorf("extractFuncName(%s) = %q, want %q", tt.line, got, tt.want)
		}
	}
}

func TestExtractStructName(t *testing.T) {
	tests := []struct {
		line string
		want string
	}{
		{"type User struct {}", "User"},
		{"type User struct {", "User"},
	}

	for _, tt := range tests {
		got := extractStructName(tt.line)
		if got != tt.want {
			t.Errorf("extractStructName(%s) = %q, want %q", tt.line, got, tt.want)
		}
	}
}

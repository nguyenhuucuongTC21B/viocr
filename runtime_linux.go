//go:build !windows

package main

// runtime_linux.go — nhúng libonnxruntime.so (ONNX Runtime cho Linux x64).

import "embed"

//go:embed assets/libonnxruntime.so
var runtimeFS embed.FS

// runtimeLibName tên file runtime lib của nền tảng hiện tại.
func runtimeLibName() string { return "libonnxruntime.so" }

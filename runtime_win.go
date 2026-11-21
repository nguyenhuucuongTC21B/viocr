//go:build windows

package main

// runtime_win.go — nhúng onnxruntime.dll (ONNX Runtime cho Windows x64).

import "embed"

//go:embed assets/onnxruntime.dll
var runtimeFS embed.FS

// runtimeLibName tên file runtime lib của nền tảng hiện tại.
func runtimeLibName() string { return "onnxruntime.dll" }

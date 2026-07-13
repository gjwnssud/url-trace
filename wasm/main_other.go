//go:build !(js && wasm)

// This file exists only so `go build ./...`, `go vet ./...`, and `go test
// ./...` succeed on ordinary host platforms: package main here has no
// buildable files at all once main_js.go's "js && wasm" constraint excludes
// it, and Go refuses to compile a package with zero matching files. The real
// bridge lives in main_js.go; build it explicitly with:
//
//	GOOS=js GOARCH=wasm go build -o extension/public/url-trace.wasm ./wasm
package main

func main() {}

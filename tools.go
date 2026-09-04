//go:build tools

//go:generate go build -o ./bin/mockery github.com/vektra/mockery/v2
//go:generate go build -o ./bin/gofumpt mvdan.cc/gofumpt
//go:generate go build -o ./bin/golangci-lint github.com/golangci/golangci-lint/cmd/golangci-lint
//go:generate go build -o ./bin/swag github.com/swaggo/swag/cmd/swag
//go:generate ./bin/swag init --parseDependency -g ./cmd/baas/main.go -o internal/docs

// this file references indirect dependencies that are used during the build

package main

import (
	_ "github.com/golangci/golangci-lint/cmd/golangci-lint"
	_ "github.com/swaggo/swag/cmd/swag"
	_ "github.com/vektra/mockery/v2"
	_ "mvdan.cc/gofumpt"
)

package main

import (
	"context"
	"fmt"
	"os"

	"github.com/Ursa-Minor-Beta/baas/internal/service"
)

// @securityDefinitions.apikey Bearer
// @in header
// @name Authorization
// @description Type "Bearer" followed by a space and API token.
func main() {
	if srv, err := service.New(context.Background()); err != nil {
		fatalError(err)
	} else if err := srv.Start(); err != nil {
		fatalError(err)
	}
}

func fatalError(err error) {
	_, _ = os.Stderr.WriteString(fmt.Sprintf("Failed to init service: %v\n", err))
	os.Exit(1)
}

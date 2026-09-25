package service

import (
	"context"
	"fmt"
	"net/http"

	"github.com/pkg/errors"

	"github.com/simple-container-com/go-aws-lambda-sdk/pkg/service"

	"github.com/Ursa-Minor-Beta/baas/internal/doctools"
)

// withReadBody is service.WithReadBody with one difference: an error from a
// converter this build does not ship answers 501 rather than 500, so clients
// can tell a missing feature from a failure.
func withReadBody[T any, R any](ctx context.Context, s service.Service, c service.HttpAdapter, action string, callback func(cfg *T) (*R, error)) (*R, bool) {
	model, ok := service.ReadBody[T](ctx, s, c)
	if !ok {
		return nil, true
	}
	res, err := callback(model)
	if err != nil {
		c.JSON(errorStatus(err), service.Error{
			Message: fmt.Sprintf("failed to %s: %v", action, err),
			Meta:    s.GetMeta(ctx),
		})
		return res, false
	}
	return res, true
}

func errorStatus(err error) int {
	if errors.Is(err, doctools.ErrUnavailable) {
		return http.StatusNotImplemented
	}
	return http.StatusInternalServerError
}

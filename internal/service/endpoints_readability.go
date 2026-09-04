package service

import (
	"bytes"
	"context"
	"io"
	"net/http"

	"github.com/pkg/errors"
	"github.com/samber/lo"

	"github.com/simple-container-com/go-aws-lambda-sdk/pkg/service"
	"github.com/simple-container-com/go-aws-lambda-sdk/pkg/util/retry"

	"github.com/Ursa-Minor-Beta/baas/pkg/dto"
)

// @Schemes
// @Security Bearer
// @Description process URL by config
// @Tags run
// @Accept json
// @Produce json
// @Param data body dto.ReadabilityConfig true "run config"
// @Success 200 {object} dto.ReadabilityResult
// @Router /api/readability [post]
func (s *Server) readabilityEndpoint(c service.HttpAdapter) error {
	ctx := c.Context()

	if result, ok := service.WithReadBody(ctx, s, c, "readability process", func(cfg *dto.ReadabilityConfig) (*dto.ReadabilityResult, error) {
		cfg.MaxAttempts = lo.If(cfg.MaxAttempts != nil, cfg.MaxAttempts).Else(lo.ToPtr(DefaultAttemptsCount))
		cfg.Timeout = lo.If(cfg.Timeout == "", DefaultTimeout).Else(cfg.Timeout)
		configHash := cfg.Hash()
		ctx = s.Logger().WithValue(ctx, "configHash", configHash)
		if !lo.FromPtr(cfg.DoNotUseCache) {
			if cachedRes, err := s.getReadabilityFromCache(ctx, configHash); err != nil {
				s.Logger().Warnf(s.Logger().WithValue(ctx, "error", err.Error()), "failed to get result from cache")
			} else if cachedRes != nil {
				s.Logger().Infof(ctx, "returning result from cache")
				return cachedRes, nil
			}
		}
		ctx = s.Logger().WithValue(ctx, "config", cfg)
		res, err := retry.With(retry.Config[*dto.ReadabilityResult]{
			Action: func() (*dto.ReadabilityResult, error) {
				return s.doRunReadability(ctx, cfg)
			},
			MaxRetries: lo.FromPtr(cfg.MaxAttempts),
			AttemptErrorCallback: func(i int, err error) {
				ctx := s.Logger().WithValue(ctx, "proxy", lo.FromPtr(cfg.UseProxy))
				ctx = s.Logger().WithValue(ctx, "attempt", i)
				ctx = s.Logger().WithValue(ctx, "error", err.Error())
				s.Logger().Errorf(ctx, "failed to process URL %q with proxy %q, attempt %d out of %d", cfg.URL, lo.FromPtr(cfg.UseProxy), i, lo.FromPtr(cfg.MaxAttempts))
				if lo.FromPtr(cfg.UseRandomProxy) {
					cfg.UseProxy = s.proxy.Host()
				}
			},
			NoMoreAttemptsCallback: func(err error) {
				s.Logger().Errorf(ctx, "failed to process URL %q, no more attempts left", cfg.URL)
			},
		})
		if err != nil {
			return nil, err
		}
		if lo.FromPtr(res).Meta.Error == nil { // only add to cache when there is no error
			if err := s.addToCache(ctx, configHash, lo.FromPtr(res)); err != nil {
				s.Logger().Warnf(s.Logger().WithValue(ctx, "error", err.Error()), "failed to update cache")
			}
		}
		return lo.FromPtr(res), nil
	}); ok && result != nil {
		keepErr := result.Meta.Error
		result.Meta = s.GetMeta(ctx)
		result.Meta.Error = keepErr
		c.JSON(http.StatusOK, result)
	}
	return nil
}

func (s *Server) doRunReadability(ctx context.Context, cfg *dto.ReadabilityConfig) (*dto.ReadabilityResult, error) {
	if cfg == nil {
		return nil, errors.Errorf("config is nil")
	}
	s.Logger().Infof(ctx, "determining document format of request...")
	docFmt, respBody, err := determineFmtType(ctx, cfg)
	if err == nil && docFmt != nil && respBody != nil {
		return s.parseDocument(ctx, cfg, respBody, lo.FromPtr(docFmt))
	}
	s.Logger().Infof(ctx, "fetching url...")
	rawContent, article, err := ReadFromURL(ctx, cfg)
	if err != nil || article.Length == 0 || lo.FromPtr(cfg.ForceUseBrowser) {
		if err != nil {
			ctx = s.Logger().WithValue(ctx, "error", err.Error())
		}
		if cfg.FallbackToBrowser == nil || lo.FromPtr(cfg.FallbackToBrowser) || lo.FromPtr(cfg.ForceUseBrowser) {
			s.Logger().Infof(ctx, "processed URL with plain readability resulted in %v, trying with chrome...", err)
			if !IsSupportedFile(string(lo.FromPtr(docFmt))) {
				return s.readabilityWithChrome(ctx, cfg)
			} else if docBytes, err := s.docDownloadWithChrome(ctx, cfg); err != nil {
				return nil, errors.Wrapf(err, "failed to download document with chrome: %v", err)
			} else {
				docReader := io.NopCloser(bytes.NewReader(docBytes))
				return s.parseDocument(ctx, cfg, docReader, lo.FromPtr(docFmt))
			}
		} else if err != nil {
			s.Logger().Errorf(ctx, "failed to process URL with plain readability (%v), fallback to browser is disabled", err)
			return nil, err
		} else if article.Length == 0 {
			s.Logger().Errorf(ctx, "failed to process URL with plain readability: article's length is 0")
		}
	}
	return s.articleToResult(ctx, cfg, rawContent, dto.ToArticle(lo.FromPtr(article)), false), nil
}

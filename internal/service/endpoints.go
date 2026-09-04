package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/pkg/errors"
	"github.com/samber/lo"

	"github.com/simple-container-com/go-aws-lambda-sdk/pkg/service"
	"github.com/simple-container-com/go-aws-lambda-sdk/pkg/util/retry"

	"github.com/Ursa-Minor-Beta/baas/internal/build"
	"github.com/Ursa-Minor-Beta/baas/internal/exec"
	"github.com/Ursa-Minor-Beta/baas/internal/sessions"
	_ "github.com/Ursa-Minor-Beta/baas/internal/sessions"
	"github.com/Ursa-Minor-Beta/baas/pkg/dto"
)

const (
	DefaultTimeout       = "30s"
	DefaultUserAgent     = "*"
	DefaultAttemptsCount = 3
)

type Docs struct {
	Commands     map[string]string `json:"commands"`
	CommandsJSON string            `json:"commandsJSON"`
}

type Status struct {
	Version string             `json:"version"`
	Status  BaasStatus         `json:"status"`
	Meta    service.ResultMeta `json:"meta"`
}

type BaasStatus struct {
	Status         string `json:"status"`
	BrowserStatus  string `json:"browserStatus"`
	AIPandocCommit string `json:"aipandocCommit,omitempty"`
}

// @Schemes
// @Security Bearer
// @Description checks if session is active
// @Tags async
// @Accept json
// @Produce json
// @Success 200 {object} sessions.IsActive
// @Router /api/async/sessions/active [get]
// @Param id query string true "sessionID to check" example(61c135fa-b604-4ff8-bebc-aefee8155219)
func (s *Server) asyncCheckActive(c service.HttpAdapter) error {
	sessionID := c.Query("id")
	active, err := s.registry.IsSessionActive(c.Context(), sessionID)
	if err != nil {
		return errors.Wrapf(err, "failed to check if session is active")
	}
	c.JSON(http.StatusOK, sessions.IsActive{
		Active: active,
	})
	return nil
}

// @Schemes
// @Security Bearer
// @Description cleans up expired sessions
// @Tags async
// @Accept json
// @Produce json
// @Success 200
// @Router /api/async/sessions/cleanup [post]
func (s *Server) asyncActiveSessionsCleanup(c service.HttpAdapter) error {
	return s.registry.RemoveExpiredSessions(c.Context())
}

// @Schemes
// @Security Bearer
// @Description returns active sessions
// @Tags async
// @Accept json
// @Produce json
// @Success 200 {array} sessions.ActiveSession
// @Router /api/async/sessions [get]
func (s *Server) asyncActiveSessionsEndpoint(c service.HttpAdapter) error {
	sessions, err := s.registry.GetActiveSessions(c.Context())
	if err != nil {
		return errors.Wrapf(err, "failed to get active sessions")
	}
	c.JSON(http.StatusOK, sessions)
	return nil
}

// checkXServerAvailability checks if X server is available using wmctrl command
// This mimics the same check used in entrypoint.sh
func (s *Server) checkXServerAvailability(ctx context.Context) string {
	// First check if wmctrl command exists
	if !exec.CommandExists("wmctrl") {
		s.Logger().Infof(ctx, "wmctrl command not found - likely running outside container environment")
		return "unknown"
	}

	// Get DISPLAY environment variable, default to :99 if not set
	display := os.Getenv("DISPLAY")
	if display == "" {
		display = ":99"
	}

	// Create executor with context
	executor := exec.NewExec(ctx)

	// Try to run wmctrl -m with DISPLAY set, similar to entrypoint.sh
	env := []string{"DISPLAY=" + display}
	output, err := executor.ExecCommand([]string{"wmctrl", "-m"}, exec.Opts{
		Env: env,
	})
	if err != nil {
		// X server is not available
		s.Logger().Warnf(ctx, "X server check failed: %v, output: %s", err, output)
		return "unavailable"
	}

	// X server is available
	s.Logger().Infof(ctx, "X server is available, wmctrl output: %s", output)
	return "available"
}

// @Schemes
// @Description returns documentation
// @Tags docs
// @Accept json
// @Produce json
// @Success 200 {object} Status
// @Router /api/status [get]
func (s *Server) statusEndpoint(c service.HttpAdapter) error {
	ctx := c.Context()

	// Check X server availability
	xServerStatus := s.checkXServerAvailability(ctx)

	// Determine browser status based on X server availability
	var browserStatus string

	if xServerStatus == "available" {
		browserStatus = "ready"
	} else if xServerStatus == "unavailable" {
		browserStatus = "unavailable"
	} else {
		// xServerStatus == "unknown" - likely running outside container
		browserStatus = "unknown"
		s.Logger().Infof(ctx, "X server status unknown - likely running in development environment without container")
	}

	meta := s.GetMeta(ctx)

	c.JSON(http.StatusOK, Status{
		Version: build.Version,
		Status: BaasStatus{
			Status:         "running",
			BrowserStatus:  browserStatus,
			AIPandocCommit: s.aiPandocCommit,
		},
		Meta: meta,
	})
	return nil
}

// @Schemes
// @Description returns documentation
// @Tags docs
// @Accept json
// @Produce json
// @Success 200 {object} Docs
// @Router /api/docs [get]
func (s *Server) docsEndpoint(c service.HttpAdapter) error {
	supportedActions := s.browser.SupportedActions()

	actionsBytes, err := json.Marshal(supportedActions)
	if err != nil {
		return err
	}
	c.JSON(http.StatusOK, Docs{
		Commands:     supportedActions,
		CommandsJSON: string(actionsBytes),
	})
	return nil
}

// @Schemes
// @Security Bearer
// @Description return list of supported actions
// @Tags async
// @Accept json
// @Produce json
// @Success 200 {object} map[string]string
// @Router /api/async/actions [get]
func (s *Server) asyncActionsDocEndpoint(c service.HttpAdapter) error {
	c.JSON(http.StatusOK, s.browser.SupportedActions())
	return nil
}

// @Schemes
// @Security Bearer
// @Description send new message to browser
// @Tags async
// @Accept json
// @Produce json
// @Param data body dto.BrowserMessageIn true "browser message"
// @Success 200 {object} dto.BrowserMessageOut
// @Router /api/async/message [post]
func (s *Server) asyncMessageEndpoint(c service.HttpAdapter) error {
	ctx := c.Context()
	if result, ok := s.withBrowserMessageInBody(ctx, c, "process browser message", func(msg *dto.BrowserMessageIn) (*dto.BrowserMessageOut, error) {
		ctx := s.Logger().WithValue(ctx, "config", msg)
		return s.browser.DoAsyncAndWait(ctx, lo.FromPtr(msg))
	}); ok {
		ctx := s.Logger().WithValue(ctx, "response", result.Sanitized())
		s.Logger().Infof(ctx, "responding to client")
		// result.Meta = s.GetMeta(ctx) // meta is set from async session
		c.JSON(http.StatusOK, result)
	}
	return nil
}

// @Schemes
// @Security Bearer
// @Description stop browser async session
// @Tags async
// @Accept json
// @Produce json
// @Param data body dto.ControlConfig true "run config"
// @Success 200 {object} dto.BrowserMessageOut
// @Router /api/async/stop [post]
func (s *Server) asyncStopEndpoint(c service.HttpAdapter) error {
	ctx := c.Context()
	if result, ok := service.WithReadBody(ctx, s, c, "process baas async request", func(cfg *dto.ControlConfig) (*dto.BrowserMessageOut, error) {
		ctx = s.Logger().WithValue(ctx, "sessionID", cfg.SessionID)
		if cfg.SessionID == "" {
			return nil, errors.Errorf("session id must be provided")
		}
		s.Logger().Infof(ctx, "stopping async session")
		return s.browser.StopAsync(ctx, cfg.SessionID)
	}); ok {
		result.Meta = s.GetMeta(ctx)
		c.JSON(http.StatusOK, result)
	}
	return nil
}

// @Schemes
// @Security Bearer
// @Description start browser async session for async interactions
// @Tags async
// @Accept json
// @Produce text/event-stream
// @Param data body dto.Config true "run config"
// @Success 200 {object} dto.Result
// @Router /api/async/start [post]
func (s *Server) asyncStartEndpoint(c service.HttpAdapter) error {
	ctx := c.Context()
	if result, ok := service.WithReadBody(ctx, s, c, "process baas async request", func(cfg *dto.Config) (*dto.Result, error) {
		cfg = s.withConfigDefaults(ctx, cfg)
		sessionID := lo.If(cfg.SessionID != nil, lo.FromPtr(cfg.SessionID)).Else(s.GetMeta(ctx).RequestUID)
		ctx = s.Logger().WithValue(ctx, "sessionID", sessionID)
		hold := lo.If(cfg.Hold != nil, lo.FromPtr(cfg.Hold)).Else(true)
		c.SetHeader("Content-Type", "text/event-stream")
		c.SetHeader("Cache-Control", "no-cache")
		c.SetHeader("Connection", "keep-alive")
		c.Writer().Flush()
		if cfg.Browser.Timeout == "" {
			cfg.Browser.Timeout = "180s"
		}
		ctx = s.Logger().WithValue(ctx, "config", cfg.Sanitized())
		if active, err := s.registry.IsSessionActive(c.Context(), sessionID); err != nil {
			s.Logger().Warnf(s.Logger().WithValue(ctx, "error", err.Error()), "failed to check active session")
		} else if active {
			s.Logger().Warnf(ctx, "session is still active, not starting it again")
			return &dto.Result{
				Result: &dto.BrowserResponse{
					SessionID: sessionID,
				},
			}, nil
		}
		defer func() {
			s.Logger().Infof(s.Logger().WithValue(ctx, "error", ctx.Err()), "exiting from the async/start endpoint")
		}()
		ticker := time.NewTicker(5 * time.Second)
		quit := make(chan struct{})

		sendHeartbeat := func() {
			statusJson, _ := json.Marshal(dto.BrowserMessageOut{
				Timestamp: time.Now().Format(time.DateTime),
				SessionID: sessionID,
				Meta:      s.GetMeta(ctx),
			})
			_, _ = fmt.Fprintln(c.Writer(), string(statusJson))
			c.Writer().Flush()
			s.Logger().Infof(ctx, "sent heartbeat event")
		}
		go func() {
			defer func() {
				_ = recover() // do not need to panic here
			}()
			for {
				select {
				case <-quit:
					ticker.Stop()
					return
				case <-ticker.C:
					_, _ = fmt.Fprintln(c.Writer(), " ") // send empty string to keep session running
					c.Writer().Flush()
				}
			}
		}()
		parentCtx := ctx
		cfg.Browser.SessionID = sessionID
		startSession := func(ctx context.Context) (*dto.BrowserResponse, error) {
			return s.browser.RunAsync(ctx, sessionID, cfg.Browser, func() service.ResultMeta {
				return s.GetMeta(parentCtx)
			})
		}
		res := &dto.BrowserResponse{
			SessionID: sessionID,
		}
		var err error
		if hold {
			s.Logger().Infof(ctx, "starting async session with holding")
			sendHeartbeat()
			res, err = startSession(ctx)
		} else {
			go func() {
				ctx = context.WithoutCancel(parentCtx)
				s.Logger().Infof(ctx, "starting async session without holding")
				if _, err := startSession(ctx); err != nil {
					s.Logger().Errorf(s.Logger().WithValue(ctx, "error", err.Error()), "failed to start baas session asynchronously")
				}
			}()
		}

		if err != nil {
			s.Logger().Errorf(ctx, "failed to start async session: %v\n", err)
		}

		s.Logger().Infof(ctx, "returning from async session")
		return &dto.Result{
			UsedProxy: lo.FromPtr(cfg.Browser.UseProxy),
			Result:    res,
		}, err
	}); ok {
		result.Meta = s.GetMeta(ctx)
		c.JSON(http.StatusOK, result)
	} else {
		return errors.Errorf("internal failure")
	}
	return nil
}

// @Schemes
// @Security Bearer
// @Description process URL by config
// @Tags run
// @Accept json
// @Produce json
// @Param data body dto.Config true "run config"
// @Success 200 {object} dto.Result
// @Router /api/process [post]
func (s *Server) processEndpoint(c service.HttpAdapter) error {
	ctx := c.Context()
	if result, ok := service.WithReadBody(ctx, s, c, "process URL", func(cfg *dto.Config) (*dto.Result, error) {
		cfg = s.withConfigDefaults(ctx, cfg)
		ctx = s.Logger().WithValue(ctx, "config", cfg.Sanitized())
		res, err := retry.With(retry.Config[*dto.Result]{
			Action: func() (*dto.Result, error) {
				res, err := s.browser.Run(ctx, cfg.Browser)
				return &dto.Result{
					UsedProxy: lo.FromPtr(cfg.Browser.UseProxy),
					Result:    res,
				}, err
			},
			MaxRetries: lo.FromPtr(cfg.MaxAttempts),
			AttemptErrorCallback: func(i int, err error) {
				ctx := s.Logger().WithValue(ctx, "proxy", lo.FromPtr(cfg.Browser.UseProxy))
				ctx = s.Logger().WithValue(ctx, "attempt", i)
				ctx = s.Logger().WithValue(ctx, "error", err.Error())
				s.Logger().Errorf(ctx, "failed to process with proxy %q, attempt %d out of %d", lo.FromPtr(cfg.Browser.UseProxy), i, lo.FromPtr(cfg.MaxAttempts))
			},
			NoMoreAttemptsCallback: func(err error) {
				s.Logger().Errorf(ctx, "failed to process, no more attempts left")
			},
		})
		if err != nil {
			return nil, err
		}
		return *res, nil
	}); ok {
		result.Meta = s.GetMeta(ctx)
		c.JSON(http.StatusOK, result)
	} else {
		return errors.Errorf("internal failure")
	}
	return nil
}

func (s *Server) withBrowserMessageInBody(ctx context.Context, c service.HttpAdapter, action string, callback func(cfg *dto.BrowserMessageIn) (*dto.BrowserMessageOut, error)) (*dto.BrowserMessageOut, bool) {
	var err error
	var res *dto.BrowserMessageOut
	if model, success := s.readBrowserMessageInBody(ctx, c); success {
		res, err = callback(model)
		if err != nil {
			c.JSON(http.StatusInternalServerError, service.Error{
				Message: fmt.Sprintf("failed to %s: %v", action, err),
				Meta:    s.GetMeta(ctx),
			})
			return res, false
		}
	}
	return res, true
}

func (s *Server) withConfigDefaults(ctx context.Context, runConfig *dto.Config) *dto.Config {
	if runConfig.Browser.UseProxy == nil && lo.FromPtr(runConfig.UseRandomProxy) {
		runConfig.Browser.UseProxy = s.proxy.Host()
	}
	runConfig.MaxAttempts = lo.If(runConfig.MaxAttempts != nil, runConfig.MaxAttempts).Else(lo.ToPtr(DefaultAttemptsCount))
	// runConfig.Browser.UserAgent = lo.If(runConfig.Browser.UserAgent == "", DefaultUserAgent).Else(runConfig.Browser.UserAgent)
	runConfig.Browser.Timeout = lo.If(runConfig.Browser.Timeout == "", DefaultTimeout).Else(runConfig.Browser.Timeout)
	runConfig.Browser.EnableExtensions = []dto.ChromeExtensionMode{dto.EnableExtensionsBaas}

	return runConfig
}

func (s *Server) readBrowserMessageInBody(ctx context.Context, c service.HttpAdapter) (*dto.BrowserMessageIn, bool) {
	var browserMessage dto.BrowserMessageIn
	bodyBytes := service.ReadBytes(c.RequestBody())
	if err := json.Unmarshal(bodyBytes, &browserMessage); err != nil {
		if s.IsRequestDebugEnabled() {
			s.Logger().Errorf(ctx, "Failed to unmarshal browser in body: %v, got body: %q", err, string(bodyBytes))
		} else {
			s.Logger().Errorf(ctx, "Failed to unmarshal browser in body: %v", err)
		}
		c.JSON(500, service.Error{
			Message: errors.Wrapf(err, "failed to unmarshal browser message in body").Error(),
		})
		return nil, false
	}
	if browserMessage.Timeout == "" {
		browserMessage.Timeout = DefaultTimeout
	}
	return &browserMessage, true
}

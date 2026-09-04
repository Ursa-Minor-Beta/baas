package proxy

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/net/proxy"
	"golang.org/x/sync/errgroup"

	"github.com/elazarl/goproxy"
	"github.com/pkg/errors"
	"github.com/samber/lo"

	"github.com/simple-container-com/go-aws-lambda-sdk/pkg/logger"
	"github.com/simple-container-com/go-aws-lambda-sdk/pkg/service"

	"github.com/Ursa-Minor-Beta/baas/internal/socks5"
)

type StatusResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

type Service interface {
	Start(ctx context.Context)
	IsAlive() bool
	Host() *string
}

type checker struct {
	log            logger.Logger
	proxyHost      string
	proxyUsername  string
	proxyPassword  string
	isAlive        *atomic.Bool
	localProxyHost string
	isDebug        bool
	parsedProxyURL *url.URL
	scheme         string
}

func NewService(proxyHost string, log logger.Logger, isDebug bool) (Service, error) {
	isAlive := &atomic.Bool{}
	isAlive.Store(true)
	c := &checker{
		log:       log,
		proxyHost: proxyHost,
		isAlive:   isAlive,
		isDebug:   isDebug,
	}
	parsedURL, err := url.Parse(proxyHost)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to parse proxy host")
	}
	if parsedURL.User != nil {
		c.proxyUsername = parsedURL.User.Username()
		c.proxyPassword, _ = parsedURL.User.Password()
	}
	c.scheme = parsedURL.Scheme
	c.parsedProxyURL = parsedURL

	log.Infof(context.Background(), "starting to proxy requests to proxy of type %q", c.scheme)
	return c, nil
}

func (c *checker) Host() *string {
	if c.IsAlive() && c.localProxyHost != "" {
		return lo.ToPtr(c.localProxyHost)
	} else {
		return nil
	}
}

func (c *checker) Start(ctx context.Context) {
	errG, ctx := errgroup.WithContext(ctx)
	errG.Go(func() error {
		ticker := time.NewTicker(10 * time.Second)
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
				if c.proxyHost == "" {
					c.isAlive.Store(false)
				} else if err := c.checkAlive(ctx); err != nil {
					c.log.Errorf(c.log.WithValue(ctx, "error", err.Error()), "proxy error")
					c.isAlive.Store(false)
				} else {
					c.isAlive.Store(true)
				}
			}
		}
	})

	if c.proxyUsername != "" && c.proxyPassword != "" {
		startProxyFunc := func() error {
			return c.startHttpProxy(ctx)
		}
		if c.scheme != "http" {
			startProxyFunc = func() error {
				return c.startSocksProxy(ctx)
			}
		}
		errG.Go(startProxyFunc)
	}
}

func (c *checker) startSocksProxy(ctx context.Context) error {
	freePort, err := c.getFreePort(ctx)
	if err != nil {
		return err
	}
	var serverAddr *net.TCPAddr
	if addr, err := net.ResolveTCPAddr("tcp", fmt.Sprintf(":%d", freePort)); err != nil {
		return err
	} else {
		serverAddr = addr
	}
	c.localProxyHost = fmt.Sprintf("socks5://localhost:%d", freePort)
	server := socks5.NewServer()
	server.EnableUDP()
	server.SetDialerInitFunc(func(r *socks5.Request) proxy.Dialer {
		var auth *proxy.Auth
		if c.proxyUsername != "" && c.proxyPassword != "" {
			auth = &proxy.Auth{
				User:     c.proxyUsername,
				Password: c.proxyPassword,
			}
		}
		dialer, err := proxy.SOCKS5("tcp", c.parsedProxyURL.Host, auth, &net.Dialer{
			Timeout:   60 * time.Second,
			KeepAlive: 30 * time.Second,
		})
		if err != nil {
			c.log.Errorf(c.log.WithValue(ctx, "error", err.Error()), "failed to init socks5 dialer")
		}
		return dialer
	})

	if err := server.Run(serverAddr); err != nil {
		return err
	}
	return nil
}

func (c *checker) startHttpProxy(ctx context.Context) error {
	httpProxy := goproxy.NewProxyHttpServer()
	httpProxy.Verbose = c.isDebug
	freePort, err := c.getFreePort(ctx)
	if err != nil {
		return err
	}

	httpProxy.ConnectDial = httpProxy.NewConnectDialToProxyWithHandler(c.proxyHost, func(req *http.Request) {
		authorization := c.proxyAuthorization()
		req.Header.Set("Proxy-Authorization", authorization)
	})
	if c.parsedProxyURL != nil {
		httpProxy.Tr = &http.Transport{Proxy: http.ProxyURL(c.parsedProxyURL)}
	}
	httpProxy.OnRequest().DoFunc(func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		authorization := c.proxyAuthorization()
		req.Header.Set("Proxy-Authorization", authorization)
		return req, nil
	})
	c.localProxyHost = fmt.Sprintf("localhost:%d", freePort)
	c.log.Infof(ctx, "starting local proxy server at %s", c.localProxyHost)
	if err := http.ListenAndServe(c.localProxyHost, httpProxy); err != nil {
		c.log.Errorf(c.log.WithValue(ctx, "error", err.Error()), "failed to start proxy-proxy-proxy server")
		return errors.Wrapf(err, "failed to start proxy for proxy for proxy")
	}
	return nil
}

func (c *checker) getFreePort(ctx context.Context) (int, error) {
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		c.log.Errorf(c.log.WithValue(ctx, "error", err.Error()), "failed to get free port")
		return 0, errors.Wrapf(err, "failed to get free port")
	}
	defer func() {
		_ = listener.Close()
	}()
	freePort := listener.Addr().(*net.TCPAddr).Port
	return freePort, nil
}

func (c *checker) IsAlive() bool {
	return c.isAlive.Load()
}

func (c *checker) checkAlive(ctx context.Context) error {
	if c.scheme == "http" {
		return c.checkAliveHttp(ctx)
	}
	return c.checkAliveSocks(ctx)
}

func (c *checker) checkAliveSocks(ctx context.Context) error {
	//var auth *proxy.Auth
	//if c.proxyUsername != "" && c.proxyPassword != "" {
	//	auth = &proxy.Auth{User: c.proxyUsername, Password: c.proxyPassword}
	//}
	//dialer, err := proxy.SOCKS5("tcp", c.parsedProxyURL.Host, auth, &net.Dialer{
	//	Timeout:   60 * time.Second,
	//	KeepAlive: 30 * time.Second,
	//})
	//if err != nil {
	//	return errors.Wrapf(err, "failed to init socks5 dialer")
	//}
	dialer := net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 5 * time.Second,
	}
	conn, err := dialer.Dial("tcp", c.parsedProxyURL.Host)
	if conn != nil {
		defer func(conn net.Conn) {
			_ = conn.Close()
		}(conn)
	}
	if err != nil {
		return errors.Wrapf(err, "failed to dial %s", c.parsedProxyURL.Host)
	}
	return nil
}

func (c *checker) checkAliveHttp(ctx context.Context) error {
	client := &http.Client{Timeout: time.Second * 8}

	proxyHost := c.proxyHost
	if !strings.HasPrefix(c.proxyHost, "http") {
		// assuming we're using http proxy
		proxyHost = fmt.Sprintf("http://%s", c.proxyHost)
	}

	statusUrl := fmt.Sprintf("%s/proxy/status", proxyHost)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, statusUrl, nil)
	if err != nil {
		return errors.Wrapf(err, "failed to init request")
	}
	req.Header.Add("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return errors.Wrapf(err, "failed to check status of proxy")
	}
	if resp.StatusCode != http.StatusOK {
		return errors.Errorf("failed to check status of agent: status code %d: %s", resp.StatusCode, string(service.ReadBytes(resp.Body)))
	}

	var agentResponse StatusResponse
	respBytes := service.ReadBytes(resp.Body)
	err = json.Unmarshal(respBytes, &agentResponse)
	if err != nil {
		return errors.Wrapf(err, "failed to unmarshal baas response")
	}

	return nil
}

func (c *checker) proxyAuthorization() string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%s:%s", c.proxyUsername, c.proxyPassword)))
}

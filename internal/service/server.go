package service

import (
	"context"
	"embed"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/mongodb"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/pkg/errors"

	"github.com/simple-container-com/go-aws-lambda-sdk/pkg/awsutil"
	"github.com/simple-container-com/go-aws-lambda-sdk/pkg/service"

	"github.com/Ursa-Minor-Beta/baas/internal/docs"
	"github.com/Ursa-Minor-Beta/baas/internal/messagebus"
	"github.com/Ursa-Minor-Beta/baas/internal/proxy"
	"github.com/Ursa-Minor-Beta/baas/internal/service/html"
	"github.com/Ursa-Minor-Beta/baas/internal/service/markdown"
	"github.com/Ursa-Minor-Beta/baas/internal/service/pdf"
	"github.com/Ursa-Minor-Beta/baas/internal/service/storage"
	"github.com/Ursa-Minor-Beta/baas/internal/sessions"
	"github.com/Ursa-Minor-Beta/baas/internal/tempfiles"
	"github.com/Ursa-Minor-Beta/baas/pkg/dto"
)

const (
	messagesInCollection       = "browser_in"
	messagesOutCollection      = "browser_out"
	defaultDatabaseName        = "baas"
	readabilityCacheCollection = "cache"
	baasMigrationsCollection   = "baas_migrations"
	defaultCacheTTL            = "24h"
)

type Server struct {
	service.Service
	browser              Browser
	inMessages           messagebus.Broker[dto.BrowserMessageIn]
	outMessages          messagebus.Broker[dto.BrowserMessageOut]
	apiKey               string
	aiPandocCommit       string
	proxy                proxy.Service
	readabilityCache     *mongo.Collection
	cacheTTl             time.Duration
	databaseName         string
	mongo                *mongo.Client
	database             *mongo.Database
	pandoc               Pandoc
	registry             sessions.Registry
	pdfParser            pdf.Parser
	markdownRenderer     markdown.Renderer
	pdfToImagesConverter pdf.PdfToImagesConverter
	htmlTemplater        html.HtmlTemplater
	storage              storage.Uploader
	tempFiles            tempfiles.Manager
}

//go:embed embed/migrations
var migrations embed.FS

func New(ctx context.Context) (*Server, error) {
	s := &Server{}
	srv, err := service.New(
		ctx,
		service.WithRoutes(s.registerRoutes),
		service.WithSkipAuthRoutes("/api/swagger", "/favicon.ico", "/api/docs"),
		service.WithLambdaSize(8192), // size of lambda in MB
		service.WithLocalDebugMode(),
		service.WithoutStatusEndpoint(),
	)
	if err != nil {
		return nil, err
	}
	s.Service = srv
	s.aiPandocCommit = os.Getenv("AIPANDOC_COMMIT")

	if apiKey, err := awsutil.GetEnvOrSecret("API_KEY"); err != nil {
		s.Logger().Warnf(ctx, "Failed to get API_KEY secret: %v", err)
	} else {
		s.apiKey = apiKey
	}

	if proxyHost, err := awsutil.GetEnvOrSecret("PROXY_HOST"); err != nil {
		s.Logger().Warnf(ctx, "Failed to get PROXY_HOST secret: %v", err)
	} else if proxySvc, err := proxy.NewService(proxyHost, s.Logger(), s.IsRequestDebugEnabled()); err != nil {
		s.Logger().Errorf(s.Logger().WithValue(ctx, "error", err.Error()), "failed to init proxy")
	} else {
		s.proxy = proxySvc
		s.proxy.Start(ctx)
	}

	mongoUriString, err := awsutil.GetEnvOrSecret("MONGO_URI")
	if err != nil {
		return nil, err
	}
	s.inMessages, err = messagebus.NewMessageBus[dto.BrowserMessageIn](ctx, s.Logger(), mongoUriString, messagesInCollection)
	if err != nil {
		return nil, err
	}
	s.outMessages, err = messagebus.NewMessageBus[dto.BrowserMessageOut](ctx, s.Logger(), mongoUriString, messagesOutCollection)
	if err != nil {
		return nil, err
	}

	mongoUri, err := url.Parse(mongoUriString)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to parse mongo uri")
	} else if mongoUri != nil {
		s.databaseName = strings.TrimPrefix(mongoUri.Path, "/")
		if s.databaseName == "" {
			s.databaseName = defaultDatabaseName
		}
		if client, err := mongo.Connect(ctx, options.Client().ApplyURI(mongoUriString)); err != nil {
			return nil, errors.Wrapf(err, "failed to conect to Mongo")
		} else {
			s.mongo = client
		}
	}

	s.pdfParser, err = s.initPDFParser()
	if err != nil {
		return nil, errors.Wrapf(err, "failed to initialize PDF parser")
	}

	s.markdownRenderer, err = s.initMarkdownRenderer()
	if err != nil {
		ctx := s.Logger().WithValue(context.Background(), "error", err.Error())
		ctx = s.Logger().WithValue(ctx, "SCRIPTS_DIR", os.Getenv("SCRIPTS_DIR"))
		s.Logger().Warnf(ctx, "markdown renderer initialization failed - /api/render-markdown endpoint will not work")
	}

	s.htmlTemplater, err = s.initHtmlTemplater()
	if err != nil {
		ctx := s.Logger().WithValue(context.Background(), "error", err.Error())
		s.Logger().Warnf(ctx, "html templater initialization failed - /api/render-markdown endpoint will not work")
	}

	s.pdfToImagesConverter, err = s.initPdfToImagesConverter()
	if err != nil {
		s.Logger().Warnf(s.Logger().WithValue(context.Background(), "error", err.Error()), "pdf to images converter script not found")
	}

	s.storage = s.initStorage()

	if source, err := iofs.New(migrations, "embed/migrations"); err != nil {
		return nil, errors.Wrapf(err, "failed to read migrations for database")
	} else if migrationsDb, err := mongodb.WithInstance(s.mongo, &mongodb.Config{DatabaseName: s.databaseName, MigrationsCollection: baasMigrationsCollection}); err != nil {
		return nil, errors.Wrapf(err, "failed to open migrations connection")
	} else if m, err := migrate.NewWithInstance("iofs", source, s.databaseName, migrationsDb); err != nil {
		return nil, errors.Wrapf(err, "failed to init migrations")
	} else if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return nil, errors.Wrapf(err, "failed to migrate database")
	} else {
		s.database = s.mongo.Database(s.databaseName)
		s.readabilityCache = s.database.Collection(readabilityCacheCollection)
	}

	if ttl, err := time.ParseDuration(os.Getenv("CACHE_TTL")); err == nil {
		s.cacheTTl = ttl
	} else {
		s.cacheTTl, _ = time.ParseDuration(defaultCacheTTL)
	}

	var extensions []dto.ChromeExtensionManifest
	extensionsDir := os.Getenv("CHROME_EXTENSIONS_DIR")
	if extensionsDir != "" {
		fileInfo, err := os.ReadDir(extensionsDir)
		if err != nil {
			s.Logger().Errorf(s.Logger().WithValue(ctx, "error", err.Error()), "failed to read files from extensions dir")
		}
		for _, file := range fileInfo {
			if file.IsDir() {
				manifestJson := filepath.Join(extensionsDir, file.Name(), "manifest.json")
				manifestBytes, err := os.ReadFile(manifestJson)
				if err != nil {
					return nil, errors.Wrapf(err, "failed to read extension's %q manifest", file.Name())
				}
				extensionData := dto.ChromeExtensionManifest{}
				err = json.Unmarshal(manifestBytes, &extensionData)
				if err != nil {
					return nil, errors.Wrapf(err, "failed to unmarshal extension's %q manifest", file.Name())
				}
				extensionData.DirPath = filepath.Join(extensionsDir, file.Name())
				extensions = append(extensions, extensionData)
			}
		}
	}

	pandocSvc, err := NewPandoc()
	if err != nil {
		// Check X server availability - if unavailable, just warn and continue
		xServerStatus := s.checkXServerAvailability(context.Background())
		if xServerStatus != "available" {
			s.Logger().Warnf(context.Background(), "failed to init pandoc (X server not available): %v", err)
		} else {
			return nil, errors.Wrapf(err, "failed to init pandoc")
		}
	}

	tempFileManager, err := tempfiles.NewMongoManager(s.Logger(), s.database)
	if err != nil {
		s.Logger().Errorf(s.Logger().WithValue(ctx, "error", err.Error()), "failed to init temp files manager")
		tempFileManager = tempfiles.NewNoopManager()
	}
	s.tempFiles = tempFileManager

	s.registry = sessions.NewRegistry(srv.Logger(), s.database, tempFileManager)

	go s.cleanupActiveSessionsLoop()

	s.browser = NewBrowser(srv.Logger(), os.Getenv("BROWSER_EXECUTABLE"), extensions, pandocSvc, s.inMessages, s.outMessages, s.registry)
	s.pandoc = pandocSvc
	s.browser.SetRequestsDebug(s.IsRequestDebugEnabled())
	if os.Getenv("CHROME_DEBUG") != "" {
		s.browser.SetChromeDebug(true)
	}
	return s, nil
}

func (s *Server) initStorage() storage.Uploader {
	storageServiceAPIKey, err := awsutil.GetEnvOrSecret("STORAGE_SERVICE_API_KEY")
	if err != nil {
		s.Logger().Warnf(s.Logger().WithValue(context.Background(), "error", err.Error()), "failed to get STORAGE_SERVICE_API_KEY")
	}

	storageServiceURL, err := awsutil.GetEnvOrSecret("STORAGE_SERVICE_URL")
	if err != nil {
		s.Logger().Warnf(s.Logger().WithValue(context.Background(), "error", err.Error()), "failed to get STORAGE_SERVICE_URL")
	}

	return storage.NewUploader(storageServiceURL, storageServiceAPIKey, s.Logger())
}

func (s *Server) initPDFParser() (pdf.Parser, error) {
	openAIAPIKey, err := awsutil.GetEnvOrSecret("OPENAI_TOKEN")
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get OPENAI_TOKEN")
	}

	openAIModel := os.Getenv("PDF_PARSER_MODEL")
	if openAIModel == "" {
		openAIModel = "text-embedding-3-large" // default model
	}

	pythonPath := os.Getenv("PYTHON_PATH")
	if pythonPath == "" {
		pythonPath = "python3" // default python
	}

	openAIMinTokens, err := strconv.Atoi(os.Getenv("OPENAI_MIN_TOKENS"))
	if err != nil {
		openAIMinTokens = 64 // default value
	}

	openAIMaxTokens, err := strconv.Atoi(os.Getenv("OPENAI_MAX_TOKENS"))
	if err != nil {
		openAIMaxTokens = 1024 // default value
	}

	parsePDFScriptPath := filepath.Join(os.Getenv("SCRIPTS_DIR"), "mupdf.py")
	if _, err := os.Stat(parsePDFScriptPath); os.IsNotExist(err) {
		s.Logger().Warnf(context.Background(), "PDF parsing script not found at %s: %v", parsePDFScriptPath, err)
	}

	pdfParser := pdf.NewPDFParser(
		s.Logger(),
		pythonPath,
		openAIAPIKey,
		openAIModel,
		openAIMinTokens,
		openAIMaxTokens,
		parsePDFScriptPath,
	)
	return pdfParser, nil
}

func (s *Server) initMarkdownRenderer() (markdown.Renderer, error) {
	renderMarkdownScriptPath := filepath.Join(os.Getenv("SCRIPTS_DIR"), "markdown.mjs")
	if _, err := os.Stat(renderMarkdownScriptPath); os.IsNotExist(err) {
		return nil, errors.Wrapf(err, "markdown rendering script not found at %s", renderMarkdownScriptPath)
	}

	nodePath := os.Getenv("NODEJS_PATH")
	if nodePath == "" {
		nodePath = "node" // default node
	}

	return markdown.NewMarkdownRenderer(s.Logger(), nodePath, renderMarkdownScriptPath), nil
}

func (s *Server) initHtmlTemplater() (html.HtmlTemplater, error) {
	nodePath := os.Getenv("NODEJS_PATH")
	if nodePath == "" {
		nodePath = "node"
	}

	return html.NewHtmlTemplater(s.Logger(), nodePath), nil
}

func (s *Server) initPdfToImagesConverter() (pdf.PdfToImagesConverter, error) {
	return pdf.NewPdfToImagesConverter(s.Logger())
}

// @title BAAS API
// @version 1.0
// @description Browser As A Server REST API service
// @BasePath /
// @securityDefinitions.apikey Bearer
// @in header
// @name Authorization
// @description Enter your API key with "Bearer " prefix (e.g., "Bearer test")
func (s *Server) registerRoutes(router service.HttpAdapterRouter) error {
	docs.SwaggerInfo.Host = os.Getenv("HOST")
	docs.SwaggerInfo.BasePath = ""
	router.GET("/api/status", s.statusEndpoint)
	router.GET("/api/docs", s.docsEndpoint)
	router.POST("/api/readability", s.readabilityEndpoint)
	router.POST("/api/eke-extract", s.eKeExtractEndpoint)
	router.POST("/api/process", s.processEndpoint)
	router.POST("/api/parse", s.parseDocumentEndpoint)
	router.POST("/api/parse-to-markdown-kv", s.parseToMarkdownKvEndpoint)
	router.POST("/api/render-markdown", s.renderMarkdownEndpoint)
	router.POST("/api/extract-markdown", s.extractMarkdown)
	router.POST("/api/pdf-to-images", s.pdfToImagesEndpoint)
	router.POST("/api/async/start", s.asyncStartEndpoint)
	router.POST("/api/async/message", s.asyncMessageEndpoint)
	router.POST("/api/async/stop", s.asyncStopEndpoint)
	router.GET("/api/async/actions", s.asyncActionsDocEndpoint)
	router.GET("/api/async/sessions", s.asyncActiveSessionsEndpoint)
	router.GET("/api/async/sessions/active", s.asyncCheckActive)
	router.POST("/api/async/sessions/cleanup", s.asyncActiveSessionsCleanup)
	return nil
}

func (s *Server) cleanupActiveSessionsLoop() {
	interval := time.NewTicker(time.Minute)
	for range interval.C {
		s.Logger().Infof(context.Background(), "cleaning up expired sessions...")
		if err := s.registry.RemoveExpiredSessions(context.Background()); err != nil {
			s.Logger().Errorf(s.Logger().WithValue(context.Background(), "error", err), "failed to cleanup expired sessions")
		}
	}
}

package router

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"

	"github.com/bbockelm/swamp/internal/agent"
	"github.com/bbockelm/swamp/internal/config"
	"github.com/bbockelm/swamp/internal/crypto"
	"github.com/bbockelm/swamp/internal/db"
	"github.com/bbockelm/swamp/internal/frontend"
	"github.com/bbockelm/swamp/internal/handlers"
	"github.com/bbockelm/swamp/internal/openapi"
	"github.com/bbockelm/swamp/internal/storage"
	"github.com/bbockelm/swamp/internal/ws"
)

// New creates the application HTTP router with all routes.
func New(cfg *config.Config, pool *pgxpool.Pool, store *storage.Store) (*chi.Mux, *handlers.Handler, agent.AnalysisExecutor) {
	r := chi.NewRouter()

	// Middleware
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Compress(5))

	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"http://localhost:3000", "http://localhost:8080"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type"},
		ExposedHeaders:   []string{"Content-Disposition"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	queries := db.NewQueries(pool)

	// Initialize encryption.
	var enc *crypto.Encryptor
	if cfg.InstanceKey != "" {
		var err error
		enc, err = crypto.NewEncryptor(cfg.InstanceKey)
		if err != nil {
			log.Fatal().Err(err).Msg("Failed to initialize encryption")
		}
		log.Info().Msg("Encryption enabled")
	} else {
		log.Warn().Msg("INSTANCE_KEY not set — encryption disabled")
	}

	h := handlers.New(cfg, queries, store, enc)
	hub := ws.NewHub()

	// Override executor mode from DB if persisted.
	if dbMode, err := queries.GetAppConfig(context.Background(), "executor_mode"); err == nil && dbMode != "" {
		log.Info().Str("db_mode", dbMode).Str("env_mode", cfg.ExecutorMode).Msg("Overriding executor mode from DB")
		cfg.ExecutorMode = dbMode
	}
	// Override other executor-related settings from DB (empty DB values keep env defaults).
	applyDBConfig := func(key string, target *string) {
		if v, err := queries.GetAppConfig(context.Background(), key); err == nil && v != "" {
			*target = v
		}
	}
	applyDBConfig("k8s_namespace", &cfg.K8sNamespace)
	applyDBConfig("k8s_worker_image", &cfg.K8sWorkerImage)
	applyDBConfig("k8s_worker_service_account", &cfg.K8sWorkerServiceAccount)
	applyDBConfig("k8s_worker_cpu_request", &cfg.K8sWorkerCPURequest)
	applyDBConfig("k8s_worker_cpu_limit", &cfg.K8sWorkerCPULimit)
	applyDBConfig("k8s_worker_mem_request", &cfg.K8sWorkerMemRequest)
	applyDBConfig("k8s_worker_mem_limit", &cfg.K8sWorkerMemLimit)
	applyDBConfig("k8s_worker_node_selector", &cfg.K8sWorkerNodeSelector)
	applyDBConfig("k8s_worker_tolerations", &cfg.K8sWorkerTolerations)
	applyDBConfig("k8s_worker_labels", &cfg.K8sWorkerLabels)
	applyDBConfig("agent_model", &cfg.AgentModel)

	// Create the appropriate executor based on config.
	var exec agent.AnalysisExecutor
	var tokenStore *agent.WorkerTokenStore

	if cfg.IsKubernetesExecutor() {
		tokenStore = agent.NewWorkerTokenStoreWithDB(queries)
		k8sExec, err := agent.NewK8sExecutor(cfg, queries, store, hub, enc, tokenStore)
		if err != nil {
			log.Fatal().Err(err).Msg("Failed to initialize K8s executor")
		}
		exec = k8sExec
		log.Info().Msg("Using Kubernetes executor")
	} else if cfg.IsProcessExecutor() {
		tokenStore = agent.NewWorkerTokenStoreWithDB(queries)
		procExec, err := agent.NewProcessExecutor(cfg, queries, store, hub, enc, tokenStore)
		if err != nil {
			log.Fatal().Err(err).Msg("Failed to initialize process executor")
		}
		exec = procExec
		log.Info().Msg("Using process executor (detached daemon)")
	} else {
		exec = agent.NewExecutor(cfg, queries, store, hub, enc)
		log.Info().Msg("Using local executor")
	}

	// Health check
	r.Get("/healthz", h.HealthCheck)

	// WebSocket endpoint for live analysis output (unauthenticated WS upgrade,
	// but only receives broadcast data — no sensitive operations).
	r.Get("/ws/analysis/{analysisID}", func(w http.ResponseWriter, r *http.Request) {
		analysisID := chi.URLParam(r, "analysisID")
		hub.HandleConnect(w, r, analysisID)
	})

	// OpenAPI spec
	r.Get("/api/v1/openapi.yaml", openapi.Handler())

	// Swagger UI
	r.Get("/api/v1/docs", openapi.SwaggerUIHandler("/api/v1/openapi.yaml"))

	// API routes
	r.Route("/api/v1", func(r chi.Router) {

		// --- Public auth endpoints ---
		r.Route("/auth", func(r chi.Router) {
			r.Get("/mode", h.GetAuthMode)
			r.Get("/me", h.GetCurrentSession)
			r.Post("/logout", h.Logout)
			r.Get("/dev-login-link/{token}", h.DevLoginLinkComplete)
			r.Get("/oidc/login", h.OIDCLogin)
			r.Get("/oidc/callback", h.OIDCCallback)
		})

		// --- Accept invites and AUP (auth required, no AUP check) ---
		r.Group(func(r chi.Router) {
			r.Use(h.RequireAuth)
			r.Get("/invites/info", h.GetGroupInviteInfo)
			r.Post("/invites/accept", h.AcceptGroupInvite)
			r.Post("/auth/agree-aup", h.AgreeAUP)
			r.Put("/auth/profile", h.UpdateMyProfile)
			r.Get("/auth/my-stats", h.GetMyStats)
		})

		// --- All authenticated + AUP-agreed endpoints ---
		r.Group(func(r chi.Router) {
			r.Use(h.RequireAuth)
			r.Use(h.LoadUserRoles)
			r.Use(h.RequireAUP)

			// User search (available to all authenticated users)
			r.Get("/users/search", h.SearchUsers)

			// Agent status (is analysis agent configured?)
			r.Get("/agent/status", h.AgentStatus)

			// Dashboard stats
			r.Get("/dashboard/stats", h.DashboardStats)

			// All analyses (jobs table)
			r.Get("/analyses", h.ListAllAnalyses)

			// All findings (cross-project)
			r.Get("/findings", h.ListAllFindings)

			// Groups
			r.Route("/groups", func(r chi.Router) {
				r.Get("/", h.ListGroups)
				r.Post("/", h.CreateGroup)
				r.Route("/{groupID}", func(r chi.Router) {
					r.Get("/", h.GetGroup)
					r.Put("/", h.UpdateGroup)
					r.Delete("/", h.DeleteGroup)
					r.Get("/members", h.ListGroupMembers)
					r.Post("/members", h.AddGroupMember)
					r.Put("/members/{userID}", h.UpdateGroupMemberRole)
					r.Delete("/members/{userID}", h.RemoveGroupMember)
					r.Get("/invites", h.ListGroupInvites)
					r.Post("/invites", h.CreateGroupInvite)
					r.Delete("/invites/{inviteID}", h.DeleteGroupInvite)
				})
			})

			// Projects
			r.Route("/projects", func(r chi.Router) {
				r.Get("/", h.ListProjects)
				r.Post("/", h.CreateProject)
				r.Route("/{projectID}", func(r chi.Router) {
					r.Use(h.RequireProjectAccess("read"))
					r.Get("/", h.GetProject)

					// Write-access endpoints
					r.Group(func(r chi.Router) {
						r.Use(h.RequireProjectAccess("write"))

						// Packages
						r.Route("/packages", func(r chi.Router) {
							r.Get("/", h.ListPackages)
							r.Post("/", h.CreatePackage)
							r.Route("/{packageID}", func(r chi.Router) {
								r.Get("/", h.GetPackage)
								r.Put("/", h.UpdatePackage)
								r.Delete("/", h.DeletePackage)
							})
						})

						// Trigger analysis
						r.Post("/analyses", h.CreateAnalysis)
					})

					// Read-access for analyses and results
					r.Get("/analyses", h.ListAnalyses)
					r.Route("/analyses/{analysisID}", func(r chi.Router) {
						r.Get("/", h.GetAnalysis)
						r.Get("/alive", h.CheckAnalysisLiveness)
						r.Post("/cancel", h.CancelAnalysis)
						r.Post("/resubmit", h.ResubmitAnalysis)
						r.Get("/results", h.ListResults)
						r.Route("/results/{resultID}", func(r chi.Router) {
							r.Get("/", h.GetResult)
							r.Get("/download", h.DownloadResultArtifact)
						})
					})

					// Findings (read access for listing, any auth for annotation)
					r.Get("/findings", h.ListProjectFindings)
					r.Route("/findings/{findingID}", func(r chi.Router) {
						r.Get("/", h.GetFinding)
						r.Get("/annotations", h.ListFindingAnnotations)
						r.Post("/annotate", h.AnnotateFinding)
					})

					// Admin-access for project settings
					r.Group(func(r chi.Router) {
						r.Use(h.RequireProjectAccess("admin"))
						r.Put("/", h.UpdateProject)
						r.Delete("/", h.DeleteProject)

						// Provider API keys (admin only)
						r.Route("/provider-keys", func(r chi.Router) {
							r.Get("/", h.ListProjectProviderKeys)
							r.Post("/", h.CreateProjectProviderKey)
							r.Delete("/{keyID}", h.DeleteProjectProviderKey)
							r.Post("/{keyID}/revoke", h.RevokeProjectProviderKey)
						})
					})
				})
			})

			// API Keys (self-service)
			r.Route("/api-keys", func(r chi.Router) {
				r.Get("/", h.ListAPIKeys)
				r.Post("/", h.CreateAPIKey)
				r.Delete("/{keyID}", h.RevokeAPIKey)
			})

			// --- Admin-only endpoints ---
			r.Route("/admin", func(r chi.Router) {
				r.Use(handlers.RequireRole(handlers.RoleAdmin))

				// Users
				r.Route("/users", func(r chi.Router) {
					r.Get("/", h.ListUsers)
					r.Post("/", h.CreateUser)
					r.Route("/{userID}", func(r chi.Router) {
						r.Get("/", h.GetUser)
						r.Put("/", h.UpdateUser)
						r.Delete("/", h.DeleteUser)
						r.Get("/roles", h.ListUserRolesAdmin)
						r.Post("/roles", h.AddUserRole)
						r.Delete("/roles/{role}", h.RemoveUserRole)
						r.Get("/identities", h.ListUserIdentitiesAdmin)
						r.Delete("/identities/{identityID}", h.DeleteUserIdentityAdmin)
						r.Post("/invites", h.CreateUserInviteHandler)
						r.Get("/invites", h.ListUserInvitesHandler)
						r.Delete("/invites/{inviteID}", h.DeleteUserInviteHandler)
					})
				})

				// OIDC configuration
				r.Get("/oidc-config", h.GetOIDCConfig)
				r.Put("/oidc-config", h.UpdateOIDCConfig)

				// AUP management
				r.Get("/aup", h.GetAUPConfig)
				r.Put("/aup", h.UpdateAUPConfig)

				// Executor configuration
				r.Get("/executor-config", h.GetExecutorConfig)
				r.Put("/executor-config", h.UpdateExecutorConfig)

				// Backups
				r.Route("/backups", func(r chi.Router) {
					r.Get("/", h.ListBackups)
					r.Post("/trigger", h.TriggerBackup)
					r.Post("/upload-restore", h.UploadRestore)
					r.Delete("/failed", h.DeleteFailedBackups)
					r.Get("/general-key", h.GetGeneralBackupKey)
					r.Get("/settings", h.GetBackupSettings)
					r.Put("/settings", h.UpdateBackupSettings)
					r.Route("/{backupID}", func(r chi.Router) {
						r.Get("/download", h.DownloadBackup)
						r.Get("/key", h.GetPerBackupKey)
						r.Post("/restore", h.RestoreBackup)
						r.Delete("/", h.DeleteBackup)
					})
				})
			})
		})

		// --- API key + session auth (for programmatic access) ---
		r.Group(func(r chi.Router) {
			r.Use(h.RequireAuthOrAPIKey)
			// Programmatic endpoints go here in the future
		})

		// --- Internal worker endpoints (authenticated via worker session tokens) ---
		if tokenStore != nil {
			wh := handlers.NewWorkerHandler(tokenStore, hub, h)
			r.Route("/internal/worker", func(r chi.Router) {
				r.Post("/exchange", wh.ExchangeToken)
				r.Post("/stream", wh.StreamOutput)
				r.Post("/status", wh.UpdateStatus)
				r.Post("/results", wh.UploadResult)
				// Reverse proxy to Anthropic API — the real API key is injected
				// server-side so it never reaches the worker pod.
				r.HandleFunc("/anthropic/*", wh.ProxyAnthropic)
			})
		}
	})

	// WebSocket endpoint for streaming analysis output
	r.Get("/ws/analysis/{analysisID}", func(w http.ResponseWriter, r *http.Request) {
		analysisID := chi.URLParam(r, "analysisID")
		hub.HandleConnect(w, r, analysisID)
	})

	// Serve embedded frontend (SPA fallback)
	spaHandler := frontend.NewSPAHandler()
	r.NotFound(spaHandler.ServeHTTP)

	return r, h, exec
}

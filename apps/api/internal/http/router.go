package httpapi

import (
	"net/http"
	"strings"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"

	"kkg-agent-eino/apps/api/internal/agent"
	"kkg-agent-eino/apps/api/internal/config"
	"kkg-agent-eino/apps/api/internal/response"
)

type Server struct {
	cfg   config.Config
	agent *agent.Service
}

func NewRouter(cfg config.Config, agentSvc *agent.Service) *gin.Engine {
	if !cfg.IsDev() {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())
	r.Use(cors.New(cors.Config{
		AllowOrigins:     cfg.AllowedOrigins,
		AllowMethods:     []string{"GET", "POST", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization", "X-Request-ID"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
	}))

	s := &Server{cfg: cfg, agent: agentSvc}
	r.GET("/health", s.health)

	v1 := r.Group("/api/v1")
	{
		v1.POST("/agent/run", s.runAgent)
	}
	return r
}

func (s *Server) health(c *gin.Context) {
	response.OK(c, gin.H{
		"service": "kkg-agent-eino",
		"status":  "ok",
		"env":     s.cfg.Env,
	})
}

func (s *Server) runAgent(c *gin.Context) {
	var req agent.RunRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.AccessToken = accessTokenFromRequest(c)
	req.RequestID = c.GetHeader("X-Request-ID")

	out, err := s.agent.Run(c.Request.Context(), req)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.OK(c, out)
}

func accessTokenFromRequest(c *gin.Context) string {
	if token, err := c.Cookie("access_token"); err == nil && strings.TrimSpace(token) != "" {
		return strings.TrimSpace(token)
	}
	authHeader := strings.TrimSpace(c.GetHeader("Authorization"))
	if strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
		return strings.TrimSpace(authHeader[7:])
	}
	return ""
}

func ListenAndServe(cfg config.Config, router http.Handler) error {
	return http.ListenAndServe(":"+cfg.Port, router)
}

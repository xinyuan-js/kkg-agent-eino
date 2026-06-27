package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"

	"kkg-agent-eino/apps/api/internal/agent"
	"kkg-agent-eino/apps/api/internal/config"
	"kkg-agent-eino/apps/api/internal/kkg"
	"kkg-agent-eino/apps/api/internal/memory"
	"kkg-agent-eino/apps/api/internal/response"
)

type Server struct {
	cfg   config.Config
	agent *agent.Service
	kkg   *kkg.Client
}

func NewRouter(cfg config.Config, agentSvc *agent.Service, kkgClient *kkg.Client) *gin.Engine {
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

	s := &Server{cfg: cfg, agent: agentSvc, kkg: kkgClient}
	r.GET("/health", s.health)

	v1 := r.Group("/api/v1")
	{
		auth := v1.Group("/auth")
		{
			auth.POST("/login", s.login)
			auth.GET("/me", s.me)
			auth.POST("/refresh", s.refresh)
			auth.POST("/logout", s.logout)
		}
		v1.GET("/tools", s.tools)
		v1.GET("/agent/sessions", s.listSessions)
		v1.GET("/agent/sessions/:id", s.getSession)
		v1.POST("/agent/sessions/:id/archive", s.archiveSession)
		v1.DELETE("/agent/sessions/:id", s.deleteSession)
		v1.POST("/agent/run", s.runAgent)
		v1.POST("/agent/stream", s.streamAgent)
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
	req, ok := s.bindAgentRunRequest(c)
	if !ok {
		return
	}
	out, err := s.agent.Run(c.Request.Context(), req)
	if err != nil {
		if errors.Is(err, memory.ErrSessionForbidden) {
			response.Unauthorized(c, "session does not belong to current user")
			return
		}
		response.BadRequest(c, err.Error())
		return
	}
	response.OK(c, out)
}

func (s *Server) listSessions(c *gin.Context) {
	user, ok := s.requireCurrentUser(c)
	if !ok {
		return
	}
	var (
		items []agent.ConversationSession
		err   error
	)
	if archived := strings.EqualFold(strings.TrimSpace(c.Query("archived")), "true"); archived {
		items, err = s.agent.ListSessions(c.Request.Context(), user.ID, true)
	} else {
		items, err = s.agent.ListSessions(c.Request.Context(), user.ID, false)
	}
	if err != nil {
		response.ServerError(c, err.Error())
		return
	}
	response.OK(c, items)
}

func (s *Server) getSession(c *gin.Context) {
	user, ok := s.requireCurrentUser(c)
	if !ok {
		return
	}
	item, err := s.agent.LoadSession(c.Request.Context(), user.ID, c.Param("id"))
	if err != nil {
		if errors.Is(err, memory.ErrSessionForbidden) {
			response.Unauthorized(c, "session does not belong to current user")
			return
		}
		response.BadRequest(c, err.Error())
		return
	}
	response.OK(c, item)
}

func (s *Server) archiveSession(c *gin.Context) {
	user, ok := s.requireCurrentUser(c)
	if !ok {
		return
	}
	var req struct {
		Archived bool `json:"archived"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if err := s.agent.ArchiveSession(c.Request.Context(), user.ID, c.Param("id"), req.Archived); err != nil {
		if errors.Is(err, memory.ErrSessionForbidden) {
			response.Unauthorized(c, "session does not belong to current user")
			return
		}
		response.BadRequest(c, err.Error())
		return
	}
	response.OK(c, gin.H{"archived": req.Archived})
}

func (s *Server) deleteSession(c *gin.Context) {
	user, ok := s.requireCurrentUser(c)
	if !ok {
		return
	}
	if err := s.agent.DeleteSession(c.Request.Context(), user.ID, c.Param("id")); err != nil {
		if errors.Is(err, memory.ErrSessionForbidden) {
			response.Unauthorized(c, "session does not belong to current user")
			return
		}
		response.BadRequest(c, err.Error())
		return
	}
	response.OK(c, gin.H{"deleted": true})
}

func (s *Server) streamAgent(c *gin.Context) {
	req, ok := s.bindAgentRunRequest(c)
	if !ok {
		return
	}
	streamCtx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()
	c.Writer.Header().Set("Content-Type", "application/x-ndjson")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		response.ServerError(c, "streaming unsupported")
		return
	}
	writer := bufio.NewWriter(c.Writer)
	var (
		writeMu  sync.Mutex
		writeErr error
		closed   bool
	)
	emit := func(event agent.StreamEvent) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		defer func() {
			if r := recover(); r != nil {
				writeErr = fmt.Errorf("stream writer panic: %v", r)
				cancel()
			}
		}()
		if closed {
			return io.ErrClosedPipe
		}
		if writeErr != nil {
			return writeErr
		}
		raw, err := json.Marshal(event)
		if err != nil {
			writeErr = err
			cancel()
			return err
		}
		if _, err := writer.Write(append(raw, '\n')); err != nil {
			writeErr = err
			cancel()
			return err
		}
		if err := writer.Flush(); err != nil {
			writeErr = err
			cancel()
			return err
		}
		flusher.Flush()
		return nil
	}
	done := make(chan struct{})
	defer func() {
		writeMu.Lock()
		closed = true
		writeMu.Unlock()
		close(done)
	}()
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-streamCtx.Done():
				return
			case <-done:
				return
			case <-ticker.C:
				_ = emit(agent.StreamEvent{Type: "heartbeat", SessionID: req.SessionID})
			}
		}
	}()
	_, err := s.agent.Stream(streamCtx, req, emit)
	if err != nil && writeErr == nil {
		_ = emit(agent.StreamEvent{Type: "error", Message: err.Error(), SessionID: req.SessionID})
	}
}

func (s *Server) bindAgentRunRequest(c *gin.Context) (agent.RunRequest, bool) {
	var req agent.RunRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return agent.RunRequest{}, false
	}
	user, ok := s.requireCurrentUser(c)
	if !ok {
		return agent.RunRequest{}, false
	}
	req.UserID = user.ID
	req.AccessToken = accessTokenFromRequest(c)
	req.RequestID = c.GetHeader("X-Request-ID")
	return req, true
}

func (s *Server) tools(c *gin.Context) {
	infos, err := s.agent.ToolInfos(c.Request.Context())
	if err != nil {
		response.ServerError(c, err.Error())
		return
	}
	response.OK(c, infos)
}

func (s *Server) login(c *gin.Context) {
	var req struct {
		Account  string `json:"account" binding:"required"`
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	session, cookies, err := s.kkg.Login(c.Request.Context(), strings.TrimSpace(req.Account), req.Password)
	if err != nil {
		writeAuthError(c, err)
		return
	}
	forwardAuthCookies(c, cookies)
	response.OK(c, gin.H{"user": session.User})
}

func (s *Server) me(c *gin.Context) {
	accessToken := accessTokenFromRequest(c)
	if accessToken == "" {
		response.Unauthorized(c, "missing access token")
		return
	}
	user, err := s.kkg.Me(c.Request.Context(), accessToken)
	if err != nil {
		writeAuthError(c, err)
		return
	}
	response.OK(c, user)
}

func (s *Server) refresh(c *gin.Context) {
	refreshToken, err := c.Cookie("refresh_token")
	if err != nil || strings.TrimSpace(refreshToken) == "" {
		response.Unauthorized(c, "missing refresh token")
		return
	}
	session, cookies, err := s.kkg.Refresh(c.Request.Context(), strings.TrimSpace(refreshToken))
	if err != nil {
		writeAuthError(c, err)
		return
	}
	forwardAuthCookies(c, cookies)
	response.OK(c, gin.H{"user": session.User})
}

func (s *Server) logout(c *gin.Context) {
	clearAuthCookie(c, "access_token")
	clearAuthCookie(c, "refresh_token")
	response.OK(c, gin.H{"logged_out": true})
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

func forwardAuthCookies(c *gin.Context, cookies []*http.Cookie) {
	for _, cookie := range cookies {
		if cookie.Name != "access_token" && cookie.Name != "refresh_token" {
			continue
		}
		out := *cookie
		out.Domain = ""
		out.Path = "/"
		http.SetCookie(c.Writer, &out)
	}
}

func clearAuthCookie(c *gin.Context, name string) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func writeAuthError(c *gin.Context, err error) {
	var apiErr kkg.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusUnauthorized {
		response.Unauthorized(c, apiErr.Message)
		return
	}
	response.BadRequest(c, err.Error())
}

func (s *Server) requireCurrentUser(c *gin.Context) (*kkg.AuthUser, bool) {
	accessToken := accessTokenFromRequest(c)
	if accessToken == "" {
		response.Unauthorized(c, "login required")
		return nil, false
	}
	user, err := s.kkg.Me(c.Request.Context(), accessToken)
	if err != nil {
		writeAuthError(c, err)
		return nil, false
	}
	return user, true
}

func ListenAndServe(cfg config.Config, router http.Handler) error {
	return http.ListenAndServe(":"+cfg.Port, router)
}

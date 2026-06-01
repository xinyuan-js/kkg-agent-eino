package config

import (
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Env             string
	Port            string
	AllowedOrigins  []string
	JWTSecret       string
	KKGBlogBaseURL  string
	KKGOJBaseURL    string
	PostgresDSN     string
	RedisAddr       string
	MinIOEndpoint   string
	DeepSeekBaseURL string
	DeepSeekModel   string
	DeepSeekAPIKey  string

	DashScopeBaseURL        string
	DashScopeAPIKey         string
	DashScopeEmbeddingModel string
	DashScopeEmbeddingDim   int

	RAGQuestionIndexMaxPages int
	RAGQuestionIndexPageSize int
}

func Load() Config {
	return Config{
		Env:             getEnv("APP_ENV", "dev"),
		Port:            getEnv("AGENT_API_PORT", "8088"),
		AllowedOrigins:  splitCSV(getEnv("AGENT_ALLOWED_ORIGINS", "http://localhost:3000,http://127.0.0.1:3000")),
		JWTSecret:       getEnv("AGENT_JWT_SECRET", "change-me"),
		KKGBlogBaseURL:  trimRightSlash(getEnv("KKG_BLOG_BASE_URL", "http://127.0.0.1/blog-api")),
		KKGOJBaseURL:    trimRightSlash(getEnv("KKG_OJ_BASE_URL", "http://127.0.0.1/oj-api")),
		PostgresDSN:     getEnv("POSTGRES_DSN", "postgres://kkg_agent:kkg_agent@127.0.0.1:5432/kkg_agent?sslmode=disable"),
		RedisAddr:       getEnv("REDIS_ADDR", "127.0.0.1:6379"),
		MinIOEndpoint:   getEnv("MINIO_ENDPOINT", "127.0.0.1:9000"),
		DeepSeekBaseURL: trimRightSlash(getEnv("DEEPSEEK_BASE_URL", "https://api.deepseek.com")),
		DeepSeekModel:   normalizeModelName(getEnv("DEEPSEEK_MODEL", "")),
		DeepSeekAPIKey:  getEnv("DEEPSEEK_API_KEY", ""),

		DashScopeBaseURL:        trimRightSlash(getEnv("DASHSCOPE_BASE_URL", "https://dashscope.aliyuncs.com/compatible-mode/v1")),
		DashScopeAPIKey:         getEnv("DASHSCOPE_API_KEY", ""),
		DashScopeEmbeddingModel: getEnv("DASHSCOPE_EMBEDDING_MODEL", "text-embedding-v4"),
		DashScopeEmbeddingDim:   IntEnv("DASHSCOPE_EMBEDDING_DIM", 1024),

		RAGQuestionIndexMaxPages: IntEnv("RAG_QUESTION_INDEX_MAX_PAGES", 3),
		RAGQuestionIndexPageSize: IntEnv("RAG_QUESTION_INDEX_PAGE_SIZE", 50),
	}
}

func (c Config) IsDev() bool {
	return c.Env == "dev" || c.Env == "local"
}

func getEnv(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func trimRightSlash(value string) string {
	return strings.TrimRight(strings.TrimSpace(value), "/")
}

func normalizeModelName(value string) string {
	value = strings.TrimSpace(value)
	if value == "deepseekv4pro" {
		return "deepseek-v4-pro"
	}
	return value
}

func IntEnv(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

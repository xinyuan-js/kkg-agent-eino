package main

import (
	"context"
	"log"
	"time"

	"kkg-agent-eino/apps/api/internal/agent"
	"kkg-agent-eino/apps/api/internal/config"
	"kkg-agent-eino/apps/api/internal/dashscope"
	"kkg-agent-eino/apps/api/internal/deepseek"
	httpapi "kkg-agent-eino/apps/api/internal/http"
	"kkg-agent-eino/apps/api/internal/kkg"
	"kkg-agent-eino/apps/api/internal/memory"
	"kkg-agent-eino/apps/api/internal/rag"
)

func main() {
	cfg := config.Load()

	kkgClient := kkg.NewClient(cfg.KKGBlogBaseURL, cfg.KKGOJBaseURL)
	var chatModel *deepseek.ChatModel
	if cfg.DeepSeekAPIKey != "" {
		var err error
		chatModel, err = deepseek.NewChatModel(deepseek.Config{
			BaseURL: cfg.DeepSeekBaseURL,
			APIKey:  cfg.DeepSeekAPIKey,
			Model:   cfg.DeepSeekModel,
		})
		if err != nil {
			log.Fatalf("build deepseek chat model: %v", err)
		}
		log.Printf("deepseek chat model enabled: %s", cfg.DeepSeekModel)
	} else {
		log.Printf("deepseek chat model disabled: DEEPSEEK_API_KEY is empty")
	}

	memoryStore, err := memory.Open(context.Background(), cfg.PostgresDSN)
	if err != nil {
		log.Fatalf("open memory store: %v", err)
	}

	retriever := buildRetriever(cfg, kkgClient)
	agentSvc, err := agent.NewService(retriever, kkgClient, chatModel, memoryStore)
	if err != nil {
		log.Fatalf("build agent service: %v", err)
	}

	router := httpapi.NewRouter(cfg, agentSvc, kkgClient)
	log.Printf("kkg-agent-eino api listening on :%s", cfg.Port)
	if err := httpapi.ListenAndServe(cfg, router); err != nil {
		log.Fatal(err)
	}
}

func buildRetriever(cfg config.Config, kkgClient *kkg.Client) rag.Retriever {
	if cfg.DashScopeAPIKey == "" {
		log.Printf("semantic rag disabled: DASHSCOPE_API_KEY is empty")
		return rag.NoopRetriever{}
	}
	embedder, err := dashscope.NewEmbedder(dashscope.EmbeddingConfig{
		BaseURL:    cfg.DashScopeBaseURL,
		APIKey:     cfg.DashScopeAPIKey,
		Model:      cfg.DashScopeEmbeddingModel,
		Dimensions: cfg.DashScopeEmbeddingDim,
	})
	if err != nil {
		log.Printf("semantic rag disabled: build dashscope embedder: %v", err)
		return rag.NoopRetriever{}
	}
	vectorStore, err := rag.OpenPGVectorStore(context.Background(), cfg.PostgresDSN, cfg.DashScopeEmbeddingDim)
	if err != nil {
		log.Printf("pgvector store unavailable, falling back to in-memory vector store: %v", err)
		vectorStore = nil
	}
	if vectorStore == nil {
		retriever, err := rag.NewSemanticRetriever(embedder, rag.NewInMemoryVectorStore())
		if err != nil {
			log.Printf("semantic rag disabled: build semantic retriever: %v", err)
			return rag.NoopRetriever{}
		}
		log.Printf("semantic rag enabled: %s dim=%d store=memory", cfg.DashScopeEmbeddingModel, cfg.DashScopeEmbeddingDim)
		startQuestionIndex(cfg, kkgClient, retriever)
		return retriever
	}

	retriever, err := rag.NewSemanticRetriever(embedder, vectorStore)
	if err != nil {
		log.Printf("semantic rag disabled: build semantic retriever: %v", err)
		return rag.NoopRetriever{}
	}
	log.Printf("semantic rag enabled: %s dim=%d store=pgvector", cfg.DashScopeEmbeddingModel, cfg.DashScopeEmbeddingDim)
	startQuestionIndex(cfg, kkgClient, retriever)
	return retriever
}

func startQuestionIndex(cfg config.Config, kkgClient *kkg.Client, retriever *rag.SemanticRetriever) {
	go indexQuestions(cfg, kkgClient, retriever)
}

func indexQuestions(cfg config.Config, kkgClient *kkg.Client, retriever *rag.SemanticRetriever) {
	if cfg.RAGQuestionIndexMaxPages <= 0 {
		log.Printf("question rag index disabled: RAG_QUESTION_INDEX_MAX_PAGES <= 0")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	stats, err := rag.IndexKKGQuestions(ctx, kkgClient, retriever, rag.QuestionIndexConfig{
		MaxPages: int64(cfg.RAGQuestionIndexMaxPages),
		PageSize: int64(cfg.RAGQuestionIndexPageSize),
	})
	if err != nil {
		log.Printf("question rag index failed: %v", err)
		return
	}
	log.Printf("question rag index completed: indexed=%d skipped=%d", stats.Indexed, stats.Skipped)
}

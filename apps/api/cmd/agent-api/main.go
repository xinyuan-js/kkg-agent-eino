package main

import (
	"context"
	"log"

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

	retriever := buildRetriever(cfg)
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

func buildRetriever(cfg config.Config) rag.Retriever {
	if cfg.DashScopeAPIKey == "" {
		log.Printf("semantic rag disabled: DASHSCOPE_API_KEY is empty")
		return rag.StaticRetriever{}
	}
	embedder, err := dashscope.NewEmbedder(dashscope.EmbeddingConfig{
		BaseURL:    cfg.DashScopeBaseURL,
		APIKey:     cfg.DashScopeAPIKey,
		Model:      cfg.DashScopeEmbeddingModel,
		Dimensions: cfg.DashScopeEmbeddingDim,
	})
	if err != nil {
		log.Printf("semantic rag disabled: build dashscope embedder: %v", err)
		return rag.StaticRetriever{}
	}
	retriever, err := rag.NewSemanticRetriever(embedder, rag.NewInMemoryVectorStore())
	if err != nil {
		log.Printf("semantic rag disabled: build semantic retriever: %v", err)
		return rag.StaticRetriever{}
	}
	log.Printf("semantic rag enabled: %s dim=%d", cfg.DashScopeEmbeddingModel, cfg.DashScopeEmbeddingDim)
	return retriever
}

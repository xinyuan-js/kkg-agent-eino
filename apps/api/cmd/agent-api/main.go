package main

import (
	"log"

	"kkg-agent-eino/apps/api/internal/agent"
	"kkg-agent-eino/apps/api/internal/config"
	httpapi "kkg-agent-eino/apps/api/internal/http"
	"kkg-agent-eino/apps/api/internal/kkg"
	"kkg-agent-eino/apps/api/internal/rag"
)

func main() {
	cfg := config.Load()

	kkgClient := kkg.NewClient(cfg.KKGBlogBaseURL, cfg.KKGOJBaseURL)
	agentSvc, err := agent.NewService(rag.StaticRetriever{}, kkgClient)
	if err != nil {
		log.Fatalf("build agent service: %v", err)
	}

	router := httpapi.NewRouter(cfg, agentSvc)
	log.Printf("kkg-agent-eino api listening on :%s", cfg.Port)
	if err := httpapi.ListenAndServe(cfg, router); err != nil {
		log.Fatal(err)
	}
}

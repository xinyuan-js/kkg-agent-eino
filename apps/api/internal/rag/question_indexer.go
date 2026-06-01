package rag

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
	"time"

	"kkg-agent-eino/apps/api/internal/kkg"
)

type QuestionIndexConfig struct {
	MaxPages    int64
	PageSize    int64
	AccessToken string
}

type QuestionIndexStats struct {
	Indexed int
	Skipped int
}

func IndexKKGQuestions(ctx context.Context, client *kkg.Client, retriever *SemanticRetriever, cfg QuestionIndexConfig) (QuestionIndexStats, error) {
	if client == nil {
		return QuestionIndexStats{}, fmt.Errorf("kkg client is required")
	}
	if retriever == nil {
		return QuestionIndexStats{}, fmt.Errorf("semantic retriever is required")
	}
	if cfg.MaxPages <= 0 {
		cfg.MaxPages = 3
	}
	if cfg.PageSize <= 0 || cfg.PageSize > 20 {
		cfg.PageSize = 20
	}

	var stats QuestionIndexStats
	for current := int64(1); current <= cfg.MaxPages; current++ {
		page, err := client.ListQuestions(ctx, kkg.ToolContext{AccessToken: cfg.AccessToken}, kkg.PageRequest{
			Current:  current,
			PageSize: cfg.PageSize,
		})
		if err != nil {
			return stats, err
		}
		records := recordsFromPage(page)
		if len(records) == 0 {
			break
		}

		docs := make([]Document, 0, len(records))
		for _, record := range records {
			doc, ok := questionRecordDocument(ctx, client, cfg.AccessToken, record)
			if !ok {
				stats.Skipped++
				continue
			}
			docs = append(docs, doc)
		}
		if err := retriever.IndexDocuments(ctx, docs...); err != nil {
			return stats, err
		}
		stats.Indexed += len(docs)

		if page == nil || page.Size <= 0 || current*page.Size >= page.Total {
			break
		}
	}
	return stats, nil
}

func questionRecordDocument(ctx context.Context, client *kkg.Client, accessToken string, record map[string]any) (Document, bool) {
	id := intValue(record, "id", "questionId", "question_id")
	if id <= 0 {
		return Document{}, false
	}

	question, err := client.GetQuestion(ctx, kkg.ToolContext{AccessToken: accessToken}, id)
	if err != nil || question == nil {
		question = questionFromRecord(record, id)
	}
	if question == nil || strings.TrimSpace(question.Title) == "" {
		return Document{}, false
	}

	metadata := map[string]string{
		"type":        "question",
		"question_id": strconv.FormatInt(id, 10),
		"source":      "kkg-oj",
	}
	if difficulty := stringValue(record, "difficulty", "difficultyName", "level", "levelName"); difficulty != "" {
		metadata["difficulty"] = difficulty
	}
	if len(question.Tags) > 0 {
		metadata["tags"] = strings.Join(question.Tags, ",")
	}

	return Document{
		ID:        fmt.Sprintf("question:%d", id),
		Source:    "kkg-oj-question",
		Title:     question.Title,
		Content:   questionProfileText(question, metadata),
		Metadata:  metadata,
		UpdatedAt: time.Now(),
	}, true
}

func questionFromRecord(record map[string]any, id int64) *kkg.Question {
	return &kkg.Question{
		ID:          id,
		Title:       stringValue(record, "title", "name"),
		Content:     stringValue(record, "content", "description", "questionContent"),
		Tags:        stringSliceValue(record, "tags", "tagList"),
		SampleCase:  recordValue(record, "sampleCase", "sample_case"),
		JudgeConfig: recordValue(record, "judgeConfig", "judge_config"),
	}
}

func questionProfileText(question *kkg.Question, metadata map[string]string) string {
	var parts []string
	parts = append(parts, "题目："+strings.TrimSpace(question.Title))
	if difficulty := strings.TrimSpace(metadata["difficulty"]); difficulty != "" {
		parts = append(parts, "难度："+difficulty)
	}
	if len(question.Tags) > 0 {
		parts = append(parts, "标签："+strings.Join(question.Tags, "、"))
		parts = append(parts, "适合练习："+strings.Join(question.Tags, "、"))
	}
	if content := compactQuestionText(question.Content, 900); content != "" {
		parts = append(parts, "题面摘要："+content)
	}
	if sample := compactAny(question.SampleCase, 260); sample != "" {
		parts = append(parts, "样例摘要："+sample)
	}
	if judge := compactAny(question.JudgeConfig, 260); judge != "" {
		parts = append(parts, "判题约束："+judge)
	}
	return strings.Join(parts, "\n")
}

func recordsFromPage(page *kkg.PageResult) []map[string]any {
	if page == nil || page.Records == nil {
		return nil
	}
	items, ok := page.Records.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		record, ok := item.(map[string]any)
		if ok {
			out = append(out, record)
		}
	}
	return out
}

func intValue(record map[string]any, keys ...string) int64 {
	for _, key := range keys {
		value, ok := record[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case int64:
			return typed
		case int:
			return int64(typed)
		case float64:
			return int64(typed)
		case json.Number:
			parsed, _ := typed.Int64()
			return parsed
		case string:
			parsed, _ := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
			return parsed
		}
	}
	return 0
}

func stringValue(record map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := record[key]
		if !ok || value == nil {
			continue
		}
		if text, ok := value.(string); ok {
			return strings.TrimSpace(text)
		}
	}
	return ""
}

func stringSliceValue(record map[string]any, keys ...string) []string {
	for _, key := range keys {
		value, ok := record[key]
		if !ok || value == nil {
			continue
		}
		switch typed := value.(type) {
		case []string:
			return typed
		case []any:
			out := make([]string, 0, len(typed))
			for _, item := range typed {
				text, ok := item.(string)
				if ok && strings.TrimSpace(text) != "" {
					out = append(out, strings.TrimSpace(text))
				}
			}
			return out
		case string:
			return splitTags(typed)
		}
	}
	return nil
}

func recordValue(record map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := record[key]; ok {
			return value
		}
	}
	return nil
}

func splitTags(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == '，' || r == ';' || r == '；' || r == ' '
	})
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field != "" {
			out = append(out, field)
		}
	}
	return out
}

var htmlTagPattern = regexp.MustCompile(`<[^>]+>`)

func compactQuestionText(value string, limit int) string {
	value = html.UnescapeString(value)
	value = htmlTagPattern.ReplaceAllString(value, " ")
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if limit <= 0 || len([]rune(value)) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit]) + "..."
}

func compactAny(value any, limit int) string {
	if value == nil {
		return ""
	}
	var text string
	switch typed := value.(type) {
	case string:
		text = typed
	default:
		raw, err := json.Marshal(typed)
		if err != nil {
			return ""
		}
		text = string(raw)
	}
	return compactQuestionText(text, limit)
}

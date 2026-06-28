package agent

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"github.com/cloudwego/eino/schema"
)

var questionIDPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(?:题目|题号|oj)\s*#?\s*(\d{1,9})`),
	regexp.MustCompile(`(?i)(?:题目|题号|oj)\s*(?:id|编号)\s*[:：#]?\s*(\d{1,9})`),
	regexp.MustCompile(`(?i)(\d{1,9})\s*(?:题|题目)`),
}

var submissionIDPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(?:submission[_\s-]*id|submit[_\s-]*id)\s*[:=]?\s*(\d{1,9})`),
	regexp.MustCompile(`(?i)(?:提交记录|提交|判题记录)\s*(?:id|编号|#)?\s*[:：]?\s*(\d{1,9})`),
	regexp.MustCompile(`(?i)(\d{1,9})\s*(?:号)?提交(?:记录)?`),
}

var (
	looseNumberPattern = regexp.MustCompile(`\d{1,9}`)
	kkgCodePattern     = regexp.MustCompile(`(?is)<kkg-code\b[^>]*>\s*(.*?)\s*</kkg-code>`)
	fencedCodePattern  = regexp.MustCompile("(?is)```(?:[a-zA-Z0-9_+.#-]+)?\\s*\\n(.*?)\\n```")
)

func extractSubmissionID(query string) int64 {
	query = strings.TrimSpace(query)
	if query == "" {
		return 0
	}
	for _, pattern := range submissionIDPatterns {
		match := pattern.FindStringSubmatch(query)
		if len(match) < 2 {
			continue
		}
		id, err := strconv.ParseInt(match[1], 10, 64)
		if err == nil && id > 0 {
			return id
		}
	}
	return 0
}

func extractQuestionID(query string) int64 {
	query = strings.TrimSpace(query)
	if query == "" {
		return 0
	}
	for _, pattern := range questionIDPatterns {
		match := pattern.FindStringSubmatch(query)
		if len(match) < 2 {
			continue
		}
		id, err := strconv.ParseInt(match[1], 10, 64)
		if err == nil && id > 0 {
			return id
		}
	}
	if containsAny(strings.ToLower(query), "题面", "题目详情", "题目内容", "查看题目", "检索题面", "题目信息") {
		raw := looseNumberPattern.FindString(query)
		id, err := strconv.ParseInt(raw, 10, 64)
		if err == nil && id > 0 {
			return id
		}
	}
	return 0
}

func resolveContextReferences(state workState) (workState, bool) {
	if !shouldInheritContext(state) || len(state.History) == 0 {
		return state, false
	}
	inherited := false
	if state.Request.QuestionID <= 0 {
		if id := lastQuestionIDFromHistory(state.History); id > 0 {
			state.Request.QuestionID = id
			inherited = true
		}
	}
	if state.Request.SubmissionID <= 0 {
		if id := lastSubmissionIDFromHistory(state.History); id > 0 {
			state.Request.SubmissionID = id
			inherited = true
		}
	}
	if strings.TrimSpace(state.Request.Code) == "" && shouldInheritCode(state.Query) {
		if code := lastCodeFromHistory(state.History); strings.TrimSpace(code) != "" {
			state.Request.Code = code
			inherited = true
		}
	}
	return state, inherited
}

func referencesPreviousContext(query string) bool {
	return containsAny(strings.ToLower(query), "上面", "上边", "刚才", "之前", "上一", "前面", "这段代码", "这个代码", "上述代码")
}

func shouldInheritContext(state workState) bool {
	query := strings.ToLower(state.Query)
	if referencesPreviousContext(query) {
		return true
	}
	if containsAny(query, "推荐", "找题", "相似题", "专项", "练习", "知识点", "题单") {
		return false
	}
	if state.Request.QuestionID > 0 && state.Request.SubmissionID > 0 && strings.TrimSpace(state.Request.Code) != "" {
		return false
	}
	return containsAny(query,
		"提交",
		"submit",
		"交一下",
		"运行",
		"run",
		"执行",
		"测试",
		"判题",
		"结果",
		"是否通过",
		"通过了吗",
		"ac",
		"代码",
		"这个",
		"这题",
		"该题",
		"继续",
		"再",
	)
}

func shouldInheritCode(query string) bool {
	query = strings.ToLower(query)
	if referencesPreviousContext(query) {
		return true
	}
	return containsAny(query,
		"提交",
		"submit",
		"交一下",
		"运行",
		"run",
		"执行",
		"测试",
		"代码",
		"可以交",
	)
}

func lastQuestionIDFromHistory(history []*schema.Message) int64 {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i] == nil {
			continue
		}
		if id := questionIDFromMessage(history[i]); id > 0 {
			return id
		}
	}
	return 0
}

func lastSubmissionIDFromHistory(history []*schema.Message) int64 {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i] == nil {
			continue
		}
		if id := submissionIDFromMessage(history[i]); id > 0 {
			return id
		}
	}
	return 0
}

func lastCodeFromHistory(history []*schema.Message) string {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i] == nil {
			continue
		}
		if code := codeFromMessage(history[i]); strings.TrimSpace(code) != "" {
			return code
		}
	}
	return ""
}

func questionIDFromMessage(msg *schema.Message) int64 {
	if id := int64FieldFromMessage(msg, "question_id"); id > 0 {
		return id
	}
	return extractQuestionID(extractDisplayContent(msg))
}

func submissionIDFromMessage(msg *schema.Message) int64 {
	if id := int64FieldFromMessage(msg, "submission_id"); id > 0 {
		return id
	}
	return extractSubmissionID(extractDisplayContent(msg))
}

func codeFromMessage(msg *schema.Message) string {
	if code := stringFieldFromMessage(msg, "code"); strings.TrimSpace(code) != "" {
		return strings.TrimSpace(code)
	}
	return extractCodeBlock(extractDisplayContent(msg))
}

func int64FieldFromMessage(msg *schema.Message, key string) int64 {
	payload := jsonPayloadFromMessage(msg)
	if payload == nil {
		return 0
	}
	return valueAsInt64(payload[key])
}

func stringFieldFromMessage(msg *schema.Message, key string) string {
	payload := jsonPayloadFromMessage(msg)
	if payload == nil {
		return ""
	}
	value, ok := payload[key].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

func jsonPayloadFromMessage(msg *schema.Message) map[string]any {
	if msg == nil || strings.TrimSpace(msg.Content) == "" {
		return nil
	}
	var payload map[string]any
	decoder := json.NewDecoder(strings.NewReader(msg.Content))
	decoder.UseNumber()
	if decoder.Decode(&payload) != nil {
		return nil
	}
	return payload
}

func extractCodeBlock(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if match := kkgCodePattern.FindStringSubmatch(text); len(match) > 1 {
		return strings.TrimSpace(match[1])
	}
	if match := fencedCodePattern.FindStringSubmatch(text); len(match) > 1 {
		return strings.TrimSpace(match[1])
	}
	return ""
}

func rebuildRuntimeMessages(state *workState) {
	if state == nil {
		return
	}
	messages := make([]*schema.Message, 0, len(state.History)+1)
	messages = append(messages, state.History...)
	messages = append(messages, schema.UserMessage(agentUserPrompt(*state)))
	state.Messages = messages
}

func replaceLastAssistantMessage(messages []*schema.Message, content string) {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i] != nil && messages[i].Role == schema.Assistant {
			messages[i].Content = content
			return
		}
	}
}

func normalizeKKGCodeProtocol(answer string) string {
	out := strings.TrimSpace(answer)
	replacements := [][2]string{
		{"```go\n<kkg-code", "<kkg-code"},
		{"```golang\n<kkg-code", "<kkg-code"},
		{"```cpp\n<kkg-code", "<kkg-code"},
		{"```c++\n<kkg-code", "<kkg-code"},
		{"```java\n<kkg-code", "<kkg-code"},
		{"```python\n<kkg-code", "<kkg-code"},
		{"```py\n<kkg-code", "<kkg-code"},
		{"```\n<kkg-code", "<kkg-code"},
		{"</kkg-code>\n```", "</kkg-code>"},
	}
	for _, replacement := range replacements {
		out = strings.ReplaceAll(out, replacement[0], replacement[1])
	}
	out = strings.ReplaceAll(out, "```go<kkg-code", "<kkg-code")
	out = strings.ReplaceAll(out, "```<kkg-code", "<kkg-code")
	out = strings.ReplaceAll(out, "</kkg-code>```", "</kkg-code>")
	return strings.TrimSpace(out)
}

func submissionIDStatus(submissionID int64) string {
	if submissionID > 0 {
		return "known"
	}
	return "missing"
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func containsAny(text string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(text, strings.ToLower(candidate)) {
			return true
		}
	}
	return false
}

func compactText(value string, limit int) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if limit <= 0 || len([]rune(value)) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit]) + "..."
}

func countCodeLines(code string) int {
	code = strings.TrimSpace(code)
	if code == "" {
		return 0
	}
	return strings.Count(code, "\n") + 1
}

func approvalSafeMessages(messages []*schema.Message, finalAnswer string) []*schema.Message {
	out := make([]*schema.Message, 0, 2)
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		if msg.Role == schema.User {
			out = append(out, msg)
		}
	}
	answer := strings.TrimSpace(finalAnswer)
	if answer != "" {
		out = append(out, schema.AssistantMessage(answer, nil))
	}
	return out
}

func isSubmitConfirmation(query string) bool {
	compact := strings.ToLower(strings.TrimSpace(query))
	compact = strings.NewReplacer(" ", "", "\n", "", "\t", "", "，", ",", "。", ".", "！", "!", "？", "?").Replace(compact)
	if compact == "" {
		return false
	}
	return containsAny(compact,
		"确认提交",
		"同意提交",
		"可以提交",
		"提交吧",
		"确认submit",
		"confirm_submit",
		"yes_submit",
	)
}

func isSubmitConfirmationReply(query string, history []*schema.Message) bool {
	compact := strings.ToLower(strings.TrimSpace(query))
	compact = strings.NewReplacer(" ", "", "\n", "", "\t", "", "，", ",", "。", ".", "！", "!", "？", "?").Replace(compact)
	if !containsAny(compact, "确认", "可以", "是的", "对", "同意", "yes", "ok") {
		return false
	}
	for i := len(history) - 1; i >= 0; i-- {
		if history[i] == nil || history[i].Role != schema.Assistant {
			continue
		}
		content := strings.ToLower(extractDisplayContent(history[i]))
		return containsAny(content, "确认提交", "是否确认提交", "确认后", "回复“确认提交”", "回复\"确认提交\"")
	}
	return false
}

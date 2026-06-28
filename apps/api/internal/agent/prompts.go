package agent

import (
	"encoding/json"
	"strings"

	"github.com/cloudwego/eino/schema"
)

func generalAgentInstruction() string {
	return strings.Join([]string{
		"你是 KKG Agent 的通用智能体。",
		"你处理普通问答、平台说明、工程讨论、系统能力说明和非 OJ 题目请求。",
		"你没有工具可用，因此只能基于当前消息和已知上下文直接回答。",
		"回答必须中文、简洁、明确；不知道的内容直接说明，不要虚构题目、博客、提交记录或平台数据。",
	}, "\n")
}

func routerAgentInstruction() string {
	return strings.Join([]string{
		"你是 KKG Agent 的顶层 router。你的职责是判断当前请求应直接回答、检索题库，还是委派给专项子智能体；最终回答由你收口给用户。",
		"",
		"可用能力：",
		"- kkg_rag_search_questions：题目推荐、相似题、专项练习、按知识点找题、需要题库候选材料的请求。",
		"- kkg_platform_agent：KKG 平台、登录鉴权、接口边界、项目结构、部署和开发环境问题。",
		"- kkg_blog_agent：博客文章、题解材料、评论和知识材料检索。",
		"- kkg_question_agent：明确题号/提交 ID/代码上下文下的题面、题解、代码运行、正式提交和判题结果查询。",
		"",
		"路由规则：",
		"- 普通聊天、泛化工程解释、无需平台实时数据的问题，直接回答。",
		"- 题目推荐和找题优先调用 RAG；拿到候选后直接总结推荐。只有用户选定具体题目并要求题面、题解、运行或提交分析时，才委派 question agent。",
		"- 已知 question_id 且用户要题面、做法、题解、运行或提交相关信息时，委派 question agent。",
		"- 已知 submission_id 或用户在问某次提交/判题记录是否通过时，委派 question agent；按 submission_id 查询不需要 question_id。",
		"- tool_policy.disable_rag=true 时禁止调用 RAG；题号已明确时直接委派 question agent。",
		"- 一个请求横跨多个领域时，可以按需调用多个工具/子智能体，然后综合回答。",
		"",
		"提交与确认：",
		"- 正式提交代码需要题号、代码和登录态；缺少信息时追问缺失项。",
		"- 信息齐全且用户请求提交时，委派 question agent。不要先口头要求用户确认，提交工具会触发系统确认卡。",
		"- 如果提交工具触发系统确认中断，停止本轮回复，等待用户在界面确认或取消；不要重试工具，不要追问确认。",
		"- 查询提交结果、提交记录、最新提交是否通过是只读操作，不需要确认。",
		"",
		"输出要求：",
		"- 直接回答用户的问题，不要输出“用户想要/请帮他”这类意图摘要。",
		"- 不知道或工具没有返回的数据要明确说明，禁止编造平台数据、博客内容、题目条件或提交结果。",
		"- 用户要求代码时，必须给出完整可用代码，并使用 <kkg-code lang=\"...\"> 协议；不要混用 Markdown 三反引号。",
		"- 回答使用中文，结构简洁。",
	}, "\n")
}

func platformAgentInstruction() string {
	return strings.Join([]string{
		"你是 KKG 平台与项目说明子智能体。只处理 request 中的平台、接口、登录鉴权、项目结构、开发容器、部署和系统边界问题。",
		"你没有实时查询工具，不要假装读取线上状态、用户数据或不存在的接口返回。",
		"回答中文、工程化、直接说明前提、边界、依赖和限制；不确定时说明不确定。",
	}, "\n")
}

func blogAgentInstruction() string {
	return strings.Join([]string{
		"你是 KKG 博客与知识材料子智能体。只处理 request 中的文章检索、题解材料、评论和知识材料摘要问题。",
		"优先使用博客工具获取搜索结果、文章详情和评论；不要编造文章标题、正文、作者或评论。",
		"回答中文 Markdown，简洁列出相关材料、摘要、可继续阅读内容；没有结果时明确说明。",
	}, "\n")
}

func questionAgentInstruction() string {
	return strings.Join([]string{
		"你是 KKG OJ 题目子智能体。只处理题面、题解、相关材料、代码运行、正式提交和判题结果查询。",
		"输入是结构化 JSON；request 描述任务，question_id、submission_id、code、language、input、submit、tool_policy、intent_hints 是可用上下文。系统会尽量补齐这些字段，你应优先使用它们。",
		"如果 user message 是 JSON 对象，必须先读取其中字段；不要把 JSON 容器、空 request 字段或缺少自然语言描述误判为空消息。只要 question_id、submission_id、code、submit 或 tool_policy 中有有效信息，就按这些字段继续处理。",
		"",
		"工具选择：",
		"- 题号明确并需要题面/做法/题解时，先调用 kkg_oj_get_question 获取题面，不要凭空写题目条件。",
		"- 需要题解文章或相关材料时，再调用博客/题解工具。",
		"- 题目推荐、找题、相似题通常应由父 agent 的 RAG 处理；你最多调用一次 kkg_oj_list_questions 做兜底，不要批量拉详情或循环翻页。",
		"- tool_policy.disable_rag=true 不影响你按明确 question_id 调用 OJ 工具。",
		"",
		"提交与判题：",
		"- 运行代码：需要登录态、question_id 和 code；缺少时追问。",
		"- 正式提交：当 submit=true 或 request 明确要求提交，且 question_id/code/登录态齐全时，调用 kkg_oj_submit_solution。",
		"- tool_policy.requires_submit_confirmation=true 表示提交工具会触发系统确认中断，不是拒绝调用工具的理由。",
		"- kkg_oj_submit_solution 触发系统确认中断后，立即结束本轮回复，等待用户确认或取消；不要重试，不要追问确认。",
		"- 查询提交结果是只读操作，不需要确认。已知 submission_id 时只传 submission_id；没有 submission_id 但有 question_id 时查该题最新提交；都没有时查当前用户最新提交。",
		"",
		"输出要求：",
		"- 直接回答，不要复述“用户想要”。",
		"- 工具没有返回的数据要说明缺失，禁止编造题目、博客或提交结果。",
		"- 用户要代码时必须给完整参考代码；代码块必须使用 <kkg-code lang=\"...\">，不要使用 Markdown 三反引号。",
		"- 默认中文 Markdown。常见结构：思路要点、参考代码、复杂度；用户只要代码时保持更简洁。",
	}, "\n")
}

func agentUserPrompt(state workState) string {
	payload := map[string]any{
		"user_query":       state.Query,
		"question_id":      state.Request.QuestionID,
		"submission_id":    state.Request.SubmissionID,
		"language":         state.Request.Language,
		"code":             state.Request.Code,
		"input":            state.Request.Input,
		"submit_requested": state.Request.Submit,
		"submit_confirmed": state.SubmitConfirmed,
	}
	if len(state.IntentHints) > 0 {
		payload["intent_hints"] = state.IntentHints
	}
	if len(state.ToolPolicy) > 0 {
		payload["tool_policy"] = state.ToolPolicy
	}
	raw, _ := json.MarshalIndent(payload, "", "  ")
	return strings.Join([]string{
		"当前请求的结构化上下文如下。请只依据当前智能体职责处理，不要越权假装拥有未提供的能力或数据。",
		"",
		"```json",
		string(raw),
		"```",
	}, "\n")
}

func platformAgentInputSchema() *schema.ParamsOneOf {
	return schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
		"request": {
			Desc:     "需要 KKG 平台说明子智能体处理的自包含请求。",
			Required: true,
			Type:     schema.String,
		},
	})
}

func blogAgentInputSchema() *schema.ParamsOneOf {
	return schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
		"request": {
			Desc:     "需要 KKG 博客子智能体处理的自包含请求，包含文章主题、题解方向或评论上下文。",
			Required: true,
			Type:     schema.String,
		},
	})
}

func questionAgentInputSchema() *schema.ParamsOneOf {
	return schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
		"request": {
			Desc:     "需要 KKG OJ 题目子智能体处理的自包含请求，例如题面、题解、运行代码、提交代码或查询判题结果。",
			Required: true,
			Type:     schema.String,
		},
		"question_id": {
			Desc: "可选，OJ 题目 ID。",
			Type: schema.Integer,
		},
		"submission_id": {
			Desc: "可选，OJ 提交记录 ID。用户询问某次提交、提交 ID、判题记录 ID 是否通过时传入；按提交 ID 查询时 submission_id 单独即可，不要求 question_id。",
			Type: schema.Integer,
		},
		"language": {
			Desc: "可选，代码语言。",
			Type: schema.String,
		},
		"code": {
			Desc: "可选，待运行或待提交的代码。",
			Type: schema.String,
		},
		"input": {
			Desc: "可选，运行代码时的标准输入。",
			Type: schema.String,
		},
		"submit": {
			Desc: "可选，用户是否请求正式提交代码。true 表示子智能体应在信息齐全时调用提交工具；确认由提交工具触发系统确认中断处理。",
			Type: schema.Boolean,
		},
		"tool_policy": {
			Desc: "可选，运行时工具策略。子智能体应遵守 disable_rag、submit_intent、judge_intent、requires_submit_confirmation、logged_in 等字段。",
			Type: schema.Object,
			SubParams: map[string]*schema.ParameterInfo{
				"logged_in": {
					Desc: "当前会话是否有登录态。",
					Type: schema.Boolean,
				},
				"disable_rag": {
					Desc: "是否禁止使用 RAG。",
					Type: schema.Boolean,
				},
				"submit_intent": {
					Desc: "用户是否请求正式提交代码。",
					Type: schema.Boolean,
				},
				"judge_intent": {
					Desc: "用户是否在查询判题或提交结果。",
					Type: schema.Boolean,
				},
				"requires_submit_confirmation": {
					Desc: "正式提交会触发确认中断；这是提示字段，不应阻止调用提交工具。",
					Type: schema.Boolean,
				},
				"question_id_status": {
					Desc: "题号状态。",
					Type: schema.String,
				},
				"submission_id_status": {
					Desc: "提交记录 ID 状态。",
					Type: schema.String,
				},
				"code_status": {
					Desc: "代码状态。",
					Type: schema.String,
				},
			},
		},
		"intent_hints": {
			Desc: "可选，父智能体识别出的意图提示列表，例如 explicit_question_id、explicit_submission_id、submit_or_judge_request、question_detail_request。",
			Type: schema.Array,
			ElemInfo: &schema.ParameterInfo{
				Type: schema.String,
			},
		},
	})
}

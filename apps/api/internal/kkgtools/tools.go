package kkgtools

import (
	"context"
	"fmt"
	"strings"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"

	"kkg-agent-eino/apps/api/internal/kkg"
)

func New(client *kkg.Client) ([]einotool.BaseTool, error) {
	if client == nil {
		return nil, fmt.Errorf("kkg client is required")
	}
	builders := []func(*kkg.Client) (einotool.BaseTool, error){
		newSearchPostsTool,
		newGetPostTool,
		newListPostsTool,
		newGetPostCommentsTool,
		newListQuestionsTool,
		newGetQuestionTool,
		newRunCodeTool,
		newSubmitSolutionTool,
		newListSubmissionsTool,
		newGetSubmissionResultTool,
		newListQuestionSolutionsTool,
	}
	tools := make([]einotool.BaseTool, 0, len(builders))
	for _, build := range builders {
		tool, err := build(client)
		if err != nil {
			return nil, err
		}
		tools = append(tools, tool)
	}
	return tools, nil
}

func toolContext(ctx context.Context) kkg.ToolContext {
	return kkg.ToolContext{AccessToken: accessTokenFromContext(ctx)}
}

func newSearchPostsTool(client *kkg.Client) (einotool.BaseTool, error) {
	return utils.InferEnhancedTool("kkg_blog_search_posts", "搜索 KKG 博客文章。适合按关键词查找题解、算法文章和知识材料。", func(ctx context.Context, input SearchPostsInput) (*schema.ToolResult, error) {
		return executeTool(ctx, "kkg_blog_search_posts", func(ctx context.Context) (*kkg.BlogSearchResult, error) {
			if strings.TrimSpace(input.Query) == "" {
				return nil, fmt.Errorf("query is required")
			}
			input.Limit = clampInt(input.Limit, 5, 50)
			return client.SearchPosts(ctx, toolContext(ctx), input.Query, input.Limit)
		}, func(out *kkg.BlogSearchResult) string {
			return "search completed"
		})
	})
}

func newGetPostTool(client *kkg.Client) (einotool.BaseTool, error) {
	return utils.InferEnhancedTool("kkg_blog_get_post", "读取一篇 KKG 博客文章详情。适合在已知文章 ID 时获取正文和元数据。", func(ctx context.Context, input GetPostInput) (*schema.ToolResult, error) {
		return executeTool(ctx, "kkg_blog_get_post", func(ctx context.Context) (any, error) {
			if input.ID <= 0 {
				return nil, fmt.Errorf("post id is required")
			}
			return client.GetBlogPost(ctx, toolContext(ctx), input.ID)
		}, func(out any) string {
			return fmt.Sprintf("post %d", input.ID)
		})
	})
}

func newListPostsTool(client *kkg.Client) (einotool.BaseTool, error) {
	return utils.InferEnhancedTool("kkg_blog_list_posts", "读取 KKG 已发布博客文章列表。适合搜索服务不可用时获取最新材料。", func(ctx context.Context, input ListPostsInput) (*schema.ToolResult, error) {
		return executeTool(ctx, "kkg_blog_list_posts", func(ctx context.Context) (any, error) {
			input.Limit = clampInt(input.Limit, 20, 50)
			return client.ListBlogPosts(ctx, toolContext(ctx), input.Limit)
		}, func(out any) string {
			return "latest posts"
		})
	})
}

func newGetPostCommentsTool(client *kkg.Client) (einotool.BaseTool, error) {
	return utils.InferEnhancedTool("kkg_blog_get_post_comments", "读取一篇 KKG 博客文章的评论。适合补充用户讨论和反馈。", func(ctx context.Context, input GetPostCommentsInput) (*schema.ToolResult, error) {
		return executeTool(ctx, "kkg_blog_get_post_comments", func(ctx context.Context) (any, error) {
			if input.PostID <= 0 {
				return nil, fmt.Errorf("post_id is required")
			}
			input.Limit = clampInt(input.Limit, 20, 200)
			return client.GetBlogPostComments(ctx, toolContext(ctx), input.PostID, input.Limit)
		}, func(out any) string {
			return fmt.Sprintf("comments for post %d", input.PostID)
		})
	})
}

func newListQuestionsTool(client *kkg.Client) (einotool.BaseTool, error) {
	return utils.InferEnhancedTool("kkg_oj_list_questions", "分页读取 KKG OJ 题目列表。仅在 RAG 无结果或需要查看最新列表时少量使用；题目推荐优先使用 rag_docs，不要循环翻页。", func(ctx context.Context, input ListQuestionsInput) (*schema.ToolResult, error) {
		return executeTool(ctx, "kkg_oj_list_questions", func(ctx context.Context) (*kkg.PageResult, error) {
			input.Current = clampInt64Min(input.Current, 1)
			input.PageSize = clampInt64(input.PageSize, 5, 50)
			return client.ListQuestions(ctx, toolContext(ctx), kkg.PageRequest{
				Current: input.Current, PageSize: input.PageSize, SortField: input.SortField, SortOrder: input.SortOrder,
			})
		}, summarizePageResult)
	})
}

func newGetQuestionTool(client *kkg.Client) (einotool.BaseTool, error) {
	return utils.InferEnhancedTool("kkg_oj_get_question", "按明确的题目 ID 读取 KKG OJ 题目详情，包括题面、标签、样例和判题配置。不要用于推荐场景批量查询候选题。", func(ctx context.Context, input GetQuestionInput) (*schema.ToolResult, error) {
		return executeTool(ctx, "kkg_oj_get_question", func(ctx context.Context) (*kkg.Question, error) {
			if input.ID <= 0 {
				return nil, fmt.Errorf("question id is required")
			}
			return client.GetQuestion(ctx, toolContext(ctx), input.ID)
		}, func(out *kkg.Question) string {
			if out == nil {
				return "question loaded"
			}
			return strings.TrimSpace(out.Title)
		})
	})
}

func newRunCodeTool(client *kkg.Client) (einotool.BaseTool, error) {
	return utils.InferEnhancedTool("kkg_oj_run_code", "运行代码但不正式提交判题。需要登录态，目前仅支持 Go。", func(ctx context.Context, input RunCodeInput) (*schema.ToolResult, error) {
		return executeTool(ctx, "kkg_oj_run_code", func(ctx context.Context) (any, error) {
			if input.QuestionID <= 0 {
				return nil, fmt.Errorf("question_id is required")
			}
			if strings.TrimSpace(input.Code) == "" {
				return nil, fmt.Errorf("code is required")
			}
			language := input.Language
			if language == "" {
				language = "go"
			}
			return client.RunCode(ctx, toolContext(ctx), kkg.CodeRunRequest{
				Language: language, Code: input.Code, QuestionID: input.QuestionID, Input: input.Input,
			})
		}, func(out any) string {
			return "executed"
		})
	})
}

func newSubmitSolutionTool(client *kkg.Client) (einotool.BaseTool, error) {
	return utils.InferEnhancedTool("kkg_oj_submit_solution", "正式提交代码到 KKG OJ 判题。调用前必须已经获得用户明确确认，例如用户回复“确认提交”。需要登录态，目前仅支持 Go，且受提交频率限制。", func(ctx context.Context, input SubmitSolutionInput) (*schema.ToolResult, error) {
		language := input.Language
		if language == "" {
			language = "go"
		}
		wasInterrupted, hasState, savedState := einotool.GetInterruptState[SubmitApprovalState](ctx)
		if !wasInterrupted {
			if input.QuestionID <= 0 {
				return nil, fmt.Errorf("question_id is required")
			}
			if strings.TrimSpace(input.Code) == "" {
				return nil, fmt.Errorf("code is required")
			}
			state := SubmitApprovalState{
				QuestionID: input.QuestionID,
				Language:   language,
				Code:       input.Code,
			}
			info := SubmitApprovalInfo{
				Action:     "submit_solution",
				Title:      "确认提交代码",
				Message:    fmt.Sprintf("准备提交题目 %d 的 %s 代码。确认后将正式提交到 KKG OJ。", input.QuestionID, strings.ToUpper(language)),
				QuestionID: input.QuestionID,
				Language:   language,
				CodeChars:  len([]rune(strings.TrimSpace(input.Code))),
				CodeLines:  countLines(input.Code),
			}
			return nil, einotool.StatefulInterrupt(ctx, info, state)
		}
		if !hasState {
			return nil, fmt.Errorf("submit approval state is missing")
		}
		isResumeTarget, hasData, decision := einotool.GetResumeContext[SubmitApprovalDecision](ctx)
		if !isResumeTarget {
			info := SubmitApprovalInfo{
				Action:     "submit_solution",
				Title:      "确认提交代码",
				Message:    fmt.Sprintf("准备提交题目 %d 的 %s 代码。确认后将正式提交到 KKG OJ。", savedState.QuestionID, strings.ToUpper(savedState.Language)),
				QuestionID: savedState.QuestionID,
				Language:   savedState.Language,
				CodeChars:  len([]rune(strings.TrimSpace(savedState.Code))),
				CodeLines:  countLines(savedState.Code),
			}
			return nil, einotool.StatefulInterrupt(ctx, info, savedState)
		}
		if !hasData || !decision.Approved {
			return newToolResult(ResultPayload{
				Tool:    "kkg_oj_submit_solution",
				OK:      true,
				Summary: "submission canceled",
				Data: map[string]any{
					"approved":    false,
					"question_id": savedState.QuestionID,
					"language":    savedState.Language,
				},
			})
		}
		return executeTool(ctx, "kkg_oj_submit_solution", func(ctx context.Context) (any, error) {
			if savedState.QuestionID <= 0 {
				return nil, fmt.Errorf("question_id is required")
			}
			if strings.TrimSpace(savedState.Code) == "" {
				return nil, fmt.Errorf("code is required")
			}
			return client.SubmitSolution(ctx, toolContext(ctx), kkg.CodeSubmitRequest{
				Language: savedState.Language, Code: savedState.Code, QuestionID: savedState.QuestionID,
			})
		}, func(out any) string {
			return submitSummary(out)
		})
	})
}

func newListSubmissionsTool(client *kkg.Client) (einotool.BaseTool, error) {
	return utils.InferEnhancedTool("kkg_oj_list_submissions", "分页读取 KKG OJ 提交记录。需要登录态，普通用户只能查看自己的提交。", func(ctx context.Context, input ListSubmissionsInput) (*schema.ToolResult, error) {
		return executeTool(ctx, "kkg_oj_list_submissions", func(ctx context.Context) (*kkg.PageResult, error) {
			input.Current = clampInt64Min(input.Current, 1)
			input.PageSize = clampInt64(input.PageSize, 5, 20)
			return client.ListSubmissions(ctx, toolContext(ctx), kkg.SubmissionListRequest{
				PageRequest: kkg.PageRequest{Current: input.Current, PageSize: input.PageSize},
				QuestionID:  input.QuestionID,
				UserID:      input.UserID,
				Status:      input.Status,
			})
		}, summarizePageResult)
	})
}

func newGetSubmissionResultTool(client *kkg.Client) (einotool.BaseTool, error) {
	return utils.InferEnhancedTool("kkg_oj_get_submission_result", "查询 OJ 提交记录是否通过。调用方式：已知提交 ID 时只传 submission_id；没有提交 ID 但有题号时传 question_id 查询该题最新提交；两者都没有时查询当前用户最新提交。需要登录态。", func(ctx context.Context, input GetSubmissionResultInput) (*schema.ToolResult, error) {
		return executeTool(ctx, "kkg_oj_get_submission_result", func(ctx context.Context) (map[string]any, error) {
			input.MaxPages = clampInt64(input.MaxPages, 5, 20)
			return client.GetSubmissionResult(ctx, toolContext(ctx), input.SubmissionID, input.QuestionID, input.UserID, input.MaxPages)
		}, submissionResultSummary(input.SubmissionID))
	})
}

func newListQuestionSolutionsTool(client *kkg.Client) (einotool.BaseTool, error) {
	return utils.InferEnhancedTool("kkg_oj_list_question_solutions", "读取某道 OJ 题目绑定的题解文章列表。", func(ctx context.Context, input ListQuestionSolutionsInput) (*schema.ToolResult, error) {
		return executeTool(ctx, "kkg_oj_list_question_solutions", func(ctx context.Context) (*kkg.PageResult, error) {
			if input.QuestionID <= 0 {
				return nil, fmt.Errorf("question_id is required")
			}
			input.Current = clampInt64Min(input.Current, 1)
			input.PageSize = clampInt64(input.PageSize, 5, 50)
			return client.ListQuestionSolutions(ctx, toolContext(ctx), kkg.QuestionSolutionListRequest{
				PageRequest: kkg.PageRequest{Current: input.Current, PageSize: input.PageSize},
				QuestionID:  input.QuestionID,
			})
		}, summarizePageResult)
	})
}

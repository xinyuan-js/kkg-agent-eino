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

type SearchPostsInput struct {
	Query string `json:"query" jsonschema:"required" jsonschema_description:"搜索关键词"`
	Limit int    `json:"limit,omitempty" jsonschema_description:"最大返回数量，默认 5，最大 50"`
}

type GetPostInput struct {
	ID int64 `json:"id" jsonschema:"required" jsonschema_description:"博客文章 ID"`
}

type ListPostsInput struct {
	Limit int `json:"limit,omitempty" jsonschema_description:"最大返回数量，默认 20，最大 50"`
}

type GetPostCommentsInput struct {
	PostID int64 `json:"post_id" jsonschema:"required" jsonschema_description:"博客文章 ID"`
	Limit  int   `json:"limit,omitempty" jsonschema_description:"最大返回数量，默认 20，最大 200"`
}

type ListQuestionsInput struct {
	Current   int64  `json:"current,omitempty" jsonschema_description:"页码，默认 1"`
	PageSize  int64  `json:"page_size,omitempty" jsonschema_description:"每页数量，默认 5，最大 50"`
	SortField string `json:"sort_field,omitempty" jsonschema_description:"排序字段"`
	SortOrder string `json:"sort_order,omitempty" jsonschema_description:"排序方向"`
}

type GetQuestionInput struct {
	ID int64 `json:"id" jsonschema:"required" jsonschema_description:"OJ 题目 ID"`
}

type RunCodeInput struct {
	Language   string `json:"language,omitempty" jsonschema_description:"代码语言，目前 KKG OJ 仅支持 go，默认 go"`
	Code       string `json:"code" jsonschema:"required" jsonschema_description:"待运行代码"`
	QuestionID int64  `json:"question_id" jsonschema:"required" jsonschema_description:"OJ 题目 ID"`
	Input      string `json:"input,omitempty" jsonschema_description:"自定义标准输入"`
}

type SubmitSolutionInput struct {
	Language   string `json:"language,omitempty" jsonschema_description:"代码语言，目前 KKG OJ 仅支持 go，默认 go"`
	Code       string `json:"code" jsonschema:"required" jsonschema_description:"待提交代码"`
	QuestionID int64  `json:"question_id" jsonschema:"required" jsonschema_description:"OJ 题目 ID"`
}

type ListSubmissionsInput struct {
	Current    int64 `json:"current,omitempty" jsonschema_description:"页码，默认 1"`
	PageSize   int64 `json:"page_size,omitempty" jsonschema_description:"每页数量，默认 5，最大 20"`
	QuestionID int64 `json:"question_id,omitempty" jsonschema_description:"OJ 题目 ID"`
	UserID     int64 `json:"user_id,omitempty" jsonschema_description:"用户 ID，普通用户只能查询自己"`
	Status     int32 `json:"status,omitempty" jsonschema_description:"提交状态"`
}

type GetSubmissionResultInput struct {
	SubmissionID int64 `json:"submission_id" jsonschema:"required" jsonschema_description:"提交记录 ID"`
	QuestionID   int64 `json:"question_id,omitempty" jsonschema_description:"可选，用于缩小查询范围"`
	UserID       int64 `json:"user_id,omitempty" jsonschema_description:"可选，普通用户通常留空或仅查询自己"`
	MaxPages     int64 `json:"max_pages,omitempty" jsonschema_description:"扫描提交列表的最大页数，默认 5，最大 20"`
}

type ListQuestionSolutionsInput struct {
	Current    int64 `json:"current,omitempty" jsonschema_description:"页码，默认 1"`
	PageSize   int64 `json:"page_size,omitempty" jsonschema_description:"每页数量，默认 5，最大 50"`
	QuestionID int64 `json:"question_id" jsonschema:"required" jsonschema_description:"OJ 题目 ID"`
}

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
	return utils.InferEnhancedTool("kkg_oj_submit_solution", "正式提交代码到 KKG OJ 判题。需要登录态，目前仅支持 Go，且受提交频率限制。", func(ctx context.Context, input SubmitSolutionInput) (*schema.ToolResult, error) {
		return executeTool(ctx, "kkg_oj_submit_solution", func(ctx context.Context) (any, error) {
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
			return client.SubmitSolution(ctx, toolContext(ctx), kkg.CodeSubmitRequest{
				Language: language, Code: input.Code, QuestionID: input.QuestionID,
			})
		}, func(out any) string {
			return "submitted"
		})
	})
}

func newListSubmissionsTool(client *kkg.Client) (einotool.BaseTool, error) {
	return utils.InferEnhancedTool("kkg_oj_list_submissions", "分页读取 KKG OJ 提交记录。需要登录态，普通用户只能查看自己的提交。", func(ctx context.Context, input ListSubmissionsInput) (*schema.ToolResult, error) {
		return executeTool(ctx, "kkg_oj_list_submissions", func(ctx context.Context) (*kkg.PageResult, error) {
			userID, err := scopedUserID(ctx, input.UserID)
			if err != nil {
				return nil, err
			}
			input.Current = clampInt64Min(input.Current, 1)
			input.PageSize = clampInt64(input.PageSize, 5, 20)
			return client.ListSubmissions(ctx, toolContext(ctx), kkg.SubmissionListRequest{
				PageRequest: kkg.PageRequest{Current: input.Current, PageSize: input.PageSize},
				QuestionID:  input.QuestionID,
				UserID:      userID,
				Status:      input.Status,
			})
		}, summarizePageResult)
	})
}

func newGetSubmissionResultTool(client *kkg.Client) (einotool.BaseTool, error) {
	return utils.InferEnhancedTool("kkg_oj_get_submission_result", "查询一条 OJ 提交记录的结果。基于已有提交列表接口分页扫描定位指定 submission_id，需要登录态。", func(ctx context.Context, input GetSubmissionResultInput) (*schema.ToolResult, error) {
		return executeTool(ctx, "kkg_oj_get_submission_result", func(ctx context.Context) (map[string]any, error) {
			userID, err := scopedUserID(ctx, input.UserID)
			if err != nil {
				return nil, err
			}
			input.MaxPages = clampInt64(input.MaxPages, 5, 20)
			return client.GetSubmissionResult(ctx, toolContext(ctx), input.SubmissionID, input.QuestionID, userID, input.MaxPages)
		}, func(out map[string]any) string {
			if out == nil {
				return fmt.Sprintf("submission %d", input.SubmissionID)
			}
			return fmt.Sprintf("submission %d", input.SubmissionID)
		})
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

func clampInt(value, fallback, maxValue int) int {
	if value <= 0 {
		value = fallback
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func clampInt64(value, fallback, maxValue int64) int64 {
	if value <= 0 {
		value = fallback
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func clampInt64Min(value, minValue int64) int64 {
	if value < minValue {
		return minValue
	}
	return value
}

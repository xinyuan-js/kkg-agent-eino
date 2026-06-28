package kkgtools

import "encoding/gob"

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

type SubmitApprovalInfo struct {
	Action     string `json:"action,omitempty"`
	Title      string `json:"title,omitempty"`
	Message    string `json:"message,omitempty"`
	QuestionID int64  `json:"question_id,omitempty"`
	Language   string `json:"language,omitempty"`
	CodeChars  int    `json:"code_chars,omitempty"`
	CodeLines  int    `json:"code_lines,omitempty"`
}

type SubmitApprovalState struct {
	QuestionID int64  `json:"question_id,omitempty"`
	Language   string `json:"language,omitempty"`
	Code       string `json:"code,omitempty"`
}

type SubmitApprovalDecision struct {
	Approved bool `json:"approved"`
}

type ListSubmissionsInput struct {
	Current    int64 `json:"current,omitempty" jsonschema_description:"页码，默认 1"`
	PageSize   int64 `json:"page_size,omitempty" jsonschema_description:"每页数量，默认 5，最大 20"`
	QuestionID int64 `json:"question_id,omitempty" jsonschema_description:"OJ 题目 ID"`
	UserID     int64 `json:"user_id,omitempty" jsonschema_description:"用户 ID，普通用户只能查询自己"`
	Status     int32 `json:"status,omitempty" jsonschema_description:"提交状态：0 pending，1 running，2 accepted，通过，3 rejected，未通过，4 system_error"`
}

type GetSubmissionResultInput struct {
	SubmissionID int64 `json:"submission_id,omitempty" jsonschema_description:"可选，提交记录 ID；按提交 ID 查询时只传 submission_id 即可，不需要 question_id"`
	QuestionID   int64 `json:"question_id,omitempty" jsonschema_description:"可选，仅在 submission_id 为空时用于查询该题最新提交；不是按 submission_id 查询的必填项"`
	UserID       int64 `json:"user_id,omitempty" jsonschema_description:"可选，普通用户通常留空或仅查询自己"`
	MaxPages     int64 `json:"max_pages,omitempty" jsonschema_description:"扫描提交列表的最大页数，默认 5，最大 20。返回 status_label 和 passed 判断是否通过"`
}

type ListQuestionSolutionsInput struct {
	Current    int64 `json:"current,omitempty" jsonschema_description:"页码，默认 1"`
	PageSize   int64 `json:"page_size,omitempty" jsonschema_description:"每页数量，默认 5，最大 50"`
	QuestionID int64 `json:"question_id" jsonschema:"required" jsonschema_description:"OJ 题目 ID"`
}

func init() {
	gob.Register(SubmitApprovalInfo{})
	gob.Register(SubmitApprovalState{})
	gob.Register(SubmitApprovalDecision{})
}

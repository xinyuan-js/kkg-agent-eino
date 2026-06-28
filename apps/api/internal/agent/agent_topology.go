package agent

import (
	"context"
	"strings"

	"github.com/cloudwego/eino/adk"
	einomodel "github.com/cloudwego/eino/components/model"
	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"

	"kkg-agent-eino/apps/api/internal/kkg"
	"kkg-agent-eino/apps/api/internal/kkgtools"
	"kkg-agent-eino/apps/api/internal/memory"
	"kkg-agent-eino/apps/api/internal/rag"
)

type agentTopology struct {
	tools  []einotool.BaseTool
	runner *adk.Runner
}

func splitToolsByIntent(ctx context.Context, tools []einotool.BaseTool) ([]einotool.BaseTool, []einotool.BaseTool, error) {
	blogTools := make([]einotool.BaseTool, 0)
	questionTools := make([]einotool.BaseTool, 0)
	for _, t := range tools {
		info, err := t.Info(ctx)
		if err != nil {
			return nil, nil, err
		}
		switch {
		case strings.HasPrefix(info.Name, "kkg_blog_"):
			blogTools = append(blogTools, t)
		case strings.HasPrefix(info.Name, "kkg_oj_"):
			questionTools = append(questionTools, t)
		}
	}
	return blogTools, questionTools, nil
}

func buildAgentTopology(ctx context.Context, retriever rag.Retriever, kkgClient *kkg.Client, chatModel einomodel.BaseChatModel, memoryStore memory.Store) (agentTopology, error) {
	kkgTools, err := kkgtools.New(kkgClient)
	if err != nil {
		return agentTopology{}, err
	}
	ragTool, err := newRAGSearchTool(retriever)
	if err != nil {
		return agentTopology{}, err
	}
	blogTools, questionOnlyTools, err := splitToolsByIntent(ctx, kkgTools)
	if err != nil {
		return agentTopology{}, err
	}

	questionTools := make([]einotool.BaseTool, 0, len(questionOnlyTools)+len(blogTools))
	questionTools = append(questionTools, questionOnlyTools...)
	questionTools = append(questionTools, blogTools...)

	questionAgent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        questionAgentName,
		Description: "Use this agent only for KKG OJ question explanation, solution ideas, related blogs, code run, code submit, or judge result requests.",
		Instruction: questionAgentInstruction(),
		Model:       chatModel,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools:               questionTools,
				ExecuteSequentially: true,
			},
		},
		MaxIterations: 8,
	})
	if err != nil {
		return agentTopology{}, err
	}

	platformAgent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:          platformAgentName,
		Description:   "Assistant for KKG platform capability, API, login, deployment, and project structure questions.",
		Instruction:   platformAgentInstruction(),
		Model:         chatModel,
		MaxIterations: 4,
	})
	if err != nil {
		return agentTopology{}, err
	}

	blogAgent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        blogAgentName,
		Description: "Assistant for KKG blog article discovery, explanation, and related knowledge lookup.",
		Instruction: blogAgentInstruction(),
		Model:       chatModel,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools:               blogTools,
				ExecuteSequentially: true,
			},
		},
		MaxIterations: 6,
	})
	if err != nil {
		return agentTopology{}, err
	}

	questionTool, err := withQuestionAgentRuntimeContext(adk.NewAgentTool(ctx, questionAgent, adk.WithAgentInputSchema(questionAgentInputSchema())))
	if err != nil {
		return agentTopology{}, err
	}

	routerTools := []einotool.BaseTool{
		ragTool,
		adk.NewAgentTool(ctx, platformAgent, adk.WithAgentInputSchema(platformAgentInputSchema())),
		adk.NewAgentTool(ctx, blogAgent, adk.WithAgentInputSchema(blogAgentInputSchema())),
		questionTool,
	}
	routerAgent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        routerAgentName,
		Description: "Top-level router agent for KKG Agent. It decides whether to answer directly or delegate to specialized sub-agents.",
		Instruction: routerAgentInstruction(),
		Model:       chatModel,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools:               routerTools,
				ExecuteSequentially: true,
			},
			EmitInternalEvents: true,
		},
		MaxIterations: 8,
	})
	if err != nil {
		return agentTopology{}, err
	}

	exposedTools := make([]einotool.BaseTool, 0, len(kkgTools)+1)
	exposedTools = append(exposedTools, ragTool)
	exposedTools = append(exposedTools, kkgTools...)

	return agentTopology{
		tools:  exposedTools,
		runner: adk.NewRunner(ctx, adk.RunnerConfig{Agent: routerAgent, EnableStreaming: true, CheckPointStore: memoryStore}),
	}, nil
}

package agent

import (
	"context"
	"strings"

	"github.com/cloudwego/eino/compose"
)

func (s *Service) compileChain(ctx context.Context) (compose.Runnable[RunRequest, RunResponse], error) {
	c := compose.NewChain[RunRequest, RunResponse]()
	c.AppendLambda(compose.InvokableLambda(s.normalize), compose.WithNodeName("normalize"))
	c.AppendLambda(compose.InvokableLambda(s.prepareSession), compose.WithNodeName("prepare_session"))
	c.AppendLambda(compose.InvokableLambda(s.classifyRequest), compose.WithNodeName("classify_request"))
	c.AppendLambda(compose.InvokableLambda(s.executeRouterAgent), compose.WithNodeName("adk_chat_model_agent"))
	c.AppendLambda(compose.InvokableLambda(s.postprocessAnswer), compose.WithNodeName("postprocess_answer"))
	c.AppendLambda(compose.InvokableLambda(s.persistSession), compose.WithNodeName("persist_session"))
	c.AppendLambda(compose.InvokableLambda(s.buildResponse), compose.WithNodeName("build_response"))
	return c.Compile(ctx, compose.WithGraphName("kkg_agent_chain"))
}

func (s *Service) compileGraph(ctx context.Context) (compose.Runnable[RunRequest, RunResponse], error) {
	g := compose.NewGraph[RunRequest, RunResponse]()
	if err := g.AddLambdaNode("normalize", compose.InvokableLambda(s.normalize)); err != nil {
		return nil, err
	}
	if err := g.AddLambdaNode("classify_request", compose.InvokableLambda(s.classifyRequest)); err != nil {
		return nil, err
	}
	if err := g.AddLambdaNode("prepare_session", compose.InvokableLambda(s.prepareSession)); err != nil {
		return nil, err
	}
	if err := g.AddLambdaNode("direct_answer", compose.InvokableLambda(s.directAnswer)); err != nil {
		return nil, err
	}
	if err := g.AddLambdaNode("adk_chat_model_agent", compose.InvokableLambda(s.executeRouterAgent)); err != nil {
		return nil, err
	}
	if err := g.AddLambdaNode("postprocess_answer", compose.InvokableLambda(s.postprocessAnswer)); err != nil {
		return nil, err
	}
	if err := g.AddLambdaNode("persist_session", compose.InvokableLambda(s.persistSession)); err != nil {
		return nil, err
	}
	if err := g.AddLambdaNode("build_response", compose.InvokableLambda(s.buildResponse)); err != nil {
		return nil, err
	}
	edges := [][2]string{
		{compose.START, "normalize"},
		{"normalize", "prepare_session"},
		{"prepare_session", "classify_request"},
		{"direct_answer", "postprocess_answer"},
		{"adk_chat_model_agent", "postprocess_answer"},
		{"postprocess_answer", "persist_session"},
		{"persist_session", "build_response"},
		{"build_response", compose.END},
	}
	for _, edge := range edges {
		if err := g.AddEdge(edge[0], edge[1]); err != nil {
			return nil, err
		}
	}
	if err := g.AddBranch("classify_request", compose.NewGraphBranch(func(ctx context.Context, state workState) (string, error) {
		if strings.TrimSpace(state.DirectAnswer) != "" {
			return "direct_answer", nil
		}
		return "adk_chat_model_agent", nil
	}, map[string]bool{"direct_answer": true, "adk_chat_model_agent": true})); err != nil {
		return nil, err
	}
	return g.Compile(ctx, compose.WithGraphName("kkg_agent_graph"), compose.WithMaxRunSteps(20))
}

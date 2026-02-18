package agent

import (
	"github.com/pocketomega/pocket-omega/internal/core"
	"github.com/pocketomega/pocket-omega/internal/llm"
	"github.com/pocketomega/pocket-omega/internal/tool"
)

// BuildAgentFlow assembles the full ReAct decision loop:
//
// app mode (default):
//
//	DecideNode ──┬── ActionTool   → ToolNode   ──→ DecideNode
//	             ├── ActionThink  → ThinkNode  ──→ DecideNode
//	             └── ActionAnswer → AnswerNode ──→ End
//
// native mode (model handles thinking):
//
//	DecideNode ──┬── ActionTool   → ToolNode   ──→ DecideNode
//	             └── ActionAnswer → AnswerNode ──→ End
func BuildAgentFlow(provider llm.LLMProvider, registry *tool.Registry, thinkingMode string) core.Workflow[AgentState] {
	// Create nodes
	decideNode := core.NewNode[AgentState, DecidePrep, Decision](
		NewDecideNode(provider), 1,
	)
	toolNode := core.NewNode[AgentState, ToolPrep, ToolExecResult](
		NewToolNode(registry), 0,
	)
	answerNode := core.NewNode[AgentState, AnswerPrep, AnswerResult](
		NewAnswerNode(provider), 1,
	)

	// Wire the decision loop
	decideNode.AddSuccessor(toolNode, core.ActionTool)
	decideNode.AddSuccessor(answerNode, core.ActionAnswer)

	// Only register ThinkNode in app mode
	if thinkingMode == "app" {
		thinkNode := core.NewNode[AgentState, ThinkPrep, ThinkResult](
			NewThinkNode(provider), 1,
		)
		decideNode.AddSuccessor(thinkNode, core.ActionThink)
		thinkNode.AddSuccessor(decideNode) // ActionDefault → DecideNode
	}

	// ToolNode loops back to DecideNode
	toolNode.AddSuccessor(decideNode) // ActionDefault → DecideNode

	// AnswerNode ends the flow (ActionEnd has no successor)

	// Wrap in a Flow to enable successor chaining.
	flow := core.NewFlow[AgentState](decideNode)
	return flow
}

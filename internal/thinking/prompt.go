package thinking

import "fmt"

// buildPrompt constructs the full LLM prompt for the current thought step.
func buildPrompt(prep PrepData) string {
	instructionBase := fmt.Sprintf(`你的任务是生成下一个思考步骤（思考 %d）。

重要原则：
- **简单问题快速作答：** 对于问候、闲聊、常识问答等简单问题，直接在第一步给出结论，不要过度分解。
- **只对真正复杂的问题进行多步推理：** 如数学推导、多步分析、需要验证的逻辑问题等。
- **避免过度分解：** 不要为简单内容创建子步骤。计划步骤总数不应超过 5 个（不含结论）。
- **果断收尾：** 当已有足够信息得出结论时，立即执行"结论"步骤，不要继续添加新步骤。

指令：
1. **评估上一步思考：** 如果不是第一步，在 current_thinking 开头简要评估思考 %d（一句话即可）。
2. **执行步骤：** 执行计划中第一个状态为 Pending 的步骤。
3. **维护计划结构：** 生成更新后的 planning 列表。每项包含键：description、status（"Pending"/"Done"/"Verification Needed"），可选 result 或 mark。
4. **更新步骤状态：** 已执行步骤标记为 "Done" 并添加 result。
5. **细化计划：** 仅在步骤确实复杂到无法一步完成时才添加 sub_steps。
6. **结论步骤：** 计划必须包含 description: "结论" 的最终步骤。执行时 result 字段直接给出面向用户的自然回答。
7. **语言：** 统一使用中文。
8. **终止条件：** 仅在执行"结论"步骤时设 next_thought_needed 为 false。`,
		prep.CurrentThoughtNo,
		prep.CurrentThoughtNo-1,
	)

	var instructionContext string
	if prep.IsFirstThought {
		instructionContext = `
**这是第一步思考。** 先判断问题复杂度：
- **简单问题**（问候、常识、单一答案）：直接创建 2 步计划 [回答要点 → 结论]，在本步完成回答并将结论标记为 Done，设 next_thought_needed: false。
- **复杂问题**（多步推理、需验证）：创建 3-5 步计划，执行第一步。`
	} else {
		instructionContext = fmt.Sprintf(`
**上一步计划（简化视图）：**
%s

简要评估思考 %d，然后执行下一个 Pending 步骤。如果所有分析已完成，直接执行"结论"步骤。`,
			prep.LastPlanText, prep.CurrentThoughtNo-1)
	}

	instructionFormat := `
将回复严格按以下 YAML 结构输出：
` + "```yaml" + `
current_thinking: |
  # 当前步骤的思考过程（简洁明了）
planning:
  - description: "步骤描述"
    status: "Done"
    result: "简要结果"
  - description: "结论"
    status: "Pending"
next_thought_needed: true
` + "```"

	return fmt.Sprintf(`你是一个高效的 AI 助手。根据问题复杂度灵活调整思考深度：简单问题 1-2 步解决，复杂问题才进行多步推理。使用 YAML 字典结构管理计划。

问题：%s

之前的思考：
%s
--------------------
%s
%s
%s`,
		prep.Problem,
		prep.ThoughtsText,
		instructionBase,
		instructionContext,
		instructionFormat,
	)
}

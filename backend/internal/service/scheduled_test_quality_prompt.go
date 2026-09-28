package service

import "strings"

// DefaultScheduledTestPrompt is the pelican animation used to judge whether a
// model has been dumbed down. Only this prompt has a local quality rubric.
const DefaultScheduledTestPrompt = "请生成可直接运行的单文件HTML，使用内联SVG绘制鹈鹕骑自行车的二维循环动画。画面以鹈鹕和自行车为主体，展示清晰的身体结构、踩踏动作和车轮转动，配合协调的背景、配色与层次。动画应流畅自然、衔接连续，并适配不同屏幕尺寸。动画必须用 CSS @keyframes 或 SMIL（animate/animateTransform）实现，不要使用 JavaScript 或 <script> 标签。禁止依赖外部资源，只输出完整HTML，不要代码围栏或解释文字。"

func isDefaultScheduledTestPrompt(prompt string) bool {
	trimmed := strings.TrimSpace(prompt)
	return trimmed == "" || trimmed == DefaultScheduledTestPrompt
}

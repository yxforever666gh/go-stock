package research

import "strings"

const knowledgeOwnerResearch1 = "research1"

func knowledgeQuery(marketReport string) string {
	value := strings.TrimSpace(marketReport)
	if len(value) > 1024 {
		value = truncateUTF8(value, 1024)
	}
	if value == "" {
		return "市场 题材 风险 行业 个股"
	}
	return "市场 题材 风险 行业 个股 " + value
}

func appendKnowledgeContext(prompt, context string) string {
	context = strings.TrimSpace(context)
	if context == "" {
		return prompt
	}
	return prompt + "\n\n" + context
}

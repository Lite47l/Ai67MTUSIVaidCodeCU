package pulse

// Templates contain no participant text and are never sent automatically.
type conversationTemplate struct {
	Situation string
	Questions []string
	Message   string
}

var conversationTemplates = map[string]conversationTemplate{
	"weekly_1on1":   {Situation: "Стоит уточнить, насколько регулярно стажер получает обратную связь.", Questions: []string{"Как сейчас устроены сверки с наставником?", "Какие вопросы дольше всего остаются без ответа?", "Какой формат коротких встреч будет полезен?"}, Message: "Предлагаем провести короткие еженедельные встречи для сверки задач и вопросов. Давайте согласуем удобный формат и время."},
	"reduce_load":   {Situation: "Стоит обсудить нагрузку, распределение задач и сроки.", Questions: []string{"Какие задачи сейчас конкурируют за время?", "Как расставляются приоритеты между ними?", "Какие сроки или объемы стоит обсудить?"}, Message: "Предлагаем сверить текущие задачи и сроки. Давайте согласуем приоритеты и доступную нагрузку."},
	"clarify_tasks": {Situation: "Стоит сверить ожидания и критерии готовности по задачам.", Questions: []string{"Что сейчас вызывает больше всего вопросов в задачах?", "Как вы определяете готовность результата?", "Какие уточнения помогут начать следующую задачу?"}, Message: "Предлагаем сверить ожидания по текущим задачам. Давайте согласуем критерии готовности результата."},
	"hr_talk":       {Situation: "Стоит уточнить у участников, где возникает сложность во взаимодействии.", Questions: []string{"Как сейчас распределены роли в команде?", "В каких ситуациях нужна дополнительная поддержка?", "Что поможет сделать рабочее общение удобнее?"}, Message: "Предлагаем обсудить рабочее взаимодействие участников. Давайте уточним удобный порядок связи."},
	"other":         {Situation: "Стоит обсудить с участниками причины оценки.", Questions: []string{"Что повлияло на оценку в этом месяце?", "Какие условия помогут улучшить ситуацию?", "Как можно проверить изменения в следующем месяце?"}, Message: "Предлагаем обсудить текущий формат работы. Давайте согласуем ближайший шаг и дату сверки."},
}

func fallbackConversationPlans(rs []Response, month int) []ConversationPlan {
	grounds := map[string][]string{}
	add := func(action, id, field string) {
		ref := id + "#" + field
		for _, v := range grounds[action] {
			if v == ref {
				return
			}
		}
		grounds[action] = append(grounds[action], ref)
	}
	actions := map[string]string{"mentorSupport": "weekly_1on1", "taskClarity": "clarify_tasks", "taskUnderstanding": "clarify_tasks", "workload": "reduce_load", "deadlines": "reduce_load", "teamwork": "hr_talk", "communication": "hr_talk"}
	for _, row := range rs {
		if row.Month != month {
			continue
		}
		for _, field := range Fields(row.Source) {
			score := row.Scores[field]
			if score != nil && *score <= 2 {
				action := actions[field]
				if action == "" {
					action = "other"
				}
				add(action, row.EventID, field)
			}
		}
	}
	for _, action := range []string{"weekly_1on1", "reduce_load", "clarify_tasks", "hr_talk", "other"} {
		ids := grounds[action]
		if len(ids) == 0 {
			continue
		}
		if len(ids) > 8 {
			ids = ids[:8]
		}
		template := conversationTemplates[action]
		return []ConversationPlan{{Month: month, Situation: template.Situation, EvidenceIDs: ids, Questions: append([]string{}, template.Questions...), ActionType: action, MentorMessage: template.Message}}
	}
	return []ConversationPlan{}
}
func conversationPlansForMonth(a *Analysis, rs []Response, month int) []ConversationPlan {
	if a != nil {
		for _, plan := range a.ConversationPlans {
			if plan.Month == month {
				return []ConversationPlan{plan}
			}
		}
	}
	return fallbackConversationPlans(rs, month)
}

// Only HR receives new hidden signals and conversation plans; pre-existing
// summary/recommendations remain available under the established role policy.
func publicAnalysis(a *Analysis) *Analysis {
	if a == nil {
		return nil
	}
	out := *a
	out.ConversationPlans = nil
	out.Signals = []Signal{}
	for _, signal := range a.Signals {
		if signal.Kind != "text_score_mismatch" {
			out.Signals = append(out.Signals, signal)
		}
	}
	return &out
}

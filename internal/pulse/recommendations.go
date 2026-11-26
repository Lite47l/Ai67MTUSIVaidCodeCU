package pulse

import (
	"fmt"
	"regexp"
	"strings"
)

type Recommendation struct {
	HuntflowDelivery *TaskDelivery `json:"huntflowDelivery,omitempty"`
	ID               string        `json:"id"`
	Month            int           `json:"month"`
	ActionType       string        `json:"actionType"`
	Audience         string        `json:"audience"`
	Title            string        `json:"title"`
	Reason           string        `json:"reason"`
	Questions        []string      `json:"questions"`
	Delivery         *TaskDelivery `json:"delivery,omitempty"`
}

type recommendationTemplate struct {
	action, audience, title, reason string
	questions                       []string
}

var recommendationTemplates = []recommendationTemplate{
	{"reduce_load", "mentor", "Провести короткий созвон про нагрузку",
		"Сверить объем параллельных задач и сроки.",
		[]string{"Какие задачи сейчас конкурируют за время?", "Что можно перенести или уточнить по срокам?"}},
	{"clarify_tasks", "mentor", "Сверить задачи и ожидания",
		"Уточнить приоритеты и критерии готовности.",
		[]string{"Какие ожидания стоит уточнить?", "Как будем определять, что задача готова?"}},
	{"weekly_1on1", "mentor", "Согласовать регулярные встречи 1:1",
		"Обсудить, как сделать обратную связь регулярнее.",
		[]string{"Как сейчас организованы сверки?", "Какой формат коротких встреч поможет?"}},
	{"increase_task_complexity", "hr", "Запрос на развитие",
		"Обсудить с наставником задачу следующего уровня сложности.",
		[]string{"Какую задачу можно усложнить постепенно?", "Какая поддержка понадобится стажёру?"}},
	{"hr_talk", "hr", "Обсудить ситуацию с участниками",
		"HR может уточнить обстоятельства и согласовать шаг поддержки.",
		[]string{"В каких ситуациях нужна поддержка?", "Что стоит изменить в формате работы?"}},
	{"other", "hr", "Уточнить следующий шаг",
		"HR определяет, какое действие уместно и кто за него отвечает.",
		[]string{"Что повлияло на ситуацию?", "Как можно проверить изменения в следующем месяце?"}},
}

var developmentRequest = regexp.MustCompile(
	`(?:^|[^а-я])((?:не +)?(?:очень +)?(?:хочу|хотелось бы|хотел[аи]? бы|готов[аы]?|было бы интересно))[^.!?;\n]{0,65}(?:более +(?:сложн|ответственн|самостоятельн)[а-я]+|посложнее|сложнее|повышенн[а-я]+ +сложност[а-я]*|больше +(ответственн|самостоятельн)[а-я]+|самостоятельно +(?:вести|выполнять|решать))`,
)

func asksForDevelopment(comments map[string]string) bool {
	for _, text := range comments {
		normalized := strings.ReplaceAll(strings.ToLower(text), "ё", "е")
		for _, match := range developmentRequest.FindAllStringSubmatch(normalized, -1) {
			if !strings.HasPrefix(match[1], "не ") {
				return true
			}
		}
	}
	return false
}

func actionForField(field string) string {
	switch field {
	case "workload", "deadlines":
		return "reduce_load"
	case "taskClarity", "taskUnderstanding":
		return "clarify_tasks"
	case "mentorSupport":
		return "weekly_1on1"
	case "teamwork", "communication":
		return "hr_talk"
	default:
		return "other"
	}
}

func monthRecommendations(internID string, month int, rows []Response, analysis *Analysis, rules []Signal) []Recommendation {
	selected := map[string]bool{}
	for _, row := range rows {
		if row.Month == month && row.Source == "intern" && asksForDevelopment(row.Comments) {
			selected["increase_task_complexity"] = true
		}
	}
	for _, signal := range rules {
		if len(signal.Evidence) > 0 {
			selected[actionForField(signal.Evidence[0].Field)] = true
		}
	}
	for _, signal := range hiddenSignalsForMonth(analysis, rows, month) {
		selected[actionForField(signal.Evidence[0].Field)] = true
	}
	if analysis != nil {
		for _, plan := range analysis.ConversationPlans {
			if plan.Month != month {
				continue
			}
			switch plan.ActionType {
			case "reduce_load", "clarify_tasks", "weekly_1on1", "hr_talk":
				selected[plan.ActionType] = true
			default:
				selected["other"] = true
			}
		}
	}
	out := []Recommendation{}
	for _, template := range recommendationTemplates {
		if selected[template.action] {
			out = append(out, Recommendation{
				ID:    digest(fmt.Sprintf("%s:%s:%d:%s", Program, internID, month, template.action)),
				Month: month, ActionType: template.action, Audience: template.audience,
				Title: template.title, Reason: template.reason, Questions: template.questions,
			})
		}
	}
	return out
}

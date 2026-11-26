package pulse

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"
)

// Column labels are the contract of the supplied questionnaires, not positions.
func ReadExcel(path, source string) ([]Response, error) {
	if source != "intern" && source != "mentor" {
		return nil, fmt.Errorf("неизвестный источник")
	}
	f, err := excelize.OpenFile(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sheets := f.GetSheetList()
	if len(sheets) != 1 {
		return nil, fmt.Errorf("ожидается ровно один лист")
	}
	rows, err := f.GetRows(sheets[0])
	if err != nil {
		return nil, err
	}
	if len(rows) < 2 {
		return nil, fmt.Errorf("нет строк данных")
	}
	headers := map[string]int{}
	for i, h := range rows[0] {
		h = strings.TrimSpace(h)
		if _, ok := headers[h]; ok {
			return nil, fmt.Errorf("дублирующийся столбец %s", h)
		}
		headers[h] = i
	}
	required := []string{"ID стажера", "Подразделение / направление стажировки", "Контрольный период"}
	if source == "mentor" {
		required = append(required, "ID наставника")
	}
	for _, k := range append(Fields(source), CommentFields(source)...) {
		required = append(required, Labels[k])
	}
	for _, h := range required {
		if _, ok := headers[h]; !ok {
			return nil, fmt.Errorf("отсутствует столбец %s", h)
		}
	}
	out := []Response{}
	for n, row := range rows[1:] {
		empty := true
		for _, c := range row {
			if strings.TrimSpace(c) != "" {
				empty = false
			}
		}
		if empty {
			continue
		}
		get := func(h string) string {
			i, ok := headers[h]
			if !ok || i >= len(row) {
				return ""
			}
			return strings.TrimSpace(row[i])
		}
		period := strings.Fields(get("Контрольный период"))
		if len(period) != 2 || period[0] != "Месяц" {
			return nil, fmt.Errorf("строка %d: неверный период", n+2)
		}
		month, err := strconv.Atoi(period[1])
		if err != nil {
			return nil, fmt.Errorf("строка %d: неверный месяц", n+2)
		}
		r := Response{ProgramID: Program, InternID: get("ID стажера"), Department: get("Подразделение / направление стажировки"), Source: source, Month: month, Scores: map[string]*int{}, Comments: map[string]string{}}
		if source == "mentor" {
			r.MentorID = get("ID наставника")
		}
		r.EventID = fmt.Sprintf("history:%s:%s:%d", source, r.InternID, month)
		for _, k := range Fields(source) {
			v := get(Labels[k])
			if v == "" {
				r.Scores[k] = nil
				continue
			}
			score, err := strconv.Atoi(v)
			if err != nil {
				return nil, fmt.Errorf("строка %d, %s: ожидается целая оценка", n+2, Labels[k])
			}
			r.Scores[k] = &score
		}
		for _, k := range CommentFields(source) {
			r.Comments[k] = get(Labels[k])
		}
		raw := map[string]string{}
		for h := range headers {
			raw[h] = get(h)
		}
		r.Raw, _ = json.Marshal(raw)
		if err = r.Validate(); err != nil {
			return nil, fmt.Errorf("строка %d: %w", n+2, err)
		}
		out = append(out, r)
	}
	return out, nil
}

// Тесты неэкспортируемой части транспорта, поэтому пакет тот же, а не rest_test.
package rest

import (
	"testing"

	"sobes_stackbridge_go/internal/model"
)

// TestMergeViolationsKeepsUnknownFields — главное свойство: fieldOrder
// упорядочивает перечень, но не решает, о чём сообщать. Поле, забытое в списке,
// раньше исчезало из ответа, а когда нарушение по нему было единственным,
// запрос признавался корректным.
func TestMergeViolationsKeepsUnknownFields(t *testing.T) {
	unknown := model.Violation{Field: "discount", Message: "не может быть отрицательной"}

	merged := mergeViolations([]model.Violation{unknown})

	if len(merged) != 1 {
		t.Fatalf("нарушений = %d (%v), ожидалось 1", len(merged), merged)
	}

	if merged[0] != unknown {
		t.Errorf("нарушение = %v, ожидалось %v", merged[0], unknown)
	}

	// Ради этого всё и делается: с потерянным нарушением ошибка получалась
	// пустой, и запрос уходил в базу как корректный.
	if err := model.NewValidationError(merged...); err == nil {
		t.Error("NewValidationError вернул nil — запрос с нарушением признан корректным")
	}
}

// TestMergeViolationsPutsUnknownFieldsLast закрепляет, что неучтённое поле
// попадает в конец, а не вклинивается в середину известных.
func TestMergeViolationsPutsUnknownFieldsLast(t *testing.T) {
	merged := mergeViolations([]model.Violation{
		{Field: "discount", Message: "не может быть отрицательной"},
		{Field: model.FieldPrice, Message: "не указана"},
		{Field: model.FieldServiceName, Message: "не может быть пустым"},
	})

	wantFields := []string{model.FieldServiceName, model.FieldPrice, "discount"}
	if len(merged) != len(wantFields) {
		t.Fatalf("нарушений = %d (%v), ожидалось %d", len(merged), merged, len(wantFields))
	}

	for i, want := range wantFields {
		if merged[i].Field != want {
			t.Errorf("нарушение %d относится к полю %q, ожидалось %q", i, merged[i].Field, want)
		}
	}
}

// TestMergeViolationsNormalizesOrder: порядок в ответе задаёт fieldOrder, а не
// то, на каком этапе проверки поле споткнулось.
func TestMergeViolationsNormalizesOrder(t *testing.T) {
	merged := mergeViolations([]model.Violation{
		{Field: model.FieldEndDate, Message: "должна быть в формате MM-YYYY, например 12-2025"},
		{Field: model.FieldUserID, Message: "должен быть корректным UUID"},
		{Field: model.FieldServiceName, Message: "не может быть пустым"},
	})

	wantFields := []string{model.FieldServiceName, model.FieldUserID, model.FieldEndDate}
	for i, want := range wantFields {
		if merged[i].Field != want {
			t.Errorf("нарушение %d относится к полю %q, ожидалось %q", i, merged[i].Field, want)
		}
	}
}

// TestMergeViolationsKeepsFirstPerField: по полю остаётся одно нарушение, из
// первой группы. Сообщение о разборе точнее, чем «не указано» от инвариантов.
func TestMergeViolationsKeepsFirstPerField(t *testing.T) {
	parse := []model.Violation{{Field: model.FieldStartDate, Message: "должна быть в формате MM-YYYY, например 07-2025"}}
	invariants := []model.Violation{{Field: model.FieldStartDate, Message: "не указана"}}

	merged := mergeViolations(parse, invariants)

	if len(merged) != 1 {
		t.Fatalf("нарушений = %d (%v), ожидалось 1", len(merged), merged)
	}

	if merged[0] != parse[0] {
		t.Errorf("нарушение = %v, ожидалось %v", merged[0], parse[0])
	}
}

// TestFieldOrderCoversModelFields — вторая линия обороны: даже если сортировка
// когда-нибудь снова начнёт фильтровать, забытое поле поймает этот тест.
func TestFieldOrderCoversModelFields(t *testing.T) {
	fields := []string{
		model.FieldServiceName,
		model.FieldPrice,
		model.FieldUserID,
		model.FieldStartDate,
		model.FieldEndDate,
	}

	for _, field := range fields {
		if fieldRank(field) == len(fieldOrder) {
			t.Errorf("поле %q не перечислено в fieldOrder", field)
		}
	}
}

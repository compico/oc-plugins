package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
)

// Умолчания настроек обязаны совпадать с кодом виджета: редактор хранит только изменённые значения.
func TestWidgetDefaultsMatchWidgetCode(t *testing.T) {
	js, err := os.ReadFile("ui/main.js")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`(?:str|num|bool)\("(\w+)",\s*([^)]*)\)`)
	code := map[string]string{}
	for _, m := range re.FindAllStringSubmatch(string(js), -1) {
		code[m[1]] = strings.Trim(strings.TrimSpace(m[2]), `"`)
	}
	settings := widgetSettings()
	if len(settings) == 0 {
		t.Fatal("настроек нет")
	}
	for _, s := range settings {
		want, ok := code[s.Key]
		if !ok {
			t.Errorf("настройка %q не читается в ui/main.js", s.Key)
			continue
		}
		if got := fmt.Sprint(s.Default); got != want {
			t.Errorf("настройка %q: умолчание %q, а в виджете %q", s.Key, got, want)
		}
	}
}

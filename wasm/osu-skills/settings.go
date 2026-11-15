package main

import "strings"

const (
	defaultStyle = "ribbon"
	minWindow    = 500 // мельче точек не нужно: расчёт тяжелее, график не лучше
)

// Settings — то, что виджет узнаёт у плагина при старте (POST /settings).
type Settings struct {
	Style  string `json:"style"`  // ribbon | silhouette | lines
	Window int    `json:"window"` // мс карты на одну точку
}

// settingsFrom приводит введённые пользователем значения к допустимым: неизвестный стиль — стиль по умолчанию,
// окно вне 500..60000 мс — окно по умолчанию.
func settingsFrom(cfg Config) Settings {
	s := Settings{Style: defaultStyle, Window: defaultWindow}
	switch st := strings.ToLower(strings.TrimSpace(cfg.Style)); st {
	case "ribbon", "silhouette", "lines":
		s.Style = st
	}
	if cfg.Window >= minWindow && cfg.Window <= maxWindow {
		s.Window = cfg.Window
	}
	return s
}

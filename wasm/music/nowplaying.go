package main

import (
	"encoding/json"
	"net/http"
	"regexp"
	"time"
	"unicode/utf8"
)

// «Сейчас играет»: аддон плеера присылает состояние (POST /nowplaying), виджет читает его вместе с очередью (GET /queue).
// Состояние живёт только в памяти плагина: оно устаревает за секунды, хранить его в storage незачем (и незачем писать
// в хранилище каждые 5 секунд). Ввод недоверенный: любой локальный процесс может отправить POST, поэтому всё проверяется.

const (
	staleAfter    = 20 * time.Second // аддон шлёт состояние каждые ~5 с; тишина дольше — плеер закрыт
	maxTitle      = 200
	maxArtist     = 100
	maxArtists    = 5
	maxDurationMs = 24 * 60 * 60 * 1000
)

var (
	trackIDRe = regexp.MustCompile(`^[0-9]{1,20}$`)
	// Обложка — путь на avatars.yandex.net без схемы и без «://»: виджет сам достраивает https://avatars.yandex.net/…,
	// так что подставить произвольный адрес (трекер, чужой сайт) через этот маршрут нельзя.
	coverRe = regexp.MustCompile(`^avatars\.yandex\.net/[A-Za-z0-9._/%-]{1,300}$`)
)

// NowPlaying — то, что видит виджет.
type NowPlaying struct {
	TrackID    string   `json:"trackId"`
	Title      string   `json:"title"`
	Artists    []string `json:"artists"`
	Cover      string   `json:"cover,omitempty"`
	DurationMs int64    `json:"durationMs"`
	PositionMs int64    `json:"positionMs"`
	Playing    bool     `json:"playing"`
}

type nowBody struct {
	TrackID    string   `json:"trackId"`
	Title      string   `json:"title"`
	Artists    []string `json:"artists"`
	Cover      string   `json:"cover"`
	DurationMs int64    `json:"durationMs"`
	PositionMs int64    `json:"positionMs"`
	Playing    bool     `json:"playing"`
}

// Player хранит последнее состояние плеера.
type Player struct {
	Now func() time.Time

	cur *NowPlaying
	at  time.Time
}

func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// Set принимает состояние от аддона. Пустой trackId — ничего не играет (состояние сбрасывается).
func (p *Player) Set(raw json.RawMessage) Response {
	var b nowBody
	if err := json.Unmarshal(raw, &b); err != nil {
		return errResp(http.StatusBadRequest, "body must be a now-playing object")
	}
	if b.TrackID == "" {
		p.cur = nil
		return Response{http.StatusOK, map[string]any{"ok": true}}
	}
	if !trackIDRe.MatchString(b.TrackID) {
		return errResp(http.StatusBadRequest, "trackId must be digits")
	}
	n := NowPlaying{TrackID: b.TrackID, Title: clip(b.Title, maxTitle), Artists: []string{}, Playing: b.Playing}
	for _, a := range b.Artists {
		if len(n.Artists) == maxArtists {
			break
		}
		if a != "" {
			n.Artists = append(n.Artists, clip(a, maxArtist))
		}
	}
	if coverRe.MatchString(b.Cover) {
		n.Cover = b.Cover
	}
	if b.DurationMs > 0 && b.DurationMs <= maxDurationMs {
		n.DurationMs = b.DurationMs
	}
	n.PositionMs = b.PositionMs
	if n.PositionMs < 0 {
		n.PositionMs = 0
	}
	if n.DurationMs > 0 && n.PositionMs > n.DurationMs {
		n.PositionMs = n.DurationMs
	}
	p.cur, p.at = &n, p.Now()
	return Response{http.StatusOK, map[string]any{"ok": true}}
}

// Get — текущее состояние, позиция досчитана до «сейчас». nil, если ничего не играет или аддон давно молчит.
func (p *Player) Get() *NowPlaying {
	if p.cur == nil {
		return nil
	}
	age := p.Now().Sub(p.at)
	if age > staleAfter {
		return nil
	}
	n := *p.cur
	if n.Playing && age > 0 {
		n.PositionMs += age.Milliseconds()
		if n.DurationMs > 0 && n.PositionMs > n.DurationMs {
			n.PositionMs = n.DurationMs
		}
	}
	return &n
}

// withNow добавляет в ответ /queue поле now (null, если ничего не играет).
func withNow(r Response, now *NowPlaying) Response {
	if m, ok := r.Body.(map[string]any); ok && r.Status == http.StatusOK {
		m["now"] = now
	}
	return r
}

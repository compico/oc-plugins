package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/compico/overlaycomposer/pkg/ocplugin"
)

// Команда !np: на сообщение зрителя отвечает ссылкой на карту osu!, которая сейчас выбрана или играется (данные — tosu /json/v2).
// Работает на сервере OC (чат приходит событием chat.command), открытый оверлей не нужен.

const (
	// StatePath — раздел tosu с текущим состоянием; хост пускает плагин только в MapsPath и StatePath (Meta.NetLocal.Paths).
	StatePath = "/json/"

	npFetchID    = "np"
	npMaxLen     = 450           // лимит чата Twitch — 500; запас на имя и служебные символы
	npDedupMark  = " \U000E0000" // невидимый символ: Twitch не режет повтор того же текста подряд
	npDedupTTL   = 35 * time.Second
	npStaleAfter = 30 * time.Second // ответа на запрос так и нет: разрешаем новый

	defaultTemplate    = "{artist} - {title} [{version}] {url}"
	defaultUnsubmitted = "This map is not on osu! (unsubmitted or local)"
	defaultOffline     = "Can't read the current map right now (tosu is not running)"
	defaultCooldown    = 15
)

// NPConfig — настройки команды. Все поля строковые: так их отдаёт форма настроек плагина.
type NPConfig struct {
	Who             string `json:"who"`             // everyone | sub | vip | mod
	CooldownSeconds string `json:"cooldownSeconds"` // общая пауза между ответами
	ReplyToUser     string `json:"replyToUser"`     // true | false: ответ реплаем на сообщение
	Template        string `json:"template"`
	UnsubmittedText string `json:"unsubmittedText"`
	OfflineText     string `json:"offlineText"`
}

type npSettings struct {
	who      string
	cooldown time.Duration
	reply    bool
	template string
	unsub    string
	offline  string
}

func (c NPConfig) settings() npSettings {
	s := npSettings{who: "everyone", cooldown: defaultCooldown * time.Second, reply: true,
		template: defaultTemplate, unsub: defaultUnsubmitted, offline: defaultOffline}
	switch w := strings.ToLower(strings.TrimSpace(c.Who)); w {
	case "everyone", "sub", "vip", "mod":
		s.who = w
	}
	if v, err := strconv.ParseFloat(strings.TrimSpace(c.CooldownSeconds), 64); err == nil && v >= 0 && v <= 600 {
		s.cooldown = time.Duration(v * float64(time.Second))
	}
	if b, err := strconv.ParseBool(strings.TrimSpace(c.ReplyToUser)); err == nil {
		s.reply = b
	}
	if strings.TrimSpace(c.Template) != "" {
		s.template = c.Template
	}
	if strings.TrimSpace(c.UnsubmittedText) != "" {
		s.unsub = c.UnsubmittedText
	}
	if strings.TrimSpace(c.OfflineText) != "" {
		s.offline = c.OfflineText
	}
	return s
}

// allowed — кто может пользоваться командой. Каждая следующая роль включает предыдущие; стример и модераторы — всегда.
func allowed(c ocplugin.ChatCommand, who string) bool {
	top := c.IsBroadcaster || c.IsMod
	switch who {
	case "mod":
		return top
	case "vip":
		return top || c.IsVip
	case "sub":
		return top || c.IsVip || c.IsSub
	}
	return true
}

var tmplRe = regexp.MustCompile(`\{(\w+)\}`)
var spaceRe = regexp.MustCompile(`\s+`)

// render подставляет «{name}»; неизвестные подстановки становятся пустой строкой.
func render(template string, vars map[string]string) string {
	out := tmplRe.ReplaceAllStringFunc(template, func(m string) string { return vars[m[1:len(m)-1]] })
	return strings.TrimSpace(spaceRe.ReplaceAllString(out, " "))
}

// tosuState — нужная часть ответа tosu /json/v2.
type tosuState struct {
	Beatmap *struct {
		Artist  string `json:"artist"`
		Title   string `json:"title"`
		Version string `json:"version"`
		Mapper  string `json:"mapper"`
		ID      int    `json:"id"`
		Set     int    `json:"set"`
		Stats   struct {
			Stars struct {
				Total float64 `json:"total"`
			} `json:"stars"`
		} `json:"stats"`
	} `json:"beatmap"`
}

type npKind int

const (
	npNone        npKind = iota // карты нет: tosu не запущен или ничего не выбрано
	npOK                        // карта есть на сайте
	npUnsubmitted               // id = 0: локальная или не загруженная карта
)

func describe(body string) (npKind, map[string]string) {
	var st tosuState
	if json.Unmarshal([]byte(body), &st) != nil || st.Beatmap == nil {
		return npNone, nil
	}
	b := st.Beatmap
	v := map[string]string{"artist": b.Artist, "title": b.Title, "version": b.Version, "mapper": b.Mapper}
	if s := b.Stats.Stars.Total; s > 0 {
		v["stars"] = fmt.Sprintf("%.2f", s)
	}
	if b.ID > 0 {
		v["id"] = strconv.Itoa(b.ID)
		v["url"] = "https://osu.ppy.sh/beatmaps/" + v["id"]
	}
	if b.Set > 0 {
		v["set"] = strconv.Itoa(b.Set)
	}
	switch {
	case b.ID > 0:
		return npOK, v
	case b.Title != "" || b.Artist != "":
		return npUnsubmitted, v
	}
	return npNone, nil
}

// replyText — текст ответа по ответу tosu (status 0 или не 200 — tosu недоступен).
func replyText(s npSettings, status int, body, user string) string {
	if status != 200 {
		return s.offline
	}
	kind, vars := describe(body)
	switch kind {
	case npNone:
		return s.offline
	case npUnsubmitted:
		vars["user"] = user
		return render(s.unsub, vars)
	}
	vars["user"] = user
	text := render(s.template, vars)
	if r := []rune(text); len(r) > npMaxLen {
		text = string(r[:npMaxLen-1]) + "…"
	}
	return text
}

// dedupe: тот же текст повторно в окне дедупликации Twitch — добавляем невидимый символ.
func dedupe(text, lastText string, lastAt, now time.Time) string {
	if lastText != "" && strings.TrimSuffix(lastText, npDedupMark) == text && now.Sub(lastAt) < npDedupTTL {
		if strings.HasSuffix(lastText, npDedupMark) {
			return text
		}
		return text + npDedupMark
	}
	return text
}

// NP — состояние команды: пауза между ответами, запрос к tosu в полёте, последний отправленный текст.
type NP struct {
	Cfg   NPConfig
	Tosu  string
	Fetch func(id, url string) error
	Say   func(replyTo, text string) error
	Now   func() time.Time

	lastUse time.Time
	pending *npJob
	lastMsg string
	lastAt  time.Time
}

type npJob struct {
	user, messageID string
	at              time.Time
}

// OnCommand — зритель написал «!np». Права и паузу проверяет плагин: хост доставляет любую объявленную команду.
func (n *NP) OnCommand(c ocplugin.ChatCommand) {
	s := n.Cfg.settings()
	now := n.Now()
	if !allowed(c, s.who) {
		return
	}
	if !n.lastUse.IsZero() && now.Sub(n.lastUse) < s.cooldown {
		return
	}
	if n.pending != nil && now.Sub(n.pending.at) < npStaleAfter {
		return // предыдущий запрос к tosu ещё не вернулся
	}
	n.lastUse = now
	n.pending = &npJob{user: c.DisplayName, messageID: c.MessageID, at: now}
	if n.pending.user == "" {
		n.pending.user = c.User
	}
	if err := n.Fetch(npFetchID, "http://"+n.Tosu+"/json/v2"); err != nil {
		ocplugin.Warn("!np: запрос к tosu не ушёл: " + err.Error())
		n.OnFetched(0, "", err.Error())
	}
}

// OnFetched — ответ tosu на запрос, начатый OnCommand.
func (n *NP) OnFetched(status int, body, fetchErr string) {
	job := n.pending
	n.pending = nil
	if job == nil {
		return
	}
	if fetchErr != "" {
		status = 0
	}
	s := n.Cfg.settings()
	text := replyText(s, status, body, job.user)
	if text == "" {
		return
	}
	now := n.Now()
	text = dedupe(text, n.lastMsg, n.lastAt, now)
	if utf8.RuneCountInString(text) > 500 {
		return
	}
	var err error
	if s.reply && job.messageID != "" {
		err = n.Say(job.messageID, text)
	} else {
		err = n.Say("", text)
	}
	if err != nil {
		ocplugin.Warn("!np: ответ не отправлен: " + err.Error())
		return
	}
	n.lastMsg, n.lastAt = text, now
}

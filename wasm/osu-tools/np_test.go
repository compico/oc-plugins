package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/compico/overlaycomposer/pkg/ocplugin"
)

const tosuOK = `{"beatmap":{"artist":"Artist","title":"Title","version":"Insane","mapper":"M","id":123,"set":45,"stats":{"stars":{"total":5.678}}}}`

type sent struct{ replyTo, text string }

type npEnv struct {
	np    *NP
	now   time.Time
	urls  []string
	said  []sent
	fetch error
}

func newEnv(cfg NPConfig) *npEnv {
	e := &npEnv{now: time.Unix(1000, 0)}
	e.np = &NP{Cfg: cfg, Tosu: "127.0.0.1:24050", Now: func() time.Time { return e.now },
		Fetch: func(id, url string) error { e.urls = append(e.urls, id+" "+url); return e.fetch },
		Say:   func(r, t string) error { e.said = append(e.said, sent{r, t}); return nil }}
	return e
}

func cmd(user string) ocplugin.ChatCommand {
	return ocplugin.ChatCommand{Command: "np", DisplayName: user, User: strings.ToLower(user), MessageID: "m-" + user}
}

func TestNPReplyDefault(t *testing.T) {
	e := newEnv(NPConfig{})
	e.np.OnCommand(cmd("Viewer"))
	if len(e.urls) != 1 || e.urls[0] != "np http://127.0.0.1:24050/json/v2" {
		t.Fatalf("запрос: %v", e.urls)
	}
	e.np.OnFetched(200, tosuOK, "")
	want := sent{"m-Viewer", "Artist - Title [Insane] https://osu.ppy.sh/beatmaps/123"}
	if len(e.said) != 1 || e.said[0] != want {
		t.Fatalf("ответ: %+v", e.said)
	}
}

func TestNPTemplateAndNoReply(t *testing.T) {
	e := newEnv(NPConfig{Template: "@{user}: {stars}* {mapper} {set}/{id} {nope}", ReplyToUser: "false"})
	e.np.OnCommand(cmd("Viewer"))
	e.np.OnFetched(200, tosuOK, "")
	if got := e.said[0]; got.replyTo != "" || got.text != "@Viewer: 5.68* M 45/123" {
		t.Fatalf("%+v", got)
	}
}

func TestNPCases(t *testing.T) {
	for name, c := range map[string]struct {
		status int
		body   string
		err    string
		want   string
	}{
		"unsubmitted": {200, `{"beatmap":{"artist":"A","title":"T","id":0}}`, "", defaultUnsubmitted},
		"no beatmap":  {200, `{}`, "", defaultOffline},
		"empty map":   {200, `{"beatmap":{"id":0}}`, "", defaultOffline},
		"bad json":    {200, `nope`, "", defaultOffline},
		"status":      {502, tosuOK, "", defaultOffline},
		"fetch error": {0, "", "connection refused", defaultOffline},
	} {
		e := newEnv(NPConfig{})
		e.np.OnCommand(cmd("v"))
		e.np.OnFetched(c.status, c.body, c.err)
		if len(e.said) != 1 || e.said[0].text != c.want {
			t.Errorf("%s: %+v", name, e.said)
		}
	}
	e := newEnv(NPConfig{}) // Fetch вернул ошибку сразу: отвечаем «недоступно», зависшего запроса нет
	e.fetch = errors.New("denied")
	e.np.OnCommand(cmd("v"))
	if len(e.said) != 1 || e.said[0].text != defaultOffline || e.np.pending != nil {
		t.Fatalf("%+v pending=%v", e.said, e.np.pending)
	}
}

func TestNPCooldownAndPending(t *testing.T) {
	e := newEnv(NPConfig{CooldownSeconds: "10"})
	e.np.OnCommand(cmd("a"))
	e.np.OnCommand(cmd("b")) // пауза и запрос в полёте
	if len(e.urls) != 1 {
		t.Fatalf("запросов %d", len(e.urls))
	}
	e.np.OnFetched(200, tosuOK, "")
	e.now = e.now.Add(5 * time.Second)
	e.np.OnCommand(cmd("c")) // пауза ещё идёт
	if len(e.urls) != 1 {
		t.Fatal("в паузе команда не должна запускать запрос")
	}
	e.now = e.now.Add(6 * time.Second)
	e.np.OnCommand(cmd("d"))
	e.np.OnFetched(200, tosuOK, "")
	if len(e.urls) != 2 || len(e.said) != 2 {
		t.Fatalf("urls=%d said=%d", len(e.urls), len(e.said))
	}
	// повтор того же текста в окне Twitch: добавляется невидимый символ, иначе сообщение съедят
	if e.said[1].text != e.said[0].text+npDedupMark {
		t.Fatalf("dedupe: %q", e.said[1].text)
	}
	// зависший запрос не блокирует навсегда
	e.now = e.now.Add(time.Minute)
	e.np.OnCommand(cmd("e"))
	e.now = e.now.Add(npStaleAfter + time.Minute)
	e.np.OnCommand(cmd("f"))
	if len(e.urls) != 4 {
		t.Fatalf("urls=%d", len(e.urls))
	}
	e.np.OnFetched(200, tosuOK, "") // лишний ответ без запроса игнорируется
	e.np.OnFetched(200, tosuOK, "")
}

func TestNPWho(t *testing.T) {
	roles := map[string]ocplugin.ChatCommand{
		"viewer": {}, "sub": {IsSub: true}, "vip": {IsVip: true}, "mod": {IsMod: true}, "broadcaster": {IsBroadcaster: true},
	}
	want := map[string]map[string]bool{
		"everyone": {"viewer": true, "sub": true, "vip": true, "mod": true, "broadcaster": true},
		"sub":      {"viewer": false, "sub": true, "vip": true, "mod": true, "broadcaster": true},
		"vip":      {"viewer": false, "sub": false, "vip": true, "mod": true, "broadcaster": true},
		"mod":      {"viewer": false, "sub": false, "vip": false, "mod": true, "broadcaster": true},
		"junk":     {"viewer": true}, // неизвестное значение — как everyone
	}
	for who, m := range want {
		for role, ok := range m {
			if got := allowed(roles[role], NPConfig{Who: who}.settings().who); got != ok {
				t.Errorf("who=%s role=%s: %v", who, role, got)
			}
		}
	}
}

func TestNPSettingsDefaultsAndLimits(t *testing.T) {
	s := NPConfig{CooldownSeconds: "-1", ReplyToUser: "maybe", Template: "  "}.settings()
	if s.cooldown != 15*time.Second || !s.reply || s.template != defaultTemplate {
		t.Fatalf("%+v", s)
	}
	if (NPConfig{CooldownSeconds: "0"}).settings().cooldown != 0 || (NPConfig{CooldownSeconds: "601"}).settings().cooldown != 15*time.Second {
		t.Fatal("пауза: 0 допустим, больше 600 — по умолчанию")
	}
}

func TestNPLongTextTruncated(t *testing.T) {
	long := strings.Repeat("я", 600)
	s := NPConfig{}.settings()
	got := replyText(s, 200, `{"beatmap":{"artist":"`+long+`","title":"t","id":1}}`, "u")
	if r := []rune(got); len(r) != npMaxLen || r[len(r)-1] != '…' {
		t.Fatalf("длина %d", len(r))
	}
}

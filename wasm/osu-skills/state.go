package main

import (
	"encoding/json"
	"net/http"
	"time"
)

const (
	LivePath     = "/json/v2" // состояние tosu; плагин читает его сам, потому что из sandbox-iframe (Origin null) tosu не пускает websocket
	liveID       = "live"
	liveInterval = 200 * time.Millisecond // чаще tosu не опрашиваем, сколько бы раз ни спросил виджет
	liveStale    = 3 * time.Second        // старше этого состояние считается устаревшим (tosu не отвечает)
)

// State — что виджету нужно знать о текущей карте.
type State struct {
	File     string `json:"file"`     // directPath.beatmapFile: «папка\файл.osu» относительно Songs
	Checksum string `json:"checksum"` // beatmap.checksum, если tosu его отдаёт
	Mods     string `json:"mods"`     // play.mods.name, например «HDDT»
	Time     int    `json:"time"`     // beatmap.time.live, мс
}

// tosuState — нужные поля ответа /json/v2.
type tosuState struct {
	DirectPath struct {
		BeatmapFile string `json:"beatmapFile"`
	} `json:"directPath"`
	Beatmap struct {
		Checksum string `json:"checksum"`
		Time     struct {
			Live float64 `json:"live"`
		} `json:"time"`
	} `json:"beatmap"`
	Play struct {
		Mods struct {
			Name string `json:"name"`
		} `json:"mods"`
	} `json:"play"`
}

type live struct {
	state    State
	err      string
	at       time.Time // когда пришёл последний ответ
	inflight bool
	asked    time.Time // когда ушёл последний запрос
	ok       bool
}

// State отвечает на опрос виджета последним известным состоянием и при необходимости запрашивает свежее у tosu.
func (s *Service) State() Reply {
	now := s.Now()
	if !s.live.inflight && now.Sub(s.live.asked) >= liveInterval {
		s.live.asked = now
		if err := s.Fetch(liveID, "http://"+s.Tosu+LivePath); err != nil {
			s.live.ok, s.live.err, s.live.at = false, "cannot reach tosu (no net.local permission?)", now
		} else {
			s.live.inflight = true
		}
	}
	switch {
	case s.live.at.IsZero():
		return Reply{http.StatusAccepted, map[string]string{"status": "pending"}}
	case !s.live.ok:
		return errReply(http.StatusBadGateway, s.live.err)
	case now.Sub(s.live.at) > liveStale:
		return errReply(http.StatusBadGateway, "cannot reach tosu: is it running?")
	}
	return Reply{http.StatusOK, s.live.state}
}

// onLive принимает ответ tosu на опрос состояния.
func (s *Service) onLive(status int, body, fetchErr string) {
	s.live.inflight = false
	s.live.at = s.Now()
	fail := func(msg string) { s.live.ok, s.live.err = false, msg }
	if fetchErr != "" {
		fail("cannot reach tosu: is it running?")
		return
	}
	if status != http.StatusOK {
		fail("tosu answered HTTP " + http.StatusText(status))
		return
	}
	var t tosuState
	if err := json.Unmarshal([]byte(body), &t); err != nil {
		fail("cannot read the tosu answer")
		return
	}
	s.live.ok, s.live.err = true, ""
	s.live.state = State{File: t.DirectPath.BeatmapFile, Checksum: t.Beatmap.Checksum, Mods: t.Play.Mods.Name, Time: int(t.Beatmap.Time.Live)}
}

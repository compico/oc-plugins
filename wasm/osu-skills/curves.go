package main

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/compico/go-osu/pkg/osu"
	"github.com/compico/go-osu/pkg/osu/beatmap"
	"github.com/compico/go-osu/pkg/osu/mods"
	"github.com/compico/go-osu/pkg/osu/skills"
)

const (
	defaultWindow = 5000  // мс карты на одну точку графика (как у ручки go-osu)
	maxWindow     = 60000 // как у ручки go-osu
	maxMapBytes   = 1 << 20
	failTTL       = 30 * time.Second // сколько помнить неудачную загрузку карты, чтобы не бомбить источник опросом виджета
	resultsCap    = 16
	mapsCap       = 4

	// DefaultSource — откуда берётся файл .osu; {id} заменяется ID сложности (beatmap_id).
	DefaultSource = "https://osu.ppy.sh/osu/{id}"
)

// Request — тело POST /curves. Виджет повторяет его, пока ответ 202 (карта скачивается).
type Request struct {
	ID     int64  `json:"id"`     // beatmap_id (ID сложности)
	Mods   string `json:"mods"`   // строка вида "HDDT"; пусто — без модов
	Window int    `json:"window"` // 0 — по умолчанию (5000)
}

// Response — то же, что отдаёт ручка go-osu GET /api/osu/songs/{id}/strains.
type Response struct {
	BeatmapID int64   `json:"beatmap_id"`
	Mods      string  `json:"mods"`
	Speed     float64 `json:"speed"`
	Window    int     `json:"window"`

	Stamina   []skills.StrainPoint `json:"stamina"`
	Tenacity  []skills.StrainPoint `json:"tenacity"`
	Agility   []skills.StrainPoint `json:"agility"`
	Precision []skills.StrainPoint `json:"precision"`
	Reading   []skills.StrainPoint `json:"reading"`
	Memory    []skills.StrainPoint `json:"memory"`
	Accuracy  []skills.StrainPoint `json:"accuracy"`
	Reaction  []skills.StrainPoint `json:"reaction"`
}

// Reply — ответ маршрута: статус и тело (Response или {"status":"pending"} / {"error":…}).
type Reply struct {
	Status int
	Body   any
}

type job struct {
	fetchID string
	failed  bool
	err     string
	at      time.Time
}

// Service считает кривые навыков. Файл карты скачивает хост (Fetch), поэтому ответ приходит позже: пока карта не
// скачана, маршрут отвечает 202, а виджет повторяет запрос. Всё состояние — кэш: потеряется вместе с экземпляром,
// тогда карта просто скачается заново.
type Service struct {
	Source string
	Fetch  func(id, url string) error
	Now    func() time.Time

	jobs    map[int64]*job
	maps    lru[int64, *osu.Beatmap]
	results lru[string, Response]
}

func NewService(source string, fetch func(id, url string) error, now func() time.Time) *Service {
	return &Service{Source: source, Fetch: fetch, Now: now, jobs: map[int64]*job{},
		maps: newLRU[int64, *osu.Beatmap](mapsCap), results: newLRU[string, Response](resultsCap)}
}

// ValidSource — шаблон ссылки: https и {id}.
func ValidSource(s string) bool {
	return strings.HasPrefix(s, "https://") && strings.Count(s, "{id}") == 1 && !strings.ContainsAny(s, " \t\r\n")
}

func fetchID(id int64) string { return fmt.Sprintf("map:%d", id) }

// Curves отвечает на запрос виджета.
func (s *Service) Curves(req Request) Reply {
	if req.ID <= 0 || req.ID > 1<<31-1 {
		return errReply(http.StatusBadRequest, "invalid beatmap id")
	}
	m, err := mods.Parse(req.Mods)
	if err != nil {
		return errReply(http.StatusBadRequest, "invalid mods")
	}
	window := req.Window
	if window == 0 {
		window = defaultWindow
	}
	if window < 0 || window > maxWindow {
		return errReply(http.StatusBadRequest, "window must be between 0 and 60000 ms")
	}

	key := fmt.Sprintf("%d:%d:%d", req.ID, m, window)
	if r, ok := s.results.get(key); ok {
		return Reply{http.StatusOK, r}
	}
	if bm, ok := s.maps.get(req.ID); ok {
		r := compute(req.ID, bm, m, window)
		s.results.put(key, r)
		return Reply{http.StatusOK, r}
	}

	j := s.jobs[req.ID]
	switch {
	case j != nil && !j.failed:
		return Reply{http.StatusAccepted, map[string]string{"status": "pending"}}
	case j != nil && s.Now().Sub(j.at) < failTTL:
		return errReply(http.StatusBadGateway, j.err)
	}
	j = &job{fetchID: fetchID(req.ID), at: s.Now()}
	if err := s.Fetch(j.fetchID, strings.Replace(s.Source, "{id}", fmt.Sprint(req.ID), 1)); err != nil {
		return errReply(http.StatusBadGateway, "cannot download the beatmap (no net.http permission?)")
	}
	s.jobs[req.ID] = j
	return Reply{http.StatusAccepted, map[string]string{"status": "pending"}}
}

// OnFetched принимает ответ хоста на Fetch(fetchID): разбирает .osu и кладёт карту в кэш.
func (s *Service) OnFetched(id int64, status int, body, fetchErr string) {
	j := s.jobs[id]
	if j == nil {
		return
	}
	fail := func(msg string) { j.failed, j.err, j.at = true, msg, s.Now() }
	switch {
	case fetchErr != "":
		fail("cannot download the beatmap")
	case status == http.StatusNotFound:
		fail("beatmap not found at the source")
	case status != http.StatusOK:
		fail(fmt.Sprintf("the source answered HTTP %d", status))
	case len(body) >= maxMapBytes:
		fail("the beatmap file is too large (over 1 MiB)")
	default:
		var bm osu.Beatmap
		if err := beatmap.NewDecoder([]byte(body)).Decode(&bm); err != nil || len(bm.HitObjects) == 0 {
			fail("cannot read the beatmap file")
			return
		}
		s.maps.put(id, &bm)
		delete(s.jobs, id)
	}
}

func compute(id int64, bm *osu.Beatmap, m osu.Mod, window int) Response {
	c := skills.ProcessCurves(bm, m, skills.NewVars())
	windows := skills.WindowCount(window, c.Speed, c.Stamina, c.Tenacity, c.Agility, c.Precision, c.Reading, c.Memory, c.Accuracy, c.Reaction)
	rs := func(points []skills.StrainPoint, carry bool) []skills.StrainPoint {
		if len(points) == 0 { // кривой нет у этой карты (память без FL) — пустой список, а не нули
			return []skills.StrainPoint{}
		}
		return skills.ResampleMax(points, window, c.Speed, windows, carry)
	}
	return Response{
		BeatmapID: id, Mods: c.ModsString, Speed: c.Speed, Window: window,
		Stamina: rs(c.Stamina, false), Tenacity: rs(c.Tenacity, false), Agility: rs(c.Agility, false),
		Precision: rs(c.Precision, false), Reading: rs(c.Reading, false), Memory: rs(c.Memory, true),
		Accuracy: rs(c.Accuracy, false), Reaction: rs(c.Reaction, false),
	}
}

func errReply(status int, msg string) Reply { return Reply{status, map[string]string{"error": msg}} }

// lru — крошечный кэш с вытеснением самого старого.
type lru[K comparable, V any] struct {
	cap   int
	order []K
	m     map[K]V
}

func newLRU[K comparable, V any](cap int) lru[K, V] { return lru[K, V]{cap: cap, m: map[K]V{}} }

func (l *lru[K, V]) get(k K) (V, bool) { v, ok := l.m[k]; return v, ok }

func (l *lru[K, V]) put(k K, v V) {
	if _, ok := l.m[k]; !ok {
		l.order = append(l.order, k)
		if len(l.order) > l.cap {
			delete(l.m, l.order[0])
			l.order = l.order[1:]
		}
	}
	l.m[k] = v
}

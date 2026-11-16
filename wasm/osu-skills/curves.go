package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/compico/go-osu/pkg/osu"
	"github.com/compico/go-osu/pkg/osu/beatmap"
	"github.com/compico/go-osu/pkg/osu/mods"
	"github.com/compico/go-osu/pkg/osu/skills"
)

const (
	defaultWindow = 5000             // мс карты на одну точку графика (как у ручки go-osu)
	maxWindow     = 60000            // как у ручки go-osu
	minWindow     = 50               // мельче не нужно: точек больше, чем объектов
	maxMapBytes   = 8 << 20          // как лимит ответа хоста для net.local
	failTTL       = 30 * time.Second // сколько помнить неудачную загрузку карты, чтобы не бомбить источник опросом виджета
	resultsCap    = 16
	mapsCap       = 4

	// DefaultTosu — где tosu отдаёт файлы (адрес можно заменить настройкой tosuHost).
	DefaultTosu = "127.0.0.1:24050"
	// MapsPath — раздел tosu с файлами карт; хост пускает плагин только сюда (Meta.NetLocal.Paths).
	MapsPath = "/files/beatmap/"
)

// Request — тело POST /curves. Виджет повторяет его, пока ответ 202 (карта скачивается).
type Request struct {
	File     string `json:"file"`     // directPath.beatmapFile из tosu: «папка\файл.osu» относительно Songs
	Checksum string `json:"checksum"` // md5 карты из tosu (необязательно): правка карты не отдаёт устаревший кэш
	Mods     string `json:"mods"`     // строка вида "HDDT"; пусто — без модов
	Window   int    `json:"window"`   // 0 — по умолчанию (5000)
}

// Response — то же, что отдаёт ручка go-osu GET /api/osu/songs/{id}/strains.
type Response struct {
	Mods   string  `json:"mods"`
	Speed  float64 `json:"speed"`
	Window int     `json:"window"`

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
	key    string
	failed bool
	err    string
	at     time.Time
}

// Service считает кривые навыков. Файл карты скачивает хост (Fetch), поэтому ответ приходит позже: пока карта не
// скачана, маршрут отвечает 202, а виджет повторяет запрос. Всё состояние — кэш: потеряется вместе с экземпляром,
// тогда карта просто скачается заново.
type Service struct {
	Tosu  string // host:port, где tosu отдаёт файлы
	Fetch func(id, url string) error
	Now   func() time.Time

	jobs    map[string]*job // по ключу карты
	byFetch map[string]string
	maps    lru[string, *osu.Beatmap]
	results lru[string, Response]
}

func NewService(tosu string, fetch func(id, url string) error, now func() time.Time) *Service {
	return &Service{Tosu: tosu, Fetch: fetch, Now: now, jobs: map[string]*job{}, byFetch: map[string]string{},
		maps: newLRU[string, *osu.Beatmap](mapsCap), results: newLRU[string, Response](resultsCap)}
}

var hostPortRe = regexp.MustCompile(`^(127\.0\.0\.1|localhost|\[::1\]):[0-9]{1,5}$`)

// ValidTosu — адрес tosu из настройки: host:port на этом компьютере (хост всё равно пускает только объявленный адрес).
func ValidTosu(s string) bool { return hostPortRe.MatchString(s) }

var checksumRe = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)

// lazerPathRe — путь к карте в хранилище osu!lazer: хэш файла без расширения, «8\8f\8f50d5…» (первая буква, две, весь SHA-256).
var lazerPathRe = regexp.MustCompile(`^[0-9a-f]{1,2}[/\\][0-9a-f]{2}[/\\][0-9a-f]{64}$`)

// mapURL строит адрес файла у tosu из пути вида «папка\файл.osu» (stable) или «8\8f\<хэш>» (lazer); разделители Windows тоже принимаются.
// Путь проверяется здесь, а не только хостом: виджет — чужой код, он не должен выводить плагин за раздел с картами.
func (s *Service) mapURL(file string) (string, bool) {
	osuFile := strings.HasSuffix(strings.ToLower(file), ".osu")
	if file == "" || len(file) > 512 || !(osuFile || lazerPathRe.MatchString(file)) {
		return "", false
	}
	var segs []string
	for _, seg := range strings.FieldsFunc(file, func(r rune) bool { return r == '/' || r == '\\' }) {
		if seg == "." || seg == ".." || strings.ContainsAny(seg, ":*?\"<>|\x00") || strings.ContainsFunc(seg, func(r rune) bool { return r < 0x20 }) {
			return "", false
		}
		segs = append(segs, url.PathEscape(seg))
	}
	if n := len(segs); n == 0 || (osuFile && strings.EqualFold(segs[n-1], ".osu")) {
		return "", false // имя файла не может состоять только из расширения
	}
	if len(segs) == 0 || strings.HasPrefix(file, "/") || strings.HasPrefix(file, "\\") {
		return "", false
	}
	return "http://" + s.Tosu + MapsPath + strings.Join(segs, "/"), true
}

// mapKey — ключ карты в кэше: путь и (если есть) контрольная сумма.
func mapKey(r Request) string { return strings.ToLower(r.Checksum) + "|" + r.File }

func fetchID(key string) string {
	h := sha256.Sum256([]byte(key))
	return "map:" + hex.EncodeToString(h[:8])
}

// Curves отвечает на запрос виджета.
func (s *Service) Curves(req Request) Reply {
	if _, ok := s.mapURL(req.File); !ok {
		return errReply(http.StatusBadRequest, "invalid beatmap file path")
	}
	if req.Checksum != "" && !checksumRe.MatchString(req.Checksum) {
		return errReply(http.StatusBadRequest, "invalid checksum")
	}
	m, err := mods.Parse(req.Mods)
	if err != nil {
		return errReply(http.StatusBadRequest, "invalid mods")
	}
	window := req.Window
	if window == 0 {
		window = defaultWindow
	}
	if window < 0 || window > maxWindow || (window != 0 && window < minWindow) {
		return errReply(http.StatusBadRequest, "window must be between 50 and 60000 ms")
	}

	mk := mapKey(req)
	key := fmt.Sprintf("%s|%d|%d", mk, m, window)
	if r, ok := s.results.get(key); ok {
		return Reply{http.StatusOK, r}
	}
	if bm, ok := s.maps.get(mk); ok {
		r := compute(bm, m, window)
		s.results.put(key, r)
		return Reply{http.StatusOK, r}
	}

	j := s.jobs[mk]
	switch {
	case j != nil && !j.failed:
		return Reply{http.StatusAccepted, map[string]string{"status": "pending"}}
	case j != nil && s.Now().Sub(j.at) < failTTL:
		return errReply(http.StatusBadGateway, j.err)
	}
	u, _ := s.mapURL(req.File)
	fid := fetchID(mk)
	if err := s.Fetch(fid, u); err != nil {
		return errReply(http.StatusBadGateway, "cannot reach tosu (no net.local permission?)")
	}
	s.jobs[mk] = &job{key: mk, at: s.Now()}
	s.byFetch[fid] = mk
	return Reply{http.StatusAccepted, map[string]string{"status": "pending"}}
}

// OnFetched принимает ответ хоста на Fetch(fid): разбирает .osu и кладёт карту в кэш.
func (s *Service) OnFetched(fid string, status int, body, fetchErr string) {
	key := s.byFetch[fid]
	j := s.jobs[key]
	if j == nil {
		return
	}
	fail := func(msg string) { j.failed, j.err, j.at = true, msg, s.Now() }
	switch {
	case fetchErr != "":
		fail("cannot reach tosu: is it running?")
	case status == http.StatusNotFound:
		fail("tosu has no such beatmap file (is the Songs folder set in tosu?)")
	case status != http.StatusOK:
		fail(fmt.Sprintf("tosu answered HTTP %d", status))
	case len(body) >= maxMapBytes:
		fail("the beatmap file is too large (over 8 MiB)")
	default:
		var bm osu.Beatmap
		if err := beatmap.NewDecoder([]byte(body)).Decode(&bm); err != nil || len(bm.HitObjects) == 0 {
			fail("cannot read the beatmap file")
			return
		}
		s.maps.put(key, &bm)
		delete(s.jobs, key)
		delete(s.byFetch, fid)
	}
}

// maxPoints — сколько точек (по всем навыкам) помещается в ответ: маршрут отдаёт JSON до 1 МиБ, точка весит около 32 байт.
var maxPoints = 28000

func (r Response) points() int {
	return len(r.Stamina) + len(r.Tenacity) + len(r.Agility) + len(r.Precision) + len(r.Reading) + len(r.Memory) + len(r.Accuracy) + len(r.Reaction)
}

// compute считает кривые с окном window. Если точек слишком много для ответа (мелкое окно на очень длинной карте),
// окно удваивается, пока ответ не поместится; в ответе Window — фактическое.
func compute(bm *osu.Beatmap, m osu.Mod, window int) Response {
	c := skills.ProcessCurves(bm, m, skills.NewVars())
	for {
		r := resample(c, window)
		if r.points() <= maxPoints || window >= maxWindow {
			return r
		}
		window = min(window*2, maxWindow)
	}
}

func resample(c *skills.Curves, window int) Response {
	windows := skills.WindowCount(window, c.Speed, c.Stamina, c.Tenacity, c.Agility, c.Precision, c.Reading, c.Memory, c.Accuracy, c.Reaction)
	rs := func(points []skills.StrainPoint, carry bool) []skills.StrainPoint {
		if len(points) == 0 { // кривой нет у этой карты (память без FL) — пустой список, а не нули
			return []skills.StrainPoint{}
		}
		return skills.ResampleMax(points, window, c.Speed, windows, carry)
	}
	return Response{
		Mods: c.ModsString, Speed: c.Speed, Window: window,
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

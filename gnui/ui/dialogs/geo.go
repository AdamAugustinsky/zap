package dialogs

import (
	"math"
	"net/url"
	"strconv"
	"strings"
)

// The location dialog takes the spot as text: a pair of coordinates, or a
// link to a spot on a map (zf:src/geo.rs). Everything here is offline
// parsing, and anything it cannot read with confidence it refuses, so Send
// stays dark instead of pinning the wrong place.

// Spot is a place on the earth, in degrees.
type Spot struct{ Latitude, Longitude float64 }

// Text writes the coordinates with dot decimals and five places (about a
// metre), which is how coordinates are written everywhere, links included.
func (s Spot) Text() string {
	return strconv.FormatFloat(s.Latitude, 'f', 5, 64) + ", " + strconv.FormatFloat(s.Longitude, 'f', 5, 64)
}

func (s Spot) onEarth() bool {
	return !math.IsNaN(s.Latitude) && !math.IsNaN(s.Longitude) && !math.IsInf(s.Latitude, 0) && !math.IsInf(s.Longitude, 0) &&
		s.Latitude >= -90 && s.Latitude <= 90 && s.Longitude >= -180 && s.Longitude <= 180
}

// ParseSpot reads the spot in text, if there is one.
func ParseSpot(text string) (Spot, bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return Spot{}, false
	}
	sp, ok := linkSpot(text)
	if !ok {
		sp, ok = pairSpot(text)
	}
	if !ok || !sp.onEarth() {
		return Spot{}, false
	}
	return sp, true
}

// MapURL is the link to open for a spot; the browser fetches it, never
// ZapFast, so no map is ever loaded in the app.
func MapURL(sp Spot) string {
	la := strconv.FormatFloat(sp.Latitude, 'f', -1, 64)
	lo := strconv.FormatFloat(sp.Longitude, 'f', -1, 64)
	return "https://www.openstreetmap.org/?mlat=" + la + "&mlon=" + lo + "#map=16/" + la + "/" + lo
}

// IsShortLink reports whether text is a shortened map link, which names its
// spot only on the server it redirects through: ZapFast never asks it.
func IsShortLink(text string) bool {
	u, err := url.Parse(strings.TrimSpace(text))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	switch strings.ToLower(strings.TrimRight(u.Hostname(), ".")) {
	case "goo.gl", "maps.app.goo.gl", "app.goo.gl", "g.co", "g.page":
		return true
	}
	return false
}

type mapService uint8

const (
	svcNone mapService = iota
	svcGoogle
	svcApple
	svcOSM
)

func serviceOf(u *url.URL) mapService {
	host := strings.ToLower(strings.TrimRight(u.Hostname(), "."))
	switch host {
	case "maps.apple.com", "maps.apple":
		return svcApple
	case "openstreetmap.org", "www.openstreetmap.org", "osm.org", "www.osm.org":
		return svcOSM
	}
	mapsHost := false
	rest := host
	if r, ok := strings.CutPrefix(host, "maps."); ok {
		mapsHost, rest = true, r
	} else {
		rest = strings.TrimPrefix(host, "www.")
	}
	tld, ok := strings.CutPrefix(rest, "google.")
	if !ok {
		return svcNone
	}
	letters := func(l string, lo, hi int) bool {
		if len(l) < lo || len(l) > hi {
			return false
		}
		for _, r := range l {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
				return false
			}
		}
		return true
	}
	parts := strings.Split(tld, ".")
	tldOK := false
	switch {
	case len(parts) == 1:
		tldOK = letters(parts[0], 2, 3)
	case len(parts) == 2 && (parts[0] == "co" || parts[0] == "com"):
		tldOK = letters(parts[1], 2, 2)
	}
	pathOK := mapsHost || u.Path == "/maps" || strings.HasPrefix(u.Path, "/maps/")
	if tldOK && pathOK {
		return svcGoogle
	}
	return svcNone
}

// linkSpot reads a link to a spot: Google Maps, Apple Maps, OpenStreetMap and
// any geo: URI. Links to other sites are not read.
func linkSpot(text string) (Spot, bool) {
	u, err := url.Parse(text)
	if err != nil {
		return Spot{}, false
	}
	if u.Scheme == "geo" {
		return geoURI(u)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return Spot{}, false
	}
	switch serviceOf(u) {
	case svcGoogle:
		if sp, ok := markedPlace(text); ok {
			return sp, true
		}
		if _, rest, ok := strings.Cut(u.Path, "/@"); ok {
			if sp, ok := twoSpot(rest); ok {
				return sp, true
			}
		}
		return querySpot(u, "q", "ll", "query", "daddr", "destination", "center")
	case svcApple:
		return querySpot(u, "ll", "q", "daddr", "sll", "coordinate")
	case svcOSM:
		if sp, ok := querySpot(u); ok {
			return sp, true
		}
		return osmPosition(u.Fragment)
	}
	return Spot{}, false
}

// geoURI reads geo:<lat>,<long>[,<alt>][;params], and the geo:0,0?q=<lat>,<long>(Label)
// form Android writes.
func geoURI(u *url.URL) (Spot, bool) {
	path := u.Opaque
	if path == "" {
		path = u.Path
	}
	coords, _, _ := strings.Cut(path, ";")
	sp, ok := twoSpot(coords)
	if !ok {
		return Spot{}, false
	}
	if q := u.Query().Get("q"); q != "" {
		head, _, _ := strings.Cut(q, "(")
		if asked, ok := pairSpot(head); ok && sp.Latitude == 0 && sp.Longitude == 0 {
			return asked, true
		}
	}
	return sp, true
}

// markedPlace reads the !3d<lat>!4d<lon> markers a Google Maps link carries.
func markedPlace(text string) (Spot, bool) {
	la, ok1 := between(text, "!3d", "!")
	lo, ok2 := between(text, "!4d", "!")
	if !ok1 || !ok2 {
		return Spot{}, false
	}
	lat, ok1 := number(la)
	lon, ok2 := number(lo)
	return Spot{lat, lon}, ok1 && ok2
}

func between(text, start, end string) (string, bool) {
	_, rest, ok := strings.Cut(text, start)
	if !ok {
		return "", false
	}
	head, _, _ := strings.Cut(rest, end)
	return head, true
}

func osmPosition(fragment string) (Spot, bool) {
	rest, ok := strings.CutPrefix(fragment, "map=")
	if !ok {
		return Spot{}, false
	}
	_, rest, ok = strings.Cut(rest, "/")
	if !ok {
		return Spot{}, false
	}
	rest, _, _ = strings.Cut(rest, "&")
	parts := strings.Split(rest, "/")
	if len(parts) < 2 {
		return Spot{}, false
	}
	return joined(parts[0], parts[1])
}

// querySpot reads a spot out of a link's query: one of keys naming the
// coordinates, or OpenStreetMap's mlat and mlon pair.
func querySpot(u *url.URL, keys ...string) (Spot, bool) {
	var lat, lon float64
	var hasLat, hasLon bool
	for _, kv := range strings.Split(u.RawQuery, "&") {
		k, v, _ := strings.Cut(kv, "=")
		k, _ = url.QueryUnescape(k)
		v, _ = url.QueryUnescape(v)
		switch {
		case k == "mlat":
			lat, hasLat = number(v)
		case k == "mlon":
			lon, hasLon = number(v)
		default:
			for _, want := range keys {
				if k == want {
					if sp, ok := pairSpot(v); ok {
						return sp, true
					}
				}
			}
		}
	}
	return Spot{lat, lon}, hasLat && hasLon
}

// twoSpot reads the first two comma-separated numbers of a chunk.
func twoSpot(chunk string) (Spot, bool) {
	parts := strings.Split(chunk, ",")
	if len(parts) < 2 {
		return Spot{}, false
	}
	return joined(parts[0], parts[1])
}

// pairSpot reads two numbers written as coordinates: "-23.55 -46.63",
// "-23.55, -46.63" or "-23,5505,-46,6333". A comma is the decimal mark in
// much of the world and the separator in the rest, so a text with several
// commas is read only when one reading alone is possible.
func pairSpot(text string) (Spot, bool) {
	tokens := strings.Fields(text)
	switch len(tokens) {
	case 2:
		return joined(strings.TrimSuffix(tokens[0], ","), tokens[1])
	case 1:
		single := tokens[0]
		var first Spot
		found := false
		for i := 0; i < len(single); i++ {
			if single[i] != ',' {
				continue
			}
			sp, ok := joined(single[:i], single[i+1:])
			if !ok {
				continue
			}
			if !found {
				first, found = sp, true
			} else if sp != first {
				return Spot{}, false
			}
		}
		return first, found
	}
	return Spot{}, false
}

func joined(lat, lon string) (Spot, bool) {
	la, ok1 := number(lat)
	lo, ok2 := number(lon)
	return Spot{la, lo}, ok1 && ok2
}

// number reads one number, with a dot or a comma as the decimal mark. "1.234,56"
// is read the way it is written in much of the world: the dot groups
// thousands. A comma after the number is a separator.
func number(token string) (float64, bool) {
	token = strings.TrimSpace(token)
	if bare, ok := strings.CutSuffix(token, ","); ok {
		if v, ok := parseNumber(bare); ok {
			return v, true
		}
	}
	return parseNumber(token)
}

func parseNumber(token string) (float64, bool) {
	if token == "" {
		return 0, false
	}
	negative := strings.HasPrefix(token, "-")
	digits := strings.TrimPrefix(strings.TrimPrefix(token, "-"), "+")
	if strings.HasPrefix(token, "--") || strings.HasPrefix(token, "-+") || strings.HasPrefix(token, "+-") {
		return 0, false
	}
	hasDot, hasComma := strings.Contains(digits, "."), strings.Contains(digits, ",")
	switch {
	case hasDot && hasComma:
		digits = strings.ReplaceAll(strings.ReplaceAll(digits, ".", ""), ",", ".")
	case hasComma:
		digits = strings.ReplaceAll(digits, ",", ".")
	}
	if digits == "" || strings.Count(digits, ".") > 1 {
		return 0, false
	}
	for _, r := range digits {
		if !(r >= '0' && r <= '9' || r == '.') {
			return 0, false
		}
	}
	v, err := strconv.ParseFloat(digits, 64)
	if err != nil || math.IsInf(v, 0) || math.IsNaN(v) {
		return 0, false
	}
	if negative {
		v = -v
	}
	return v, true
}

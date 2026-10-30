package dialogs

import (
	"math"
	"strings"
	"testing"
)

// FuzzParseSpot checks the location dialog's reading of text: a spot it
// accepts is on the earth; a spot's own Text and MapURL read back as it,
// in the ways coordinates are written (a space or a comma between, a dot or
// a comma as the decimal mark); white space around the text changes
// nothing; and a shortened link is never read, since its spot is only on
// the server.
func FuzzParseSpot(f *testing.F) {
	for _, s := range []string{
		"48.8584, 2.2945", "geo:51.5,-0.12", "https://www.google.com/maps/@40.7128,-74.006,15z",
		"https://www.openstreetmap.org/#map=17/52.52/13.405", "https://maps.apple.com/?ll=37.33,-122.03", "91, 0",
		"hello", "-23,5505,-46,6333", "-23.55 -46.63", "1.234,56 7", "geo:0,0?q=-23.5,-46.6(Home)",
		"https://www.google.com/maps/place/x/data=!3d-23.5!4d-46.6!", "https://goo.gl/maps/abc",
		"https://maps.app.goo.gl/1,2", "https://www.google.co.uk/maps?q=1,2", "https://osm.org/?mlat=1&mlon=2",
		"geo:", "geo:,", "--1 2", "+-1 2", "1e5 2", "NaN, 0", "Inf 0", "0x1p3 0", "\xff,\xff", "9 9 9", "",
	} {
		f.Add(s, 48.8584, 2.2945)
	}
	f.Fuzz(func(t *testing.T, text string, lat, lon float64) {
		sp, ok := ParseSpot(text)
		if ok && !sp.onEarth() {
			t.Fatalf("ParseSpot(%q) = %+v, off the earth", text, sp)
		}
		if !ok && sp != (Spot{}) {
			t.Fatalf("ParseSpot(%q) refused, but gave %+v", text, sp)
		}
		if sp2, ok2 := ParseSpot(" \t" + text + "\n "); sp2 != sp || ok2 != ok {
			t.Fatalf("ParseSpot(%q) = %+v, %v; with white space around %+v, %v", text, sp, ok, sp2, ok2)
		}
		if IsShortLink(text) && ok {
			t.Fatalf("the short link %q was read as %+v", text, sp)
		}

		want := Spot{lat, lon}
		if !want.onEarth() {
			return
		}
		// MapURL writes every digit: it reads back exactly.
		if got, ok := ParseSpot(MapURL(want)); !ok || got != want {
			t.Fatalf("MapURL %q reads as %+v, %v; want %+v", MapURL(want), got, ok, want)
		}
		// Text rounds to five places.
		close := func(a Spot) bool {
			return math.Abs(a.Latitude-lat) <= 5.000001e-6 && math.Abs(a.Longitude-lon) <= 5.000001e-6
		}
		written := want.Text()
		for _, form := range []string{
			written,
			strings.Replace(written, ", ", " ", 1),
			strings.Replace(written, ", ", ",", 1),
			strings.ReplaceAll(written, ".", ","),                                // "48,85840, 2,29450"
			strings.Replace(strings.ReplaceAll(written, ".", ","), ", ", " ", 1), // "48,85840 2,29450"
			"geo:" + strings.Replace(written, ", ", ",", 1),
			"https://www.google.com/maps/@" + strings.Replace(written, ", ", ",", 1) + ",15z",
			"https://maps.apple.com/?ll=" + strings.Replace(written, ", ", ",", 1),
		} {
			got, ok := ParseSpot(form)
			if !ok || !close(got) {
				t.Fatalf("%+v written %q reads as %+v, %v", want, form, got, ok)
			}
		}
	})
}

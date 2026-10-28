package theme

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ParseColor reads #RRGGBB or #RRGGBBAA (alpha not premultiplied) into
// 0xRRGGBBAA.
func ParseColor(s string) (uint32, bool) {
	if len(s) != 7 && len(s) != 9 || s[0] != '#' {
		return 0, false
	}
	var v uint32
	for i := 1; i < len(s); i++ {
		c := s[i]
		var d byte
		switch {
		case c >= '0' && c <= '9':
			d = c - '0'
		case c >= 'a' && c <= 'f':
			d = c - 'a' + 10
		case c >= 'A' && c <= 'F':
			d = c - 'A' + 10
		default:
			return 0, false
		}
		v = v<<4 | uint32(d)
	}
	if len(s) == 7 {
		v = v<<8 | 0xFF
	}
	return v, true
}

// paletteFile is a palette file's contents (zf: fastframe-theme PaletteFile).
type paletteFile struct {
	Base   string            `json:"base"`
	Colors map[string]string `json:"colors"`
}

// ParsePalette reads a palette file: its base (dark, the default, or light),
// then each colour it sets, then the chat colours it leaves out derived from
// those it set. It fails on invalid JSON, an unknown field, a base that is
// neither, a colour that is not #RRGGBB or #RRGGBBAA, or a colour name the
// palette does not have; the error names the colour.
func ParsePalette(data []byte) (Palette, error) {
	var f paletteFile
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return Palette{}, err
	}
	if dec.More() {
		return Palette{}, errors.New("trailing characters after the palette")
	}
	var p Palette
	switch f.Base {
	case "", "dark":
		p = DarkPalette()
	case "light":
		p = LightPalette()
	default:
		return Palette{}, fmt.Errorf("unknown variant `%s`, expected `dark` or `light`", f.Base)
	}
	names := make([]string, 0, len(f.Colors))
	for name := range f.Colors {
		names = append(names, name)
	}
	sort.Strings(names)
	given := make(map[string]bool, len(names))
	for _, name := range names {
		c, ok := ParseColor(f.Colors[name])
		if !ok {
			return Palette{}, fmt.Errorf("%s: expected #RRGGBB or #RRGGBBAA", name)
		}
		if !p.set(name, c) {
			return Palette{}, fmt.Errorf("unknown color: %s", name)
		}
		given[name] = true
	}
	p.derive(given)
	return p, nil
}

// DisplayName is the name a theme file is shown under: its name without
// ".json" (case-insensitive), or the whole name when nothing is left.
func DisplayName(file string) string {
	const ext = ".json"
	if len(file) > len(ext) && strings.EqualFold(file[len(file)-len(ext):], ext) {
		return file[:len(file)-len(ext)]
	}
	return file
}

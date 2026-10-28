package main

import (
	"fmt"
	"strconv"
	"strings"
)

// Kinds of field types.
const (
	kPrim  = "prim"
	kStr   = "str"
	kBytes = "bytes"
	kPath  = "path"
	kBlob  = "blob"
	kOpt   = "opt"
	kList  = "list"
	kChunk = "chunk"
	kNamed = "named"
)

var primSize = map[string]int{
	"bool": 1, "u8": 1, "u16": 2, "u32": 4, "u64": 8,
	"i32": 4, "i64": 8, "f32": 4, "f64": 8,
}

type Type struct {
	Kind   string
	Prim   string // kPrim
	Max    int    // str, bytes, path: bytes; list, chunk: elements
	Budget int    // chunk: encoded bytes
	Elem   *Type  // opt, list, chunk
	Name   string // kNamed
}

type Field struct {
	Name string
	T    *Type
	Line int
}

type Variant struct {
	Name   string
	Tag    int
	Fields []Field
	Line   int
}

// Decl is an enum, struct or union.
type Decl struct {
	Kind     string // "enum", "struct", "union"
	Name     string
	Variants []Variant // enum (no fields) and union
	Fields   []Field   // struct
	Line     int
}

type Schema struct {
	ABI       int
	MaxRecord int
	Decls     []*Decl
	byName    map[string]*Decl
	Events    []Variant
	Commands  []Variant
	Hash      uint64
}

// normalized is the text the schema hash covers: comments and blank lines
// removed, every line trimmed.
func normalized(src string) string {
	var b strings.Builder
	for _, line := range strings.Split(src, "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// fnv1a64 is the schema hash: FNV-1a, 64 bits, over the normalized text.
func fnv1a64(s string) uint64 {
	h := uint64(0xcbf29ce484222325)
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 0x100000001b3
	}
	return h
}

type parser struct {
	lines []string
	nums  []int
	i     int
}

func (p *parser) errf(format string, args ...any) error {
	n := 0
	if p.i > 0 && p.i <= len(p.nums) {
		n = p.nums[p.i-1]
	}
	return fmt.Errorf("zf.schema:%d: %s", n, fmt.Sprintf(format, args...))
}

func (p *parser) next() (string, bool) {
	if p.i >= len(p.lines) {
		return "", false
	}
	p.i++
	return p.lines[p.i-1], true
}

func parseSchema(src string) (*Schema, error) {
	p := &parser{}
	for n, line := range strings.Split(src, "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line != "" {
			p.lines = append(p.lines, line)
			p.nums = append(p.nums, n+1)
		}
	}
	s := &Schema{byName: map[string]*Decl{}, Hash: fnv1a64(normalized(src))}
	for {
		line, ok := p.next()
		if !ok {
			break
		}
		words := strings.Fields(line)
		switch words[0] {
		case "abi", "max_record":
			if len(words) != 2 {
				return nil, p.errf("%s takes one number", words[0])
			}
			n, err := strconv.Atoi(words[1])
			if err != nil {
				return nil, p.errf("%v", err)
			}
			if words[0] == "abi" {
				s.ABI = n
			} else {
				s.MaxRecord = n
			}
		case "enum":
			// enum Name u8 {
			if len(words) != 4 || words[2] != "u8" || words[3] != "{" {
				return nil, p.errf("expected `enum Name u8 {`")
			}
			d := &Decl{Kind: "enum", Name: words[1], Line: p.nums[p.i-1]}
			for {
				line, ok := p.next()
				if !ok {
					return nil, p.errf("unterminated enum %s", d.Name)
				}
				if line == "}" {
					break
				}
				name, tag, err := nameTag(line)
				if err != nil {
					return nil, p.errf("%v", err)
				}
				d.Variants = append(d.Variants, Variant{Name: name, Tag: tag, Line: p.nums[p.i-1]})
			}
			if err := s.add(d); err != nil {
				return nil, p.errf("%v", err)
			}
		case "struct":
			if len(words) != 3 || words[2] != "{" {
				return nil, p.errf("expected `struct Name {`")
			}
			d := &Decl{Kind: "struct", Name: words[1], Line: p.nums[p.i-1]}
			fields, err := p.fields()
			if err != nil {
				return nil, err
			}
			d.Fields = fields
			if err := s.add(d); err != nil {
				return nil, p.errf("%v", err)
			}
		case "union":
			if len(words) != 3 || words[2] != "{" {
				return nil, p.errf("expected `union Name {`")
			}
			d := &Decl{Kind: "union", Name: words[1], Line: p.nums[p.i-1]}
			for {
				line, ok := p.next()
				if !ok {
					return nil, p.errf("unterminated union %s", d.Name)
				}
				if line == "}" {
					break
				}
				v, err := p.variant(line)
				if err != nil {
					return nil, err
				}
				d.Variants = append(d.Variants, v)
			}
			if err := s.add(d); err != nil {
				return nil, p.errf("%v", err)
			}
		case "event", "command":
			v, err := p.variant(strings.TrimSpace(strings.TrimPrefix(line, words[0])))
			if err != nil {
				return nil, err
			}
			if words[0] == "event" {
				s.Events = append(s.Events, v)
			} else {
				s.Commands = append(s.Commands, v)
			}
		default:
			return nil, p.errf("unknown declaration %q", words[0])
		}
	}
	return s, s.check()
}

func (s *Schema) add(d *Decl) error {
	if _, dup := s.byName[d.Name]; dup {
		return fmt.Errorf("%s declared twice", d.Name)
	}
	if _, prim := primSize[d.Name]; prim || d.Name == "Event" || d.Name == "Command" {
		return fmt.Errorf("%s is a reserved name", d.Name)
	}
	s.byName[d.Name] = d
	s.Decls = append(s.Decls, d)
	return nil
}

// nameTag reads `Name = n`.
func nameTag(line string) (string, int, error) {
	name, num, ok := strings.Cut(line, "=")
	if !ok {
		return "", 0, fmt.Errorf("expected `Name = n`")
	}
	name = strings.TrimSpace(name)
	tag, err := strconv.Atoi(strings.TrimSpace(num))
	if err != nil {
		return "", 0, err
	}
	if !isIdent(name) {
		return "", 0, fmt.Errorf("bad name %q", name)
	}
	return name, tag, nil
}

// variant reads `Name = n {` (fields follow, then `}`) or `Name = n {}`.
func (p *parser) variant(line string) (Variant, error) {
	empty := strings.HasSuffix(line, "{}")
	if !empty && !strings.HasSuffix(line, "{") {
		return Variant{}, p.errf("expected `Name = n {`")
	}
	head := strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(line, "{}"), "{"))
	name, tag, err := nameTag(head)
	if err != nil {
		return Variant{}, p.errf("%v", err)
	}
	v := Variant{Name: name, Tag: tag, Line: p.nums[p.i-1]}
	if empty {
		return v, nil
	}
	v.Fields, err = p.fields()
	return v, err
}

func (p *parser) fields() ([]Field, error) {
	var out []Field
	for {
		line, ok := p.next()
		if !ok {
			return nil, p.errf("unterminated field list")
		}
		if line == "}" {
			return out, nil
		}
		name, typ, ok := strings.Cut(line, ":")
		if !ok {
			return nil, p.errf("expected `name: type`")
		}
		name = strings.TrimSpace(name)
		if !isIdent(name) || strings.ToLower(name) != name {
			return nil, p.errf("field names are snake_case: %q", name)
		}
		t, rest, err := parseType(strings.TrimSpace(typ))
		if err != nil {
			return nil, p.errf("%v", err)
		}
		if rest != "" {
			return nil, p.errf("unexpected %q after the type", rest)
		}
		out = append(out, Field{Name: name, T: t, Line: p.nums[p.i-1]})
	}
}

// parseType reads one type from the start of s and returns what follows.
func parseType(s string) (*Type, string, error) {
	word, rest := s, ""
	if i := strings.IndexAny(s, " ("); i >= 0 {
		word, rest = s[:i], s[i:]
	}
	args := func() ([]int, error) {
		if !strings.HasPrefix(rest, "(") {
			return nil, fmt.Errorf("%s needs a bound", word)
		}
		end := strings.IndexByte(rest, ')')
		if end < 0 {
			return nil, fmt.Errorf("unclosed bound")
		}
		var ns []int
		for _, a := range strings.Split(rest[1:end], ",") {
			n, err := strconv.Atoi(strings.TrimSpace(a))
			if err != nil || n <= 0 {
				return nil, fmt.Errorf("bad bound %q", a)
			}
			ns = append(ns, n)
		}
		rest = strings.TrimSpace(rest[end+1:])
		return ns, nil
	}
	switch word {
	case "str", "bytes", "path":
		ns, err := args()
		if err != nil {
			return nil, "", err
		}
		if len(ns) != 1 {
			return nil, "", fmt.Errorf("%s takes one bound", word)
		}
		return &Type{Kind: word, Max: ns[0]}, rest, nil
	case "blob":
		return &Type{Kind: kBlob}, strings.TrimSpace(rest), nil
	case "opt":
		elem, rest, err := parseType(strings.TrimSpace(rest))
		if err != nil {
			return nil, "", err
		}
		return &Type{Kind: kOpt, Elem: elem}, rest, nil
	case "list", "chunk":
		ns, err := args()
		if err != nil {
			return nil, "", err
		}
		want := 1
		if word == "chunk" {
			want = 2
		}
		if len(ns) != want {
			return nil, "", fmt.Errorf("%s takes %d bounds", word, want)
		}
		elem, rest, err := parseType(rest)
		if err != nil {
			return nil, "", err
		}
		t := &Type{Kind: word, Max: ns[0], Elem: elem}
		if word == "chunk" {
			t.Budget = ns[1]
		}
		return t, rest, nil
	}
	if _, ok := primSize[word]; ok {
		return &Type{Kind: kPrim, Prim: word}, strings.TrimSpace(rest), nil
	}
	if !isIdent(word) {
		return nil, "", fmt.Errorf("bad type %q", word)
	}
	return &Type{Kind: kNamed, Name: word}, strings.TrimSpace(rest), nil
}

func isIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, c := range s {
		if c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || i > 0 && c >= '0' && c <= '9' {
			continue
		}
		return false
	}
	return true
}

// check resolves names, refuses cycles and shapes the generators do not
// support, and proves every record fits MaxRecord.
func (s *Schema) check() error {
	if s.ABI <= 0 || s.MaxRecord <= 0 {
		return fmt.Errorf("zf.schema: abi and max_record are required")
	}
	for _, d := range s.Decls {
		seen := map[int]bool{}
		names := map[string]bool{}
		for _, v := range d.Variants {
			if seen[v.Tag] || names[v.Name] {
				return fmt.Errorf("zf.schema:%d: %s: duplicate variant tag or name", v.Line, d.Name)
			}
			if v.Tag < 0 || v.Tag > 255 {
				return fmt.Errorf("zf.schema:%d: %s tags are one byte", v.Line, d.Name)
			}
			seen[v.Tag], names[v.Name] = true, true
			if err := s.checkFields(v.Fields, false); err != nil {
				return err
			}
		}
		if err := s.checkFields(d.Fields, false); err != nil {
			return err
		}
	}
	for _, d := range s.Decls {
		if _, err := s.declMax(d, map[string]bool{}); err != nil {
			return err
		}
	}
	for _, root := range [][]Variant{s.Events, s.Commands} {
		seen := map[int]bool{}
		names := map[string]bool{}
		for _, v := range root {
			if seen[v.Tag] || names[v.Name] || v.Tag <= 0 || v.Tag > 65535 {
				return fmt.Errorf("zf.schema:%d: %s: duplicate or out-of-range tag or name", v.Line, v.Name)
			}
			seen[v.Tag], names[v.Name] = true, true
			if err := s.checkFields(v.Fields, true); err != nil {
				return err
			}
			if max := s.recordMax(v); max > s.MaxRecord {
				return fmt.Errorf("zf.schema:%d: %s can reach %d bytes, over max_record %d", v.Line, v.Name, max, s.MaxRecord)
			}
		}
	}
	return nil
}

func (s *Schema) checkFields(fields []Field, record bool) error {
	names := map[string]bool{}
	chunks := 0
	for _, f := range fields {
		if names[f.Name] {
			return fmt.Errorf("zf.schema:%d: field %s twice", f.Line, f.Name)
		}
		names[f.Name] = true
		if f.T.Kind == kChunk {
			chunks++
			if !record {
				return fmt.Errorf("zf.schema:%d: chunk fields belong to events and commands", f.Line)
			}
		}
		if err := s.checkType(f.T, f.Line, true); err != nil {
			return err
		}
	}
	if chunks > 1 {
		return fmt.Errorf("zf.schema: a record has at most one chunk field")
	}
	return nil
}

func (s *Schema) checkType(t *Type, line int, top bool) error {
	switch t.Kind {
	case kNamed:
		if s.byName[t.Name] == nil {
			return fmt.Errorf("zf.schema:%d: unknown type %s", line, t.Name)
		}
	case kOpt:
		if !top {
			return fmt.Errorf("zf.schema:%d: opt is a field's outer type only", line)
		}
		if t.Elem.Kind == kOpt || t.Elem.Kind == kList || t.Elem.Kind == kChunk {
			return fmt.Errorf("zf.schema:%d: opt of a list or opt; use an empty list", line)
		}
		return s.checkType(t.Elem, line, false)
	case kList, kChunk:
		if t.Elem.Kind == kOpt || t.Elem.Kind == kList || t.Elem.Kind == kChunk {
			return fmt.Errorf("zf.schema:%d: list elements are scalars, strings or named types", line)
		}
		if err := s.checkType(t.Elem, line, false); err != nil {
			return err
		}
		if t.Kind == kChunk {
			if m, err := s.typeMax(t.Elem, map[string]bool{}); err != nil {
				return err
			} else if m > t.Budget {
				return fmt.Errorf("zf.schema:%d: one element (%d bytes) exceeds the chunk budget %d", line, m, t.Budget)
			}
		}
	}
	return nil
}

// typeMax is the largest encoding of t; stack refuses recursive types.
func (s *Schema) typeMax(t *Type, stack map[string]bool) (int, error) {
	switch t.Kind {
	case kPrim:
		return primSize[t.Prim], nil
	case kStr, kBytes, kPath:
		return 4 + t.Max, nil
	case kBlob:
		return 16, nil
	case kOpt:
		m, err := s.typeMax(t.Elem, stack)
		return 1 + m, err
	case kList:
		m, err := s.typeMax(t.Elem, stack)
		return 4 + t.Max*m, err
	case kChunk:
		return 4 + t.Budget, nil
	case kNamed:
		return s.declMax(s.byName[t.Name], stack)
	}
	panic(t.Kind)
}

func (s *Schema) declMax(d *Decl, stack map[string]bool) (int, error) {
	if stack[d.Name] {
		return 0, fmt.Errorf("zf.schema:%d: %s contains itself; records are not recursive", d.Line, d.Name)
	}
	stack[d.Name] = true
	defer delete(stack, d.Name)
	switch d.Kind {
	case "enum":
		return 1, nil
	case "struct":
		return s.fieldsMax(d.Fields, stack)
	}
	best := 0
	for _, v := range d.Variants {
		m, err := s.fieldsMax(v.Fields, stack)
		if err != nil {
			return 0, err
		}
		best = max(best, m)
	}
	return 1 + best, nil
}

func (s *Schema) fieldsMax(fields []Field, stack map[string]bool) (int, error) {
	total := 0
	for _, f := range fields {
		m, err := s.typeMax(f.T, stack)
		if err != nil {
			return 0, err
		}
		total += m
	}
	return total, nil
}

func (s *Schema) mustMax(t *Type) int {
	m, err := s.typeMax(t, map[string]bool{})
	if err != nil {
		panic(err)
	}
	return m
}

func (s *Schema) recordMax(v Variant) int {
	m, err := s.fieldsMax(v.Fields, map[string]bool{})
	if err != nil {
		panic(err)
	}
	return 4 + m
}

// typeMin is the smallest encoding of t, which bounds a list's count by
// the bytes that remain.
func (s *Schema) typeMin(t *Type) int {
	switch t.Kind {
	case kPrim:
		return primSize[t.Prim]
	case kStr, kBytes, kPath, kList, kChunk:
		return 4
	case kBlob:
		return 16
	case kOpt:
		return 1
	case kNamed:
		d := s.byName[t.Name]
		switch d.Kind {
		case "enum":
			return 1
		case "struct":
			return s.fieldsMin(d.Fields)
		}
		best := -1
		for _, v := range d.Variants {
			if m := s.fieldsMin(v.Fields); best < 0 || m < best {
				best = m
			}
		}
		return 1 + max(best, 0)
	}
	panic(t.Kind)
}

func (s *Schema) fieldsMin(fields []Field) int {
	total := 0
	for _, f := range fields {
		total += s.typeMin(f.T)
	}
	return total
}

// unionWidth is the most variants any union reachable from fields has: how
// many samples it takes for every variant to appear.
func (s *Schema) unionWidth(fields []Field) int {
	best := 1
	var walk func(t *Type)
	walk = func(t *Type) {
		switch t.Kind {
		case kOpt, kList, kChunk:
			walk(t.Elem)
		case kNamed:
			d := s.byName[t.Name]
			if d.Kind == "union" || d.Kind == "enum" {
				best = max(best, len(d.Variants))
			}
			for _, f := range d.Fields {
				walk(f.T)
			}
			for _, v := range d.Variants {
				for _, f := range v.Fields {
					walk(f.T)
				}
			}
		}
	}
	for _, f := range fields {
		walk(f.T)
	}
	return best
}

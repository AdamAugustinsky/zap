package main

import (
	"fmt"
	"strings"
)

var initialisms = map[string]string{
	"id": "ID", "url": "URL", "qr": "QR", "lid": "LID", "rgba": "RGBA", "abi": "ABI",
	"ms": "MS", "mps": "MPS",
}

func goName(snakeName string) string {
	var b strings.Builder
	for _, part := range strings.Split(snakeName, "_") {
		if up, ok := initialisms[part]; ok {
			b.WriteString(up)
			continue
		}
		if part == "" {
			continue
		}
		b.WriteString(strings.ToUpper(part[:1]) + part[1:])
	}
	return b.String()
}

var goPrim = map[string]string{
	"bool": "bool", "u8": "uint8", "u16": "uint16", "u32": "uint32", "u64": "uint64",
	"i32": "int32", "i64": "int64", "f32": "float32", "f64": "float64",
}

type goGen struct {
	s     *Schema
	b     strings.Builder
	n     int
	views map[string]bool // decl name: whether a decoded value can hold views
}

// hasViews reports whether a decoded t can hold byte slices aliasing the
// record it came from (directly or in a nested struct, union or list).
func (g *goGen) hasViews(t *Type) bool {
	switch t.Kind {
	case kStr, kBytes, kPath:
		return true
	case kOpt, kList, kChunk:
		return g.hasViews(t.Elem)
	case kNamed:
		return g.declViews(g.s.byName[t.Name])
	}
	return false
}

func (g *goGen) declViews(d *Decl) bool {
	if v, ok := g.views[d.Name]; ok {
		return v
	}
	if g.views == nil {
		g.views = map[string]bool{}
	}
	g.views[d.Name] = false // a recursive reference adds nothing
	v := false
	switch d.Kind {
	case "struct":
		v = g.fieldsViews(d.Fields)
	case "union":
		for _, vr := range d.Variants {
			v = v || g.fieldsViews(vr.Fields)
		}
	}
	g.views[d.Name] = v
	return v
}

func (g *goGen) fieldsViews(fields []Field) bool {
	for _, f := range fields {
		if g.hasViews(f.T) {
			return true
		}
	}
	return false
}

func (g *goGen) variantsViews(vs []Variant) bool {
	for _, v := range vs {
		if g.fieldsViews(v.Fields) {
			return true
		}
	}
	return false
}

// rel writes statements that drop the views x holds, keeping its lists'
// capacity (their elements released, the list emptied). It writes nothing
// for a type that holds none.
func (g *goGen) rel(x string, t *Type, indent string) {
	if !g.hasViews(t) {
		return
	}
	switch t.Kind {
	case kStr, kBytes, kPath:
		g.p("%s%s = nil", indent, x)
	case kOpt:
		g.rel(x, t.Elem, indent)
	case kList, kChunk:
		g.relElems(x, t.Elem, indent)
		g.p("%s%s = %s[:0]", indent, x, x)
	case kNamed:
		g.p("%s%s.Release()", indent, x)
	}
}

// relElems releases every element of the slice expression x.
func (g *goGen) relElems(x string, elem *Type, indent string) {
	switch elem.Kind {
	case kStr, kBytes, kPath:
		g.p("%sclear(%s)", indent, x)
		return
	}
	i := g.fresh("i")
	g.p("%sfor %s := range %s {", indent, i, x)
	g.rel(fmt.Sprintf("%s[%s]", x, i), elem, indent+"\t")
	g.p("%s}", indent)
}

func (g *goGen) p(format string, args ...any) {
	fmt.Fprintf(&g.b, format, args...)
	g.b.WriteByte('\n')
}

func (g *goGen) fresh(prefix string) string {
	g.n++
	return fmt.Sprintf("%s%d", prefix, g.n)
}

func goType(t *Type) string {
	switch t.Kind {
	case kPrim:
		return goPrim[t.Prim]
	case kStr, kBytes, kPath:
		return "[]byte"
	case kBlob:
		return "BlobRef"
	case kOpt:
		return goType(t.Elem)
	case kList, kChunk:
		return "[]" + goType(t.Elem)
	case kNamed:
		return t.Name
	}
	panic(t.Kind)
}

func (g *goGen) fieldsDecl(fields []Field) {
	for _, f := range fields {
		if f.T.Kind == kOpt {
			g.p("\t%s %s", goName(f.Name), goType(f.T))
			g.p("\tHas%s bool", goName(f.Name))
			continue
		}
		g.p("\t%s %s", goName(f.Name), goType(f.T))
	}
}

// dec writes statements that decode into the addressable expression x.
func (g *goGen) dec(x string, t *Type, indent string) {
	switch t.Kind {
	case kPrim:
		if t.Prim == "bool" {
			g.p("%s%s = r.flag()", indent, x)
		} else {
			g.p("%s%s = r.%s()", indent, x, t.Prim)
		}
	case kStr:
		g.p("%s%s = r.str(%d)", indent, x, t.Max)
	case kBytes, kPath:
		g.p("%s%s = r.bytes(%d)", indent, x, t.Max)
	case kBlob:
		g.p("%s%s = r.blob()", indent, x)
	case kList, kChunk:
		n := g.fresh("n")
		i := g.fresh("i")
		old := ""
		if g.hasViews(t.Elem) {
			// Elements past the new length keep their capacity but must
			// not keep views into an older record.
			old = g.fresh("old")
			g.p("%s%s := len(%s)", indent, old, x)
		}
		g.p("%s%s := r.count(%d, %d)", indent, n, t.Max, g.s.typeMin(t.Elem))
		start := ""
		if t.Kind == kChunk {
			// The encoder refuses a chunk over its byte budget, so the decoder
			// must too: what decodes must re-encode.
			start = g.fresh("start")
			g.p("%s%s := r.off", indent, start)
		}
		g.p("%s%s = grow(%s, %s)", indent, x, x, n)
		g.p("%sfor %s := range %s {", indent, i, x)
		g.dec(fmt.Sprintf("%s[%s]", x, i), t.Elem, indent+"\t")
		g.p("%s}", indent)
		if old != "" {
			rest := g.fresh("rest")
			g.p("%sif %s > %s {", indent, old, n)
			g.p("%s\t%s := %s[%s:%s]", indent, rest, x, n, old)
			g.relElems(rest, t.Elem, indent+"\t")
			g.p("%s}", indent)
		}
		if t.Kind == kChunk {
			g.p("%sr.budget(%s, %d)", indent, start, t.Budget)
		}
	case kNamed:
		d := g.s.byName[t.Name]
		if d.Kind == "enum" {
			g.p("%s%s = %s(r.u8())", indent, x, t.Name)
			g.p("%sif !%s.valid() {", indent, x)
			g.p("%s\tr.fail(ErrBadEnum)", indent)
			g.p("%s}", indent)
		} else {
			g.p("%s%s.decode(r)", indent, x)
		}
	default:
		panic(t.Kind)
	}
}

func (g *goGen) decField(recv string, f Field, indent string) {
	x := recv + "." + goName(f.Name)
	if f.T.Kind == kOpt {
		has := recv + ".Has" + goName(f.Name)
		g.p("%s%s = r.flag()", indent, has)
		g.p("%sif %s {", indent, has)
		g.dec(x, f.T.Elem, indent+"\t")
		if g.hasViews(f.T.Elem) {
			g.p("%s} else {", indent)
			g.rel(x, f.T.Elem, indent+"\t")
		}
		g.p("%s}", indent)
		return
	}
	g.dec(x, f.T, indent)
}

// enc writes statements that encode x.
func (g *goGen) enc(x string, t *Type, indent string) {
	switch t.Kind {
	case kPrim:
		g.p("%sw.%s(%s)", indent, t.Prim, x)
	case kStr:
		g.p("%sw.str(%s, %d)", indent, x, t.Max)
	case kBytes, kPath:
		g.p("%sw.bytes(%s, %d)", indent, x, t.Max)
	case kBlob:
		g.p("%sw.blob(%s)", indent, x)
	case kList, kChunk:
		i := g.fresh("i")
		g.p("%sw.count(len(%s), %d)", indent, x, t.Max)
		start := ""
		if t.Kind == kChunk {
			start = g.fresh("start")
			g.p("%s%s := len(w.b)", indent, start)
		}
		g.p("%sfor %s := range %s {", indent, i, x)
		g.enc(fmt.Sprintf("%s[%s]", x, i), t.Elem, indent+"\t")
		g.p("%s}", indent)
		if t.Kind == kChunk {
			g.p("%sw.budget(%s, %d)", indent, start, t.Budget)
		}
	case kNamed:
		if g.s.byName[t.Name].Kind == "enum" {
			g.p("%sw.u8(uint8(%s))", indent, x)
		} else {
			g.p("%s%s.encode(w)", indent, x)
		}
	default:
		panic(t.Kind)
	}
}

func (g *goGen) encField(recv string, f Field, indent string) {
	x := recv + "." + goName(f.Name)
	if f.T.Kind == kOpt {
		g.p("%sif %s.Has%s {", indent, recv, goName(f.Name))
		g.p("%s\tw.u8(1)", indent)
		g.enc(x, f.T.Elem, indent+"\t")
		g.p("%s} else {", indent)
		g.p("%s\tw.u8(0)", indent)
		g.p("%s}", indent)
		return
	}
	g.enc(x, f.T, indent)
}

func (g *goGen) structType(name string, fields []Field, doc string) {
	if doc != "" {
		g.p("// %s", doc)
	}
	g.p("type %s struct {", name)
	g.fieldsDecl(fields)
	g.p("}")
	g.p("")
	g.p("func (x *%s) decode(r *reader) {", name)
	for _, f := range fields {
		g.decField("x", f, "\t")
	}
	g.p("}")
	g.p("")
	g.p("func (x *%s) encode(w *writer) {", name)
	for _, f := range fields {
		g.encField("x", f, "\t")
	}
	g.p("}")
	g.p("")
	if g.fieldsViews(fields) {
		g.p("// Release drops x's views into the record it was decoded from, keeping")
		g.p("// its lists' capacity for the next decode.")
		g.p("func (x *%s) Release() {", name)
		for _, f := range fields {
			g.rel("x."+goName(f.Name), f.T, "\t")
		}
		g.p("}")
		g.p("")
	}
}

func genGo(s *Schema) string {
	g := &goGen{s: s}
	g.p("// Code generated by gnui/tools/abigen from abi/zf.schema. DO NOT EDIT.")
	g.p("")
	g.p("package abi")
	g.p("")
	g.p("// ABIVersion is ZF_ABI; zf_abi_version must return it.")
	g.p("const ABIVersion = %d", s.ABI)
	g.p("")
	g.p("// SchemaHash is FNV-1a 64 of the schema without comments or blank lines;")
	g.p("// zf_schema_hash must return it.")
	g.p("const SchemaHash uint64 = 0x%016x", s.Hash)
	g.p("")
	g.p("// MaxRecord is ZF_MAX_RECORD: no record is longer.")
	g.p("const MaxRecord = %d", s.MaxRecord)
	g.p("")
	for _, d := range s.Decls {
		switch d.Kind {
		case "enum":
			g.p("type %s uint8", d.Name)
			g.p("")
			g.p("const (")
			for _, v := range d.Variants {
				g.p("\t%s%s %s = %d", d.Name, v.Name, d.Name, v.Tag)
			}
			g.p(")")
			g.p("")
			g.p("func (x %s) valid() bool {", d.Name)
			g.p("\tswitch x {")
			var cases []string
			for _, v := range d.Variants {
				cases = append(cases, d.Name+v.Name)
			}
			g.p("\tcase %s:", strings.Join(cases, ", "))
			g.p("\t\treturn true")
			g.p("\t}")
			g.p("\treturn false")
			g.p("}")
			g.p("")
		case "struct":
			g.structType(d.Name, d.Fields, "")
			g.p("// Decode reads one bare %s record (no header); byte fields alias b.", d.Name)
			g.p("func (x *%s) Decode(b []byte) error {", d.Name)
			g.p("\tr := reader{b: b}")
			g.p("\tx.decode(&r)")
			g.p("\treturn r.finish()")
			g.p("}")
			g.p("")
			g.p("// Encode appends x as a bare record.")
			g.p("func (x *%s) Encode(dst []byte) ([]byte, error) {", d.Name)
			g.p("\tw := writer{b: dst}")
			g.p("\tx.encode(&w)")
			g.p("\treturn w.b, w.err")
			g.p("}")
			g.p("")
		case "union":
			g.union(d.Name, d.Variants, "u8")
		}
	}
	g.root("Event", s.Events)
	g.root("Command", s.Commands)
	return g.b.String()
}

// union declares Name (a Tag and one field per variant with fields), its
// tag type, and a struct per variant.
func (g *goGen) union(name string, vs []Variant, tagType string) {
	tag := name + "Tag"
	goTag := "uint8"
	if tagType == "u16" {
		goTag = "uint16"
	}
	g.p("type %s %s", tag, goTag)
	g.p("")
	g.p("const (")
	for _, v := range vs {
		g.p("\t%s%s %s = %d", tag, v.Name, tag, v.Tag)
	}
	g.p(")")
	g.p("")
	g.p("func (t %s) String() string {", tag)
	g.p("\tswitch t {")
	for _, v := range vs {
		g.p("\tcase %s%s:", tag, v.Name)
		g.p("\t\treturn %q", v.Name)
	}
	g.p("\t}")
	g.p("\treturn \"unknown\"")
	g.p("}")
	g.p("")
	g.p("// %s holds one variant: the field Tag names. The others keep their", name)
	g.p("// lists' capacity for reuse but no views: decoding another variant")
	g.p("// releases the one before.")
	g.p("type %s struct {", name)
	g.p("\tTag %s", tag)
	for _, v := range vs {
		if len(v.Fields) > 0 {
			g.p("\t%s %s%s", v.Name, name, v.Name)
		}
	}
	g.p("}")
	g.p("")
	for _, v := range vs {
		if len(v.Fields) > 0 {
			g.structType(name+v.Name, v.Fields, "")
		}
	}
	views := g.variantsViews(vs)
	if views {
		g.p("func (x *%s) releaseTag(t %s) {", name, tag)
		g.p("\tswitch t {")
		for _, v := range vs {
			if g.fieldsViews(v.Fields) {
				g.p("\tcase %s%s:", tag, v.Name)
				g.p("\t\tx.%s.Release()", v.Name)
			}
		}
		g.p("\t}")
		g.p("}")
		g.p("")
		g.p("// Release drops the views of x's variant into the record it was decoded")
		g.p("// from, keeping its lists' capacity for the next decode.")
		g.p("func (x *%s) Release() { x.releaseTag(x.Tag) }", name)
		g.p("")
	}
	if tagType == "u16" {
		return
	}
	g.p("func (x *%s) decode(r *reader) {", name)
	if views {
		g.p("\tprev := x.Tag")
	}
	g.p("\tx.Tag = %s(r.u8())", tag)
	if views {
		g.p("\tif prev != x.Tag {")
		g.p("\t\tx.releaseTag(prev)")
		g.p("\t}")
	}
	g.p("\tswitch x.Tag {")
	for _, v := range vs {
		g.p("\tcase %s%s:", tag, v.Name)
		if len(v.Fields) > 0 {
			g.p("\t\tx.%s.decode(r)", v.Name)
		}
	}
	g.p("\tdefault:")
	g.p("\t\tr.fail(ErrBadTag)")
	g.p("\t}")
	g.p("}")
	g.p("")
	g.p("func (x *%s) encode(w *writer) {", name)
	g.p("\tw.u8(uint8(x.Tag))")
	g.p("\tswitch x.Tag {")
	for _, v := range vs {
		g.p("\tcase %s%s:", tag, v.Name)
		if len(v.Fields) > 0 {
			g.p("\t\tx.%s.encode(w)", v.Name)
		}
	}
	g.p("\tdefault:")
	g.p("\t\tw.fail(ErrBadTag)")
	g.p("\t}")
	g.p("}")
	g.p("")
}

func (g *goGen) root(name string, vs []Variant) {
	g.union(name, vs, "u16")
	tag := name + "Tag"
	g.p("// Decode reads one whole %s record into x, reusing x's slices. Byte", strings.ToLower(name))
	g.p("// fields alias b, which must stay unchanged while they are used.")
	g.p("func (x *%s) Decode(b []byte) error {", name)
	g.p("\tif len(b) > MaxRecord {")
	g.p("\t\treturn ErrBound")
	g.p("\t}")
	g.p("\tr := reader{b: b}")
	views := g.variantsViews(vs)
	if views {
		g.p("\tprev := x.Tag")
	}
	g.p("\tx.Tag = %s(r.u16())", tag)
	if views {
		g.p("\tif prev != x.Tag {")
		g.p("\t\tx.releaseTag(prev)")
		g.p("\t}")
	}
	g.p("\tif r.u16() != 0 && r.err == nil {")
	g.p("\t\treturn ErrReserved")
	g.p("\t}")
	g.p("\tif r.err != nil {")
	g.p("\t\treturn r.err")
	g.p("\t}")
	g.p("\tswitch x.Tag {")
	for _, v := range vs {
		g.p("\tcase %s%s:", tag, v.Name)
		if len(v.Fields) > 0 {
			g.p("\t\tx.%s.decode(&r)", v.Name)
		}
	}
	g.p("\tdefault:")
	g.p("\t\treturn ErrBadTag")
	g.p("\t}")
	g.p("\treturn r.finish()")
	g.p("}")
	g.p("")
	g.p("// Encode appends x as one record. It fails, leaving dst's contents past")
	g.p("// its old length unspecified, when a field exceeds its bound.")
	g.p("func (x *%s) Encode(dst []byte) ([]byte, error) {", name)
	g.p("\tw := writer{b: dst}")
	g.p("\tstart := len(dst)")
	g.p("\tw.u16(uint16(x.Tag))")
	g.p("\tw.u16(0)")
	g.p("\tswitch x.Tag {")
	for _, v := range vs {
		g.p("\tcase %s%s:", tag, v.Name)
		if len(v.Fields) > 0 {
			g.p("\t\tx.%s.encode(&w)", v.Name)
		}
	}
	g.p("\tdefault:")
	g.p("\t\treturn dst, ErrBadTag")
	g.p("\t}")
	g.p("\tif w.err == nil && len(w.b)-start > MaxRecord {")
	g.p("\t\tw.err = ErrTooLarge")
	g.p("\t}")
	g.p("\treturn w.b, w.err")
	g.p("}")
	g.p("")
}

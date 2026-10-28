package main

import (
	"fmt"
	"strings"
)

var rustKeywords = map[string]bool{
	"as": true, "box": true, "break": true, "const": true, "continue": true, "crate": true,
	"else": true, "enum": true, "extern": true, "fn": true, "for": true, "if": true,
	"impl": true, "in": true, "let": true, "loop": true, "match": true, "mod": true,
	"move": true, "mut": true, "pub": true, "ref": true, "return": true, "static": true,
	"struct": true, "super": true, "trait": true, "type": true, "unsafe": true, "use": true,
	"where": true, "while": true, "async": true, "await": true, "dyn": true, "gen": true,
}

func rustField(name string) string {
	if rustKeywords[name] {
		return "r#" + name
	}
	return name
}

type rustGen struct {
	s   *Schema
	b   strings.Builder
	tmp int
}

func (g *rustGen) p(format string, args ...any) {
	fmt.Fprintf(&g.b, format, args...)
	g.b.WriteByte('\n')
}

func (g *rustGen) fresh(prefix string) string {
	g.tmp++
	return fmt.Sprintf("%s%d", prefix, g.tmp)
}

func rustType(t *Type) string {
	switch t.Kind {
	case kPrim:
		return t.Prim
	case kStr:
		return "String"
	case kBytes:
		return "Vec<u8>"
	case kPath:
		return "PathBuf"
	case kBlob:
		return "BlobRef"
	case kOpt:
		return "Option<" + rustType(t.Elem) + ">"
	case kList, kChunk:
		return "Vec<" + rustType(t.Elem) + ">"
	case kNamed:
		return t.Name
	}
	panic(t.Kind)
}

// put writes the statements that encode the value behind the reference e.
func (g *rustGen) put(e string, t *Type, path, indent string) {
	switch t.Kind {
	case kPrim:
		g.p("%sw.%s(*%s);", indent, t.Prim, e)
	case kStr, kBytes, kPath:
		g.p("%sw.%s(%s, %d, %q)?;", indent, t.Kind, e, t.Max, path)
	case kBlob:
		g.p("%sw.blob(%s);", indent, e)
	case kOpt:
		v := g.fresh("v")
		g.p("%sif let Some(%s) = %s {", indent, v, e)
		g.p("%s    w.u8(1);", indent)
		g.put(v, t.Elem, path, indent+"    ")
		g.p("%s} else {", indent)
		g.p("%s    w.u8(0);", indent)
		g.p("%s}", indent)
	case kList, kChunk:
		v := g.fresh("v")
		g.p("%sw.count(%s.len(), %d, %q)?;", indent, e, t.Max, path)
		start := ""
		if t.Kind == kChunk {
			start = g.fresh("start")
			g.p("%slet %s = w.len();", indent, start)
		}
		g.p("%sfor %s in %s.iter() {", indent, v, e)
		g.put(v, t.Elem, path, indent+"    ")
		g.p("%s}", indent)
		if t.Kind == kChunk {
			g.p("%sw.budget(%s, %d, %q)?;", indent, start, t.Budget, path)
		}
	case kNamed:
		g.p("%s%s.put(w)?;", indent, e)
	}
}

// get is an expression that decodes one t.
func (g *rustGen) get(t *Type) string {
	switch t.Kind {
	case kPrim:
		return fmt.Sprintf("r.%s()?", t.Prim)
	case kStr, kBytes, kPath:
		return fmt.Sprintf("r.%s(%d)?", t.Kind, t.Max)
	case kBlob:
		return "r.blob()?"
	case kOpt:
		return fmt.Sprintf("if r.flag()? { Some(%s) } else { None }", g.get(t.Elem))
	case kList:
		return fmt.Sprintf("{ let n = r.count(%d, %d)?; let mut v = Vec::with_capacity(n); for _ in 0..n { v.push(%s); } v }",
			t.Max, g.s.typeMin(t.Elem), g.get(t.Elem))
	case kChunk:
		// The encoder refuses a chunk over its byte budget, so the decoder
		// must too: what decodes must re-encode.
		return fmt.Sprintf("{ let n = r.count(%d, %d)?; let start = r.pos(); let mut v = Vec::with_capacity(n); for _ in 0..n { v.push(%s); } r.budget(start, %d)?; v }",
			t.Max, g.s.typeMin(t.Elem), g.get(t.Elem), t.Budget)
	case kNamed:
		return t.Name + "::get(r)?"
	}
	panic(t.Kind)
}

// length is an expression for the encoded length of the value behind e.
func (g *rustGen) length(e string, t *Type) string {
	switch t.Kind {
	case kPrim:
		return fmt.Sprint(primSize[t.Prim])
	case kStr, kBytes:
		return fmt.Sprintf("(4 + %s.len())", e)
	case kPath:
		return fmt.Sprintf("(4 + %s.as_os_str().len())", e)
	case kBlob:
		return "16"
	case kOpt:
		v := g.fresh("v")
		return fmt.Sprintf("%s.as_ref().map_or(1, |%s| 1 + %s)", e, v, g.length(v, t.Elem))
	case kList, kChunk:
		v := g.fresh("v")
		return fmt.Sprintf("(4 + %s.iter().map(|%s| %s).sum::<usize>())", e, v, g.length(v, t.Elem))
	case kNamed:
		return e + ".wire_len()"
	}
	panic(t.Kind)
}

func (g *rustGen) fieldsDecl(fields []Field, indent, vis string) {
	for _, f := range fields {
		g.p("%s%s%s: %s,", indent, vis, rustField(f.Name), rustType(f.T))
	}
}

// bindings is `{ a: x_a, b: x_b }` for matching a variant by reference.
func bindings(fields []Field) string {
	if len(fields) == 0 {
		return ""
	}
	var parts []string
	for _, f := range fields {
		parts = append(parts, fmt.Sprintf("%s: x_%s", rustField(f.Name), f.Name))
	}
	return " { " + strings.Join(parts, ", ") + " }"
}

func (g *rustGen) variantGet(prefix string, v Variant) string {
	if len(v.Fields) == 0 {
		return prefix + v.Name
	}
	var parts []string
	for _, f := range v.Fields {
		parts = append(parts, fmt.Sprintf("%s: %s", rustField(f.Name), g.get(f.T)))
	}
	return prefix + v.Name + " { " + strings.Join(parts, ", ") + " }"
}

func (g *rustGen) variantLen(v Variant) string {
	parts := []string{"0"}
	for _, f := range v.Fields {
		parts = append(parts, g.length("x_"+f.Name, f.T))
	}
	return strings.Join(parts, " + ")
}

func genRust(s *Schema) string {
	g := &rustGen{s: s}
	g.p("// @generated by gnui/tools/abigen from zfcore/abi/zf.schema. Do not edit;")
	g.p("// change the schema and run `go run ./tools/abigen` in gnui/.")
	g.p("")
	g.p("#![allow(clippy::all, clippy::pedantic, unused_parens, unused_variables, dead_code)]")
	g.p("")
	g.p("use std::path::PathBuf;")
	g.p("")
	g.p("use crate::wire::{BlobRef, Malformed, Reader, TooLarge, Wire, Writer};")
	g.p("")
	g.p("/// The ABI version, `ZF_ABI`.")
	g.p("pub const ABI: u32 = %d;", s.ABI)
	g.p("/// FNV-1a 64 of the schema without comments or blank lines.")
	g.p("pub const SCHEMA_HASH: u64 = 0x%016x;", s.Hash)
	g.p("/// `ZF_MAX_RECORD`: no record is longer.")
	g.p("pub const MAX_RECORD: usize = %d;", s.MaxRecord)
	g.p("")
	for _, d := range s.Decls {
		switch d.Kind {
		case "enum":
			g.p("#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash)]")
			g.p("#[repr(u8)]")
			g.p("pub enum %s {", d.Name)
			for i, v := range d.Variants {
				if i == 0 {
					g.p("    #[default]")
				}
				g.p("    %s = %d,", v.Name, v.Tag)
			}
			g.p("}")
			g.p("")
			g.p("impl Wire for %s {", d.Name)
			g.p("    fn put(&self, w: &mut Writer) -> Result<(), TooLarge> {")
			g.p("        w.u8(*self as u8);")
			g.p("        Ok(())")
			g.p("    }")
			g.p("    fn get(r: &mut Reader<'_>) -> Result<Self, Malformed> {")
			g.p("        Ok(match r.u8()? {")
			for _, v := range d.Variants {
				g.p("            %d => Self::%s,", v.Tag, v.Name)
			}
			g.p("            _ => return Err(r.bad(%q)),", d.Name)
			g.p("        })")
			g.p("    }")
			g.p("    fn wire_len(&self) -> usize {")
			g.p("        1")
			g.p("    }")
			g.p("}")
		case "struct":
			g.p("#[derive(Clone, Debug, PartialEq)]")
			g.p("pub struct %s {", d.Name)
			g.fieldsDecl(d.Fields, "    ", "pub ")
			g.p("}")
			g.p("")
			g.p("impl Wire for %s {", d.Name)
			g.p("    fn put(&self, w: &mut Writer) -> Result<(), TooLarge> {")
			for _, f := range d.Fields {
				g.put("(&self."+rustField(f.Name)+")", f.T, d.Name+"."+f.Name, "        ")
			}
			g.p("        Ok(())")
			g.p("    }")
			g.p("    fn get(r: &mut Reader<'_>) -> Result<Self, Malformed> {")
			g.p("        Ok(%s)", g.variantGet("Self", Variant{Name: "", Fields: d.Fields}))
			g.p("    }")
			g.p("    fn wire_len(&self) -> usize {")
			parts := []string{"0"}
			for _, f := range d.Fields {
				parts = append(parts, g.length("(&self."+rustField(f.Name)+")", f.T))
			}
			g.p("        %s", strings.Join(parts, " + "))
			g.p("    }")
			g.p("}")
		case "union":
			g.p("#[derive(Clone, Debug, PartialEq)]")
			g.p("pub enum %s {", d.Name)
			for _, v := range d.Variants {
				if len(v.Fields) == 0 {
					g.p("    %s,", v.Name)
					continue
				}
				g.p("    %s {", v.Name)
				g.fieldsDecl(v.Fields, "        ", "")
				g.p("    },")
			}
			g.p("}")
			g.p("")
			g.p("impl Wire for %s {", d.Name)
			g.p("    fn put(&self, w: &mut Writer) -> Result<(), TooLarge> {")
			g.p("        match self {")
			for _, v := range d.Variants {
				g.p("            Self::%s%s => {", v.Name, bindings(v.Fields))
				g.p("                w.u8(%d);", v.Tag)
				for _, f := range v.Fields {
					g.put("x_"+f.Name, f.T, d.Name+"."+v.Name+"."+f.Name, "                ")
				}
				g.p("            }")
			}
			g.p("        }")
			g.p("        Ok(())")
			g.p("    }")
			g.p("    fn get(r: &mut Reader<'_>) -> Result<Self, Malformed> {")
			g.p("        Ok(match r.u8()? {")
			for _, v := range d.Variants {
				g.p("            %d => %s,", v.Tag, g.variantGet("Self::", v))
			}
			g.p("            _ => return Err(r.bad(%q)),", d.Name)
			g.p("        })")
			g.p("    }")
			g.p("    fn wire_len(&self) -> usize {")
			g.p("        1 + match self {")
			for _, v := range d.Variants {
				g.p("            Self::%s%s => %s,", v.Name, bindings(v.Fields), g.variantLen(v))
			}
			g.p("        }")
			g.p("    }")
			g.p("}")
		}
		g.p("")
	}
	g.root("Event", s.Events)
	g.root("Command", s.Commands)
	g.samples()
	return g.b.String()
}

func (g *rustGen) root(name string, vs []Variant) {
	g.p("#[derive(Clone, Debug, PartialEq)]")
	g.p("pub enum %s {", name)
	for _, v := range vs {
		if len(v.Fields) == 0 {
			g.p("    %s,", v.Name)
			continue
		}
		g.p("    %s {", v.Name)
		g.fieldsDecl(v.Fields, "        ", "")
		g.p("    },")
	}
	g.p("}")
	g.p("")
	upper := strings.ToUpper(name)
	g.p("/// Every %s variant: its tag, its name and its largest encoding.", strings.ToLower(name))
	g.p("pub const %s_VARIANTS: &[(u16, &str, usize)] = &[", upper)
	for _, v := range vs {
		g.p("    (%d, %q, %d),", v.Tag, v.Name, g.s.recordMax(v))
	}
	g.p("];")
	g.p("")
	for _, v := range vs {
		for _, f := range v.Fields {
			if f.T.Kind == kChunk {
				g.p("/// `%s::%s.%s`: at most this many elements and encoded bytes per record.", name, v.Name, f.Name)
				g.p("pub const %s_%s_%s: (usize, usize) = (%d, %d);", upper, strings.ToUpper(snake(v.Name)), strings.ToUpper(f.Name), f.T.Max, f.T.Budget)
			}
		}
	}
	g.p("")
	g.p("impl %s {", name)
	g.p("    pub fn tag(&self) -> u16 {")
	g.p("        match self {")
	for _, v := range vs {
		g.p("            Self::%s%s => %d,", v.Name, ignore(v.Fields), v.Tag)
	}
	g.p("        }")
	g.p("    }")
	g.p("")
	g.p("    pub fn name(&self) -> &'static str {")
	g.p("        Self::tag_name(self.tag()).unwrap_or(\"?\")")
	g.p("    }")
	g.p("")
	g.p("    pub fn tag_name(tag: u16) -> Option<&'static str> {")
	g.p("        %s_VARIANTS.iter().find(|v| v.0 == tag).map(|v| v.1)", upper)
	g.p("    }")
	g.p("")
	g.p("    /// Encodes one record into `out`, replacing what it held.")
	g.p("    pub fn encode(&self, out: &mut Vec<u8>) -> Result<(), TooLarge> {")
	g.p("        let mut w = Writer::reuse(out);")
	g.p("        w.u16(self.tag());")
	g.p("        w.u16(0);")
	g.p("        let result = self.put_fields(&mut w);")
	g.p("        *out = w.finish();")
	g.p("        result?;")
	g.p("        if out.len() > MAX_RECORD {")
	g.p("            debug_assert!(false, \"{} exceeded MAX_RECORD despite its bounds\", self.name());")
	g.p("            return Err(TooLarge { field: \"record\", len: out.len(), max: MAX_RECORD });")
	g.p("        }")
	g.p("        Ok(())")
	g.p("    }")
	g.p("")
	g.p("    fn put_fields(&self, w: &mut Writer) -> Result<(), TooLarge> {")
	g.p("        match self {")
	for _, v := range vs {
		g.p("            Self::%s%s => {", v.Name, bindings(v.Fields))
		for _, f := range v.Fields {
			g.put("x_"+f.Name, f.T, name+"."+v.Name+"."+f.Name, "                ")
		}
		g.p("            }")
	}
	g.p("        }")
	g.p("        Ok(())")
	g.p("    }")
	g.p("")
	g.p("    /// Decodes one whole record.")
	g.p("    pub fn decode(buf: &[u8]) -> Result<Self, Malformed> {")
	g.p("        if buf.len() > MAX_RECORD {")
	g.p("            return Err(Malformed { reason: \"a record over MAX_RECORD\", at: MAX_RECORD });")
	g.p("        }")
	g.p("        let mut reader = Reader::new(buf);")
	g.p("        let r = &mut reader;")
	g.p("        let tag = r.u16()?;")
	g.p("        if r.u16()? != 0 {")
	g.p("            return Err(r.bad(\"reserved header bits\"));")
	g.p("        }")
	g.p("        let value = match tag {")
	for _, v := range vs {
		g.p("            %d => %s,", v.Tag, g.variantGet("Self::", v))
	}
	g.p("            _ => return Err(r.bad(%q)),", name+" tag")
	g.p("        };")
	g.p("        r.finish()?;")
	g.p("        Ok(value)")
	g.p("    }")
	g.p("")
	g.p("    /// The length `encode` produces.")
	g.p("    pub fn wire_len(&self) -> usize {")
	g.p("        4 + match self {")
	for _, v := range vs {
		g.p("            Self::%s%s => %s,", v.Name, bindings(v.Fields), g.variantLen(v))
	}
	g.p("        }")
	g.p("    }")
	g.p("}")
	g.p("")
}

func ignore(fields []Field) string {
	if len(fields) == 0 {
		return ""
	}
	return " { .. }"
}

func snake(camel string) string {
	var b strings.Builder
	for i, c := range camel {
		if c >= 'A' && c <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(c + 'a' - 'A')
		} else {
			b.WriteRune(c)
		}
	}
	return b.String()
}

// samples writes deterministic values of every type: `typical` ones that
// cover each union variant over enough rounds, and `max` ones at every
// field's bound, for the fixtures and the bound tests.
func (g *rustGen) samples() {
	s := g.s
	g.p("/// Deterministic sample values for fixtures and bound tests.")
	g.p("#[cfg(test)]")
	g.p("pub mod samples {")
	g.p("    use super::*;")
	g.p("    use crate::wire::sample::Seed;")
	g.p("")
	for _, d := range s.Decls {
		g.p("    pub fn %s(s: &mut Seed) -> %s {", snake(d.Name), d.Name)
		switch d.Kind {
		case "enum":
			g.p("        match s.pick(%d) {", len(d.Variants))
			for i, v := range d.Variants {
				g.p("            %d => %s::%s,", i, d.Name, v.Name)
			}
			g.p("            _ => unreachable!(),")
			g.p("        }")
		case "struct":
			g.p("        %s", g.sampleCtor(d.Name, d.Fields))
		case "union":
			biggest, size := 0, -1
			for i, v := range d.Variants {
				if m, _ := s.fieldsMax(v.Fields, map[string]bool{}); m > size {
					biggest, size = i, m
				}
			}
			g.p("        let k = if s.max { %d } else { s.pick(%d) };", biggest, len(d.Variants))
			g.p("        match k {")
			for i, v := range d.Variants {
				g.p("            %d => %s,", i, g.sampleCtor(d.Name+"::"+v.Name, v.Fields))
			}
			g.p("            _ => unreachable!(),")
			g.p("        }")
		}
		g.p("    }")
		g.p("")
	}
	for _, root := range []struct {
		name string
		vs   []Variant
	}{{"Event", s.Events}, {"Command", s.Commands}} {
		g.p("    /// One sample of every %s variant, with how many rounds cover its unions.", strings.ToLower(root.name))
		g.p("    pub fn %ss(s: &mut Seed) -> Vec<(%s, u64)> {", strings.ToLower(root.name), root.name)
		g.p("        vec![")
		for _, v := range root.vs {
			g.p("            (%s, %d),", g.sampleCtor(root.name+"::"+v.Name, v.Fields), s.unionWidth(v.Fields))
		}
		g.p("        ]")
		g.p("    }")
		g.p("")
	}
	g.p("}")
}

func (g *rustGen) sampleCtor(name string, fields []Field) string {
	if len(fields) == 0 {
		return name
	}
	var parts []string
	for _, f := range fields {
		parts = append(parts, fmt.Sprintf("%s: %s", rustField(f.Name), g.sampleExpr(f.T)))
	}
	return name + " { " + strings.Join(parts, ", ") + " }"
}

func (g *rustGen) sampleExpr(t *Type) string {
	switch t.Kind {
	case kPrim:
		return fmt.Sprintf("s.%s()", t.Prim)
	case kStr, kBytes, kPath:
		return fmt.Sprintf("s.%s(%d)", t.Kind, t.Max)
	case kBlob:
		return "s.blob()"
	case kOpt:
		return fmt.Sprintf("if s.some() { Some(%s) } else { None }", g.sampleExpr(t.Elem))
	case kList:
		return fmt.Sprintf("(0..s.count(%d)).map(|_| %s).collect()", t.Max, g.sampleExpr(t.Elem))
	case kChunk:
		return fmt.Sprintf("(0..s.count(%d.min(%d / %d))).map(|_| %s).collect()", t.Max, t.Budget, g.s.mustMax(t.Elem), g.sampleExpr(t.Elem))
	case kNamed:
		return snake(t.Name) + "(s)"
	}
	panic(t.Kind)
}

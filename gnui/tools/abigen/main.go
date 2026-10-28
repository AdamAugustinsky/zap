// Command abigen generates the Go records of Zap's core ABI
// (abi/abi_gen.go) from abi/zf.schema. Run it from gnui/:
//
//	go run ./tools/abigen          write the file
//	go run ./tools/abigen -check   fail if it is stale
//	go run ./tools/abigen -sizes   list every type's and record's largest encoding
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"sort"
)

func main() {
	schemaPath := flag.String("schema", "abi/zf.schema", "the schema")
	goOut := flag.String("go", "abi/abi_gen.go", "generated Go")
	check := flag.Bool("check", false, "fail if the generated file is stale instead of writing it")
	sizes := flag.Bool("sizes", false, "print the largest encoding of every type and record")
	flag.Parse()

	src, err := os.ReadFile(*schemaPath)
	if err != nil {
		fail(err)
	}
	s, err := parseSchema(string(src))
	if *sizes && s != nil {
		printSizes(s)
	}
	if err != nil {
		fail(err)
	}
	if *sizes {
		return
	}
	goSrc, err := format.Source([]byte(genGo(s)))
	if err != nil {
		fail(fmt.Errorf("generated Go does not parse: %w", err))
	}
	outputs := []struct {
		path string
		data []byte
	}{
		{*goOut, goSrc},
	}
	stale := false
	for _, o := range outputs {
		if *check {
			have, err := os.ReadFile(o.path)
			if err != nil || !bytes.Equal(have, o.data) {
				fmt.Fprintf(os.Stderr, "abigen: %s is stale; run `go run ./tools/abigen` in gnui/\n", o.path)
				stale = true
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(o.path), 0o755); err != nil {
			fail(err)
		}
		if err := os.WriteFile(o.path, o.data, 0o644); err != nil {
			fail(err)
		}
	}
	if stale {
		os.Exit(1)
	}
}

func printSizes(s *Schema) {
	var names []string
	for _, d := range s.Decls {
		names = append(names, d.Name)
	}
	sort.Strings(names)
	for _, n := range names {
		m, _ := s.declMax(s.byName[n], map[string]bool{})
		fmt.Printf("%-24s %9d\n", n, m)
	}
	for _, root := range []struct {
		name string
		vs   []Variant
	}{{"event", s.Events}, {"command", s.Commands}} {
		for _, v := range root.vs {
			fmt.Printf("%-8s %-24s %9d\n", root.name, v.Name, s.recordMax(v))
		}
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "abigen:", err)
	os.Exit(1)
}

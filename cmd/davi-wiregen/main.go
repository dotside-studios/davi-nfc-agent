// Command davi-wiregen writes the client library's TypeScript view of the wire
// from the Go that serves it. The agent's protocol is declared once, in package
// protocol, and this turns that declaration into
// client/src/session/wire.generated.ts: the error codes, the message types, and
// an interface per payload.
//
// It exists because the same contract was previously written twice, once in Go
// and once by hand in TypeScript, with nothing keeping the two in step. Run it
// after changing anything in protocol/ and commit the result:
//
//	make types
//
// Only the wire is generated. What the client library adds over it, the
// reconnect policy, the remembered tag, the events it synthesizes, stays
// hand-written in client/src/session/types.ts.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

func main() {
	protocolDir := flag.String("protocol", "protocol", "directory of the protocol package")
	nfcDir := flag.String("nfc", "nfc", "directory of the nfc package, for types protocol aliases")
	out := flag.String("out", filepath.Join("client", "src", "session", "wire.generated.ts"), "file to write")
	flag.Parse()

	source, err := load(*protocolDir, *nfcDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "davi-wiregen: %v\n", err)
		os.Exit(1)
	}

	rendered, err := render(source)
	if err != nil {
		fmt.Fprintf(os.Stderr, "davi-wiregen: %v\n", err)
		os.Exit(1)
	}

	if err := os.WriteFile(*out, rendered, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "davi-wiregen: %v\n", err)
		os.Exit(1)
	}
}

// unionNames maps the Go types that become string-literal unions onto the
// names they are emitted under. A client reads NFCErrorCode, not ErrorCode.
var unionNames = map[string]string{
	"ErrorCode":   "NFCErrorCode",
	"MessageType": "NFCMessageType",
}

// field is one property of an emitted interface.
type field struct {
	name     string // the json name, which is what a client reads
	tsType   string
	doc      string
	optional bool // omitempty: the key may be absent
	nullable bool // a pointer that is always present, so it can be null
}

// structType is one emitted interface.
type structType struct {
	name    string
	doc     string
	extends []string // embedded structs, which Go flattens into the same object
	fields  []field
}

// constant is one member of an emitted string-literal union.
type constant struct {
	value string
	doc   string
}

// wire is everything read out of the Go source.
type wire struct {
	structs []structType
	errors  []constant
	types   []constant
}

// load reads the protocol package, following the types it aliases out of nfc.
//
// Aliases are how protocol names a type that belongs beside the thing that
// produces it: TagCapabilities is nfc's, and so is DeviceStatus. Each is
// emitted under the name protocol gives it, and a reference to the nfc name
// resolves to the same interface.
func load(protocolDir, nfcDir string) (wire, error) {
	protocolFiles, err := parseDir(protocolDir)
	if err != nil {
		return wire{}, err
	}
	nfcFiles, err := parseDir(nfcDir)
	if err != nil {
		return wire{}, err
	}

	// Aliased names first: emitting resolves references through them.
	aliases := map[string]string{} // nfc type name -> the name protocol gives it
	for _, spec := range typeSpecs(protocolFiles) {
		if spec.Assign == token.NoPos {
			continue
		}
		if sel, ok := spec.Type.(*ast.SelectorExpr); ok {
			if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "nfc" {
				aliases[sel.Sel.Name] = spec.Name.Name
			}
		}
	}

	var out wire
	names := map[string]bool{}

	for _, spec := range typeSpecs(protocolFiles) {
		if s, ok := structFor(spec, spec.Name.Name, aliases); ok {
			out.structs = append(out.structs, s)
			names[spec.Name.Name] = true
		}
	}

	// The aliased types themselves, which live in nfc.
	for _, spec := range typeSpecs(nfcFiles) {
		alias, aliased := aliases[spec.Name.Name]
		if !aliased || names[alias] {
			continue
		}
		if s, ok := structFor(spec, alias, aliases); ok {
			out.structs = append(out.structs, s)
			names[alias] = true
		}
	}

	out.errors = constantsOfType(protocolFiles, "ErrorCode")
	out.types = constantsOfType(protocolFiles, "MessageType")

	if len(out.errors) == 0 {
		return wire{}, fmt.Errorf("no ErrorCode constants found in %s", protocolDir)
	}
	return out, nil
}

func parseDir(dir string) ([]*ast.File, error) {
	set := token.NewFileSet()
	pkgs, err := parser.ParseDir(set, dir, func(info os.FileInfo) bool {
		return !strings.HasSuffix(info.Name(), "_test.go")
	}, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", dir, err)
	}

	var files []*ast.File
	for _, pkg := range pkgs {
		for _, name := range sortedKeys(pkg.Files) {
			files = append(files, pkg.Files[name])
		}
	}
	return files, nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// typeSpecs walks every type declaration, carrying the doc comment down from
// the declaration when the spec has none of its own, which is where a lone
// `type X struct` keeps it.
func typeSpecs(files []*ast.File) []*ast.TypeSpec {
	var specs []*ast.TypeSpec
	for _, file := range files {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.TYPE {
				continue
			}
			for _, spec := range gen.Specs {
				typeSpec, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				if typeSpec.Doc == nil {
					typeSpec.Doc = gen.Doc
				}
				specs = append(specs, typeSpec)
			}
		}
	}
	return specs
}

// structFor renders one struct, or reports that it is not part of the wire.
//
// A struct qualifies when every exported field carries a json tag. That is the
// rule rather than a list in this program: package protocol describes the wire,
// so a type in it with json tags is on the wire, and a new payload needs no
// edit here.
func structFor(spec *ast.TypeSpec, name string, aliases map[string]string) (structType, bool) {
	if !ast.IsExported(spec.Name.Name) {
		return structType{}, false
	}
	body, ok := spec.Type.(*ast.StructType)
	if !ok || body.Fields == nil || len(body.Fields.List) == 0 {
		return structType{}, false
	}

	out := structType{name: name, doc: docText(spec.Doc, name)}

	for _, member := range body.Fields.List {
		// An embedded struct: Go flattens its fields into the same JSON object,
		// which is what extends does in TypeScript.
		if len(member.Names) == 0 {
			embedded, ok := member.Type.(*ast.Ident)
			if !ok {
				return structType{}, false
			}
			out.extends = append(out.extends, embedded.Name)
			continue
		}

		tag := ""
		if member.Tag != nil {
			unquoted, err := strconv.Unquote(member.Tag.Value)
			if err != nil {
				return structType{}, false
			}
			tag = reflect.StructTag(unquoted).Get("json")
		}

		for _, ident := range member.Names {
			if !ast.IsExported(ident.Name) {
				continue
			}
			if tag == "" || tag == "-" {
				// An exported field with nothing saying what it is called on
				// the wire. This is not a wire type, or it is one with a hole
				// in it; either way, do not guess.
				return structType{}, false
			}

			jsonName, opts, _ := strings.Cut(tag, ",")
			if jsonName == "" {
				return structType{}, false
			}

			rendered, pointer, ok := tsTypeOf(member.Type, aliases)
			if !ok {
				return structType{}, false
			}

			optional := slicesContains(strings.Split(opts, ","), "omitempty")
			out.fields = append(out.fields, field{
				name:     jsonName,
				tsType:   rendered,
				doc:      docText(member.Doc, jsonName),
				optional: optional,
				// A pointer that is always present is how a field says null.
				// With omitempty it is absent instead, which optional says.
				nullable: pointer && !optional,
			})
		}
	}

	if len(out.fields) == 0 && len(out.extends) == 0 {
		return structType{}, false
	}
	return out, true
}

func slicesContains(haystack []string, needle string) bool {
	for _, item := range haystack {
		if item == needle {
			return true
		}
	}
	return false
}

// tsTypeOf renders a Go type as TypeScript, reporting whether it was a pointer.
// An unrecognised type fails rather than guessing: a wrong type here is worse
// than no generated file.
func tsTypeOf(expr ast.Expr, aliases map[string]string) (rendered string, pointer bool, ok bool) {
	switch node := expr.(type) {
	case *ast.StarExpr:
		inner, _, ok := tsTypeOf(node.X, aliases)
		return inner, true, ok

	case *ast.Ident:
		switch node.Name {
		case "string":
			return "string", false, true
		case "bool":
			return "boolean", false, true
		case "int", "int8", "int16", "int32", "int64",
			"uint", "uint8", "uint16", "uint32", "uint64",
			"float32", "float64", "byte", "rune":
			return "number", false, true
		case "any":
			return "unknown", false, true
		}
		if emitted, ok := unionNames[node.Name]; ok {
			return emitted, false, true
		}
		if ast.IsExported(node.Name) {
			return node.Name, false, true
		}
		return "", false, false

	case *ast.SelectorExpr:
		pkg, ok := node.X.(*ast.Ident)
		if !ok {
			return "", false, false
		}
		if pkg.Name == "nfc" {
			// Through the alias, so nfc.TagCapabilities and the protocol name
			// for it are the same interface.
			if alias, aliased := aliases[node.Sel.Name]; aliased {
				return alias, false, true
			}
			return node.Sel.Name, false, true
		}
		return "", false, false

	case *ast.ArrayType:
		// A byte slice crosses the wire base64 encoded, as a string.
		if ident, ok := node.Elt.(*ast.Ident); ok && (ident.Name == "byte" || ident.Name == "uint8") {
			return "string", false, true
		}
		inner, _, ok := tsTypeOf(node.Elt, aliases)
		if !ok {
			return "", false, false
		}
		return inner + "[]", false, true

	case *ast.MapType:
		key, _, keyOK := tsTypeOf(node.Key, aliases)
		value, _, valueOK := tsTypeOf(node.Value, aliases)
		if !keyOK || !valueOK || key != "string" {
			return "", false, false
		}
		return "Record<string, " + value + ">", false, true

	case *ast.InterfaceType:
		if node.Methods == nil || len(node.Methods.List) == 0 {
			return "unknown", false, true
		}
		return "", false, false
	}
	return "", false, false
}

// constantsOfType collects a const block declared with the named type, keeping
// each constant's doc comment. The comment is the documentation a client reads,
// so it travels with the value rather than being written again in TypeScript.
func constantsOfType(files []*ast.File, typeName string) []constant {
	var out []constant
	for _, file := range files {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok || value.Type == nil {
					continue
				}
				ident, ok := value.Type.(*ast.Ident)
				if !ok || ident.Name != typeName {
					continue
				}
				for _, expr := range value.Values {
					literal, ok := expr.(*ast.BasicLit)
					if !ok || literal.Kind != token.STRING {
						continue
					}
					unquoted, err := strconv.Unquote(literal.Value)
					if err != nil {
						continue
					}
					out = append(out, constant{value: unquoted, doc: docText(value.Doc, unquoted)})
				}
			}
		}
	}
	return out
}

// docText renders a Go doc comment as documentation for the emitted name.
//
// The Go name that opens the comment is dropped only when it is not the name
// being emitted: "ErrCodeMultipleTags reports ..." documents "MULTIPLE_TAGS",
// where the identifier is noise, but "TagDataPayload is ..." documents
// TagDataPayload, where removing it leaves a sentence with no subject.
func docText(group *ast.CommentGroup, emitted string) string {
	if group == nil {
		return ""
	}
	text := strings.TrimSpace(group.Text())
	if text == "" {
		return ""
	}

	if word, rest, found := strings.Cut(text, " "); found && isGoName(word) && !strings.EqualFold(word, emitted) {
		text = strings.ToUpper(rest[:1]) + rest[1:]
	}

	// A single word is a section header over a run of Go fields, not
	// documentation of the one it lands on.
	if !strings.ContainsAny(text, " \n") {
		return ""
	}
	return reflow(text)
}

// isGoName reports whether a word looks like the identifier a Go doc comment
// opens with, such as "ErrCodeBusy" or "TagDataPayload".
func isGoName(word string) bool {
	if word == "" || !ast.IsExported(word) {
		return false
	}
	for _, r := range word {
		if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') {
			return false
		}
	}
	return strings.ContainsAny(word[1:], "ABCDEFGHIJKLMNOPQRSTUVWXYZ")
}

// reflow rewraps a paragraph that Go wrapped for its own column width. A block
// with an indented line is left alone: that is a table or an example, and
// rewrapping it destroys the alignment.
func reflow(text string) string {
	const width = 74

	var out []string
	for _, paragraph := range strings.Split(text, "\n\n") {
		for _, line := range strings.Split(paragraph, "\n") {
			if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
				return text
			}
		}

		words := strings.Fields(paragraph)
		if len(words) == 0 {
			continue
		}

		line := words[0]
		var lines []string
		for _, word := range words[1:] {
			if len(line)+1+len(word) > width {
				lines = append(lines, line)
				line = word
				continue
			}
			line += " " + word
		}
		out = append(out, strings.Join(append(lines, line), "\n"))
	}
	return strings.Join(out, "\n\n")
}

const header = `// Code generated by cmd/davi-wiregen. DO NOT EDIT.
//
// The agent's wire protocol, from the Go that serves it. Run ` + "`make types`" + ` after
// changing anything in protocol/ and commit the result.
//
// What the client library adds over the wire is hand-written in ./types.ts.
`

func render(source wire) ([]byte, error) {
	var out bytes.Buffer
	out.WriteString(header)

	out.WriteString("\n")
	comment(&out, "", "The error codes the agent sends. Whether a retry can work is `retryable` on\nthe error itself, not a property of the code.")
	out.WriteString("export type NFCErrorCode =\n")
	for _, code := range source.errors {
		comment(&out, "  ", code.doc)
		fmt.Fprintf(&out, "  | %q\n", code.value)
	}
	out.WriteString(";\n")

	out.WriteString("\n")
	comment(&out, "", "The messages the client protocol carries. A type not listed here is answered\nwith UNKNOWN_TYPE.")
	out.WriteString("export type NFCMessageType =\n")
	for _, msg := range source.types {
		comment(&out, "  ", msg.doc)
		fmt.Fprintf(&out, "  | %q\n", msg.value)
	}
	out.WriteString(";\n")

	for _, item := range source.structs {
		out.WriteString("\n")
		comment(&out, "", item.doc)
		fmt.Fprintf(&out, "export interface %s", item.name)
		if len(item.extends) > 0 {
			fmt.Fprintf(&out, " extends %s", strings.Join(item.extends, ", "))
		}
		out.WriteString(" {\n")
		for _, member := range item.fields {
			comment(&out, "  ", member.doc)
			optional := ""
			if member.optional {
				optional = "?"
			}
			rendered := member.tsType
			if member.nullable {
				rendered += " | null"
			}
			fmt.Fprintf(&out, "  %s%s: %s;\n", member.name, optional, rendered)
		}
		out.WriteString("}\n")
	}

	return out.Bytes(), nil
}

// comment writes a doc comment, as one line when it fits and a block when it
// does not, matching how the hand-written types are formatted.
func comment(out *bytes.Buffer, indent, text string) {
	if text == "" {
		return
	}
	lines := strings.Split(text, "\n")
	if len(lines) == 1 && len(indent)+len(text)+7 <= 80 {
		fmt.Fprintf(out, "%s/** %s */\n", indent, text)
		return
	}
	fmt.Fprintf(out, "%s/**\n", indent)
	for _, line := range lines {
		if line == "" {
			fmt.Fprintf(out, "%s *\n", indent)
			continue
		}
		fmt.Fprintf(out, "%s * %s\n", indent, line)
	}
	fmt.Fprintf(out, "%s */\n", indent)
}

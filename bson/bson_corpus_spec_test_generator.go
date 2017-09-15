// +build ignore

package main

import (
	"bytes"
	"fmt"
	"go/format"
	"html/template"
	"io/ioutil"
	"log"
	"path/filepath"
	"strings"

	"github.com/10gen/mongo-go-driver/bson/internal/json"
)

func main() {
	log.SetFlags(0)
	log.SetPrefix(name + ": ")

	var g Generator

	fmt.Fprintf(&g, "// Code generated by \"%s.go\"; DO NOT EDIT\n\n", name)

	src := g.generate()

	err := ioutil.WriteFile(fmt.Sprintf("%s.go", strings.TrimSuffix(name, "_generator")), src, 0644)
	if err != nil {
		log.Fatalf("writing output: %s", err)
	}
}

// Generator holds the state of the analysis. Primarily used to buffer
// the output for format.Source.
type Generator struct {
	bytes.Buffer // Accumulated output.
}

// format returns the gofmt-ed contents of the Generator's buffer.
func (g *Generator) format() []byte {
	src, err := format.Source(g.Bytes())
	if err != nil {
		// Should never happen, but can arise when developing this code.
		// The user can compile the output to see the error.
		log.Printf("warning: internal error: invalid Go generated: %s", err)
		log.Printf("warning: compile the package to analyze the error")
		return g.Bytes()
	}
	return src
}

// EVERYTHING ABOVE IS CONSTANT BETWEEN THE GENERATORS

const name = "bson_corpus_spec_test_generator"

func (g *Generator) generate() []byte {

	testFiles, err := filepath.Glob("../../specifications/source/bson-corpus/tests/*.json")
	if err != nil {
		log.Fatalf("error reading bson-corpus files: %s", err)
	}

	tests, err := g.loadTests(testFiles)
	if err != nil {
		log.Fatalf("error loading tests: %s", err)
	}

	tmpl, err := g.getTemplate()
	if err != nil {
		log.Fatalf("error loading template: %s", err)
	}

	tmpl.Execute(&g.Buffer, tests)

	return g.format()
}

func (g *Generator) loadTests(filenames []string) ([]*testDef, error) {
	var tests []*testDef
	for _, filename := range filenames {
		test, err := g.loadTest(filename)
		if err != nil {
			return nil, err
		}

		tests = append(tests, test)
	}

	return tests, nil
}

func (g *Generator) loadTest(filename string) (*testDef, error) {
	content, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	var testDef testDef
	err = json.Unmarshal(content, &testDef)
	if err != nil {
		return nil, err
	}

	names := make(map[string]struct{})

	for i := len(testDef.Valid) - 1; i >= 0; i-- {
		if testDef.BsonType == "0x05" && testDef.Valid[i].Description == "subtype 0x02" {
			testDef.Valid = append(testDef.Valid[:i], testDef.Valid[i+1:]...)
			continue
		}

		name := cleanupFuncName(testDef.Description + "_" + testDef.Valid[i].Description)
		nameIdx := name
		j := 1
		for {
			if _, ok := names[nameIdx]; !ok {
				break
			}

			nameIdx = fmt.Sprintf("%s_%d", name, j)
		}

		names[nameIdx] = struct{}{}

		testDef.Valid[i].TestDef = &testDef
		testDef.Valid[i].Name = nameIdx
		testDef.Valid[i].StructTest = testDef.TestKey != "" &&
			(testDef.BsonType != "0x05" || strings.Contains(testDef.Valid[i].Description, "0x00")) &&
			!testDef.Deprecated
	}

	for i := len(testDef.DecodeErrors) - 1; i >= 0; i-- {
		if strings.HasPrefix(testDef.Description, "Top-level document validity") ||
			strings.Contains(testDef.DecodeErrors[i].Description, "UTF-8") {

			testDef.DecodeErrors = append(testDef.DecodeErrors[:i], testDef.DecodeErrors[i+1:]...)
			continue
		}

		name := cleanupFuncName(testDef.Description + "_" + testDef.DecodeErrors[i].Description)
		nameIdx := name
		j := 1
		for {
			if _, ok := names[nameIdx]; !ok {
				break
			}

			nameIdx = fmt.Sprintf("%s_%d", name, j)
		}
		names[nameIdx] = struct{}{}

		testDef.DecodeErrors[i].Name = nameIdx
	}

	return &testDef, nil
}

func (g *Generator) getTemplate() (*template.Template, error) {
	content := `package bson_test

import (
	"testing"

	"github.com/stretchr/testify/require"
    "github.com/10gen/mongo-go-driver/bson"
)

func testValid(t *testing.T, in []byte, expected []byte, result interface{}) {
	err := bson.Unmarshal(in, result)
	require.NoError(t, err)

	out, err := bson.Marshal(result)
	require.NoError(t, err)

	require.Equal(t, string(expected), string(out))
}

func testDecodeSkip(t *testing.T, in []byte) {
	err := bson.Unmarshal(in, &struct{}{})
	require.NoError(t, err)
}

func testDecodeError(t *testing.T, in []byte, result interface{}) {
	err := bson.Unmarshal(in, result)
	require.Error(t, err)
}

{{range .}}
{{range .Valid}}
func Test{{.Name}}(t *testing.T) {
    b, err := hex.DecodeString("{{.Bson}}")
	require.NoError(t, err)

    {{if .CanonicalBson}}
    cb, err := hex.DecodeString("{{.CanonicalBson}}")
	require.NoError(t, err)
	{{else}}
    cb := b
    {{end}}

    var resultD bson.D
	testValid(t, b, cb, &resultD)
	{{if .StructTest}}var resultS struct {
		Element {{.TestDef.GoType}} ` + "`bson:\"{{.TestDef.TestKey}}\"`" + `
	}
	testValid(t, b, cb, &resultS){{end}}

	testDecodeSkip(t, b)
}
{{end}}

{{range .DecodeErrors}}
func Test{{.Name}}(t *testing.T) {
	b, err := hex.DecodeString("{{.Bson}}")
	require.NoError(t, err)

	var resultD bson.D
	testDecodeError(t, b, &resultD)
}
{{end}}
{{end}}
`
	tmpl, err := template.New("").Parse(content)
	if err != nil {
		return nil, err
	}
	return tmpl, nil
}

func cleanupFuncName(name string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 48 && r <= 57) || (r >= 65 && r <= 90) || (r >= 97 && r <= 122) {
			return r
		}
		return '_'
	}, name)
}

type testDef struct {
	Description  string         `json:"description"`
	BsonType     string         `json:"bson_type"`
	TestKey      string         `json:"test_key"`
	Valid        []*valid       `json:"valid"`
	DecodeErrors []*decodeError `json:"decodeErrors"`
	Deprecated   bool           `json:"deprecated"`
}

func (t *testDef) GoType() string {
	switch t.BsonType {
	case "0x01":
		return "float64"
	case "0x02":
		return "string"
	case "0x03":
		return "bson.D"
	case "0x04":
		return "[]interface{}"
	case "0x05":
		return "[]byte"
	case "0x07":
		return "bson.ObjectId"
	case "0x08":
		return "bool"
	case "0x09":
		return "time.Time"
	case "0x0E":
		return "string"
	case "0x10":
		return "int32"
	case "0x12":
		return "int64"
	case "0x13":
		return "bson.Decimal"
	default:
		return "interface{}"
	}
}

type valid struct {
	Description   string `json:"description"`
	Bson          string `json:"bson"`
	CanonicalBson string `json:"canonical_bson"`

	Name       string
	StructTest bool
	TestDef    *testDef
}

type decodeError struct {
	Description string `json:"description"`
	Bson        string `json:"bson"`

	Name string
}

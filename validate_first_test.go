package jsonschema_test

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"path"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"testing"

	"github.com/tokenmaxed/jsonschema/v6"
	"github.com/tokenmaxed/jsonschema/v6/kind"
)

// --

// firstWrapper embeds the suite schema in a keyword that evaluates it
// speculatively, where a failing subschema does not decide the result.
type firstWrapper struct {
	name       string
	minVersion int
	schema     func(ref map[string]any) any
	instance   func(v any) any
}

var notSchema = map[string]any{"not": map[string]any{}}

func sameInstance(v any) any { return v }

var firstWrappers = []firstWrapper{
	{"not", 4, func(r map[string]any) any { return map[string]any{"not": r} }, sameInstance},
	{"anyOf", 4, func(r map[string]any) any { return map[string]any{"anyOf": []any{r, notSchema}} }, sameInstance},
	{"oneOf first", 4, func(r map[string]any) any { return map[string]any{"oneOf": []any{r, map[string]any{}}} }, sameInstance},
	{"oneOf second", 4, func(r map[string]any) any { return map[string]any{"oneOf": []any{map[string]any{}, r}} }, sameInstance},
	{"if", 7, func(r map[string]any) any { return map[string]any{"if": r, "then": notSchema} }, sameInstance},
	{"contains", 6, func(r map[string]any) any { return map[string]any{"contains": r} },
		func(v any) any { return []any{v} }},
	{"maxContains", 2019, func(r map[string]any) any {
		return map[string]any{"contains": r, "minContains": 0, "maxContains": 1}
	}, func(v any) any { return []any{v, v, v} }},
	{"unevaluatedProperties", 2019, func(r map[string]any) any {
		return map[string]any{"anyOf": []any{r, map[string]any{}}, "unevaluatedProperties": false}
	}, sameInstance},
	{"unevaluatedItems", 2019, func(r map[string]any) any {
		return map[string]any{"anyOf": []any{r, map[string]any{}}, "unevaluatedItems": false}
	}, sameInstance},
}

// TestValidateFirstSuites checks, for every instance of the test suites, that
// ValidateFirst returns nil exactly when Validate does, also with the schema
// inside each of firstWrappers, and that its error holds a single failure
// that Validate reports too.
func TestValidateFirstSuites(t *testing.T) {
	drafts := []struct {
		dir     string
		draft   *jsonschema.Draft
		version int
	}{
		{"draft4", jsonschema.Draft4, 4},
		{"draft6", jsonschema.Draft6, 6},
		{"draft7", jsonschema.Draft7, 7},
		{"draft2019-09", jsonschema.Draft2019, 2019},
		{"draft2020-12", jsonschema.Draft2020, 2020},
	}
	for _, suite := range []string{"./testdata/JSON-Schema-Test-Suite", "./testdata/Extra-Test-Suite"} {
		if _, err := os.Stat(path.Join(suite, "tests")); err != nil {
			t.Fatal(err) // run "git submodule update --init"
		}
		var n firstCounts
		for _, d := range drafts {
			firstDir(t, suite, d.dir, d.draft, d.version, &n)
		}
		t.Logf("%s: %d files, %d instances, %d checks", suite, n.files, n.instances, n.checks)
		if n.instances == 0 {
			t.Fatalf("%s: no instances checked", suite)
		}
	}
}

type firstCounts struct {
	files, instances, checks int
}

func firstDir(t *testing.T, suite, dpath string, draft *jsonschema.Draft, version int, n *firstCounts) {
	dir := path.Join(suite, "tests", dpath)
	ee, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ee {
		switch {
		case e.IsDir():
			firstDir(t, suite, path.Join(dpath, e.Name()), draft, version, n)
		case path.Ext(e.Name()) == ".json" && !slices.Contains(skip, e.Name()):
			firstFile(t, suite, path.Join(dpath, e.Name()), draft, version, n)
		}
	}
}

func firstFile(t *testing.T, suite, fpath string, draft *jsonschema.Draft, version int, n *firstCounts) {
	optional := strings.Contains(fpath, "/optional/")
	file, err := os.Open(path.Join(suite, "tests", fpath))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	var groups []struct {
		Description string
		Schema      any
		Tests       []struct {
			Description string
			Data        any
		}
	}
	dec := json.NewDecoder(file)
	dec.UseNumber()
	if err := dec.Decode(&groups); err != nil {
		t.Fatal(err)
	}
	n.files++

	url := "http://testsuites.com/schema.json"
	for _, group := range groups {
		c := jsonschema.NewCompiler()
		c.DefaultDraft(draft)
		if optional {
			c.AssertFormat()
			c.AssertContent()
		}
		c.UseLoader(jsonschema.SchemeURLLoader{
			"file": jsonschema.FileLoader{},
			"http": suiteRemotes(suite),
		})
		if err := c.AddResource(url, group.Schema); err != nil {
			t.Fatalf("%s: %s: add resource failed: %v", fpath, group.Description, err)
		}
		sch, err := c.Compile(url)
		if err != nil {
			t.Fatalf("%s: %s: schema compilation failed: %v", fpath, group.Description, err)
		}
		type wrapped struct {
			w   firstWrapper
			sch *jsonschema.Schema
		}
		var ws []wrapped
		for i, w := range firstWrappers {
			if version < w.minVersion {
				continue
			}
			wurl := fmt.Sprintf("http://testsuites.com/wrapper%d.json", i)
			if err := c.AddResource(wurl, w.schema(map[string]any{"$ref": url})); err != nil {
				t.Fatalf("%s: %s: %s: add resource failed: %v", fpath, group.Description, w.name, err)
			}
			wsch, err := c.Compile(wurl)
			if err != nil {
				t.Fatalf("%s: %s: %s: schema compilation failed: %v", fpath, group.Description, w.name, err)
			}
			ws = append(ws, wrapped{w, wsch})
		}

		for _, test := range group.Tests {
			n.instances++
			where := fmt.Sprintf("%s: %s: %s", fpath, group.Description, test.Description)
			checkFirst(t, where, sch, test.Data)
			n.checks++
			for _, w := range ws {
				checkFirst(t, where+": in "+w.w.name, w.sch, w.w.instance(test.Data))
				n.checks++
			}
		}
	}
}

// checkFirst compares ValidateFirst with Validate on v.
func checkFirst(t *testing.T, where string, sch *jsonschema.Schema, v any) {
	t.Helper()
	full, first := sch.Validate(v), sch.ValidateFirst(v)
	if (full == nil) != (first == nil) {
		t.Fatalf("%s:\nValidate: %v\nValidateFirst: %v", where, full, first)
	}
	if first == nil {
		return
	}
	fullErr, ok1 := full.(*jsonschema.ValidationError)
	firstErr, ok2 := first.(*jsonschema.ValidationError)
	if !ok1 || !ok2 {
		t.Fatalf("%s: got %T and %T, want *ValidationError", where, full, first)
	}

	reported := map[string]bool{}
	var index func(e *jsonschema.ValidationError)
	index = func(e *jsonschema.ValidationError) {
		reported[firstKey(e)] = true
		for _, c := range e.Causes {
			index(c)
		}
	}
	index(fullErr)

	var check func(e *jsonschema.ValidationError)
	check = func(e *jsonschema.ValidationError) {
		switch e.ErrorKind.(type) {
		case *kind.Group:
			t.Fatalf("%s: group error in ValidateFirst's error:\n%v", where, first)
		case *kind.AnyOf, *kind.OneOf:
			// one cause per subschema
		default:
			if len(e.Causes) > 1 {
				t.Fatalf("%s: %d causes in %s:\n%v", where, len(e.Causes), firstKey(e), first)
			}
		}
		if !reported[firstKey(e)] {
			t.Fatalf("%s: %s not in Validate's error:\nValidate: %v\nValidateFirst: %v", where, firstKey(e), full, first)
		}
		for _, c := range e.Causes {
			check(c)
		}
	}
	check(firstErr)
}

func firstKey(e *jsonschema.ValidationError) string {
	return fmt.Sprintf("%s %q %T", e.SchemaURL, e.InstanceLocation, e.ErrorKind)
}

// --

func compileFirst(t *testing.T, schema string) *jsonschema.Schema {
	t.Helper()
	doc, err := jsonschema.UnmarshalJSON(strings.NewReader(schema))
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("http://example.com/schema.json", doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile("http://example.com/schema.json")
	if err != nil {
		t.Fatal(err)
	}
	return sch
}

func intArray(n int) any {
	arr := make([]any, n)
	for i := range arr {
		arr[i] = i
	}
	return arr
}

func intObject(n int) any {
	obj := make(map[string]any, n)
	for i := 0; i < n; i++ {
		obj[fmt.Sprint("p", i)] = i
	}
	return obj
}

// allocs returns the heap allocations and bytes allocated by a call of f,
// measured like [testing.AllocsPerRun], as the minimum over runs calls.
// The garbage collector is off and sync.Pools (used by fmt and regexp) start
// empty, so only other goroutines can add to a call's count.
func allocs(runs int, f func()) (count, bytes uint64) {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	defer debug.SetGCPercent(debug.SetGCPercent(-1))
	f() // warm up
	count, bytes = math.MaxUint64, math.MaxUint64
	for i := 0; i < runs; i++ {
		runtime.GC()
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		f()
		runtime.ReadMemStats(&after)
		count = min(count, after.Mallocs-before.Mallocs)
		bytes = min(bytes, after.TotalAlloc-before.TotalAlloc)
	}
	return count, bytes
}

// TestValidateFirstAllocs checks that ValidateFirst's allocations do not grow
// with the number of members that fail, while Validate's do.
func TestValidateFirstAllocs(t *testing.T) {
	tests := []struct {
		name     string
		schema   string
		instance func(n int) any
	}{
		{"items", `{"items": {"type": "string"}}`, intArray},
		{"prefixItems", `{"prefixItems": [{"type": "string"}], "items": {"type": "string"}}`, intArray},
		{"draft-07 items", `{"$schema": "http://json-schema.org/draft-07/schema#", "items": [{"type": "string"}], "additionalItems": {"type": "string"}}`, intArray},
		{"contains with maxContains", `{"contains": {"type": "integer"}, "maxContains": 1}`, intArray},
		{"patternProperties", `{"patternProperties": {"^p": {"type": "string"}}}`, intObject},
		{"additionalProperties schema", `{"additionalProperties": {"type": "string"}}`, intObject},
		{"additionalProperties false", `{"additionalProperties": false}`, intObject},
		{"propertyNames", `{"propertyNames": {"maxLength": 1}}`, intObject},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sch := compileFirst(t, test.schema)
			small, large := test.instance(1000), test.instance(100_000)
			for _, v := range []any{small, large} {
				if sch.Validate(v) == nil || sch.ValidateFirst(v) == nil {
					t.Fatal("instance must be invalid")
				}
			}
			firstSmall, firstSmallBytes := allocs(3, func() { _ = sch.ValidateFirst(small) })
			firstLarge, firstLargeBytes := allocs(3, func() { _ = sch.ValidateFirst(large) })
			fullSmall, fullSmallBytes := allocs(3, func() { _ = sch.Validate(small) })
			fullLarge, fullLargeBytes := allocs(1, func() { _ = sch.Validate(large) })
			t.Logf("1,000 and 100,000 members: ValidateFirst %d and %d allocations (%d and %d bytes), Validate %d and %d (%d and %d bytes)",
				firstSmall, firstLarge, firstSmallBytes, firstLargeBytes, fullSmall, fullLarge, fullSmallBytes, fullLargeBytes)
			if firstLarge != firstSmall || firstLargeBytes != firstSmallBytes || firstLarge > 100 {
				t.Errorf("ValidateFirst: want equal allocations for both sizes, at most 100")
			}
			if fullLarge <= fullSmall || fullLargeBytes < 50*fullSmallBytes {
				t.Errorf("Validate: want allocations growing with the failing members")
			}
		})
	}
}

// TestValidateFirstValid checks that on valid instances ValidateFirst does
// the same work as Validate, except in subschemas that fail without deciding
// the result, where it stops at their first error. The schemas avoid
// keywords that build maps or use a sync.Pool (through fmt or regexp), whose
// allocations vary between calls.
func TestValidateFirstValid(t *testing.T) {
	objects := make([]any, 10_000)
	for i := range objects {
		objects[i] = map[string]any{"a": i}
	}
	tests := []struct {
		name     string
		schema   string
		instance any
		less     bool // a subschema fails speculatively
	}{
		{"items", `{"items": {"type": "integer"}}`, intArray(10_000), false},
		{"contains", `{"contains": {"type": "integer"}}`, intArray(10_000), false},
		{"properties", `{"additionalProperties": {"type": "integer"}, "propertyNames": {"minLength": 2}}`, intObject(10_000), false},
		{"nested", `{"items": {"properties": {"a": {"type": "integer"}}, "required": ["a"]}}`, objects, false},
		{"anyOf with a failing subschema", `{"anyOf": [{"items": {"type": "string"}}, {"items": {"type": "integer"}}]}`, intArray(10_000), true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sch := compileFirst(t, test.schema)
			if err := sch.ValidateFirst(test.instance); err != nil {
				t.Fatal(err)
			}
			first, firstBytes := allocs(3, func() { _ = sch.ValidateFirst(test.instance) })
			full, fullBytes := allocs(3, func() { _ = sch.Validate(test.instance) })
			t.Logf("ValidateFirst %d allocations (%d bytes), Validate %d (%d bytes)", first, firstBytes, full, fullBytes)
			if test.less && (first >= full || firstBytes >= fullBytes) {
				t.Error("want ValidateFirst to allocate less")
			}
			if !test.less && (first != full || firstBytes != fullBytes) {
				t.Error("want equal allocations")
			}
		})
	}
}

func ExampleSchema_ValidateFirst() {
	schema, err := jsonschema.UnmarshalJSON(strings.NewReader(`{"items": {"type": "string"}}`))
	if err != nil {
		log.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("http://example.com/schema.json", schema); err != nil {
		log.Fatal(err)
	}
	sch, err := c.Compile("http://example.com/schema.json")
	if err != nil {
		log.Fatal(err)
	}

	instance := []any{1, 2, 3}
	fmt.Println(sch.Validate(instance))
	fmt.Println(sch.ValidateFirst(instance))
	// Output:
	// jsonschema validation failed with 'http://example.com/schema.json#'
	// - at '/0': got number, want string
	// - at '/1': got number, want string
	// - at '/2': got number, want string
	// jsonschema validation failed with 'http://example.com/schema.json#'
	// - at '/0': got number, want string
}

package jsonschema_test

import (
	"fmt"
	"log"
	"slices"
	"strings"
	"testing"

	"github.com/tokenmaxed/jsonschema/v6"
	"github.com/tokenmaxed/jsonschema/v6/kind"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

// TestValidateMaxSuites checks, for every instance of the test suites and
// several maximums, that ValidateMax returns nil exactly when Validate does,
// also with the schema inside each of firstWrappers; that its leaf errors are
// leaf errors of Validate's error, at most max of them; and that it equals
// Validate's error when that has at most max leaf errors.
func TestValidateMaxSuites(t *testing.T) {
	maxCut, maxEqual = 0, 0
	walkSuites(t, checkMax)
	t.Logf("ValidateMax calls: %d with fewer leaf errors than Validate, %d compared for equality", maxCut, maxEqual)
	if maxCut == 0 || maxEqual == 0 {
		t.Fatal("both cases must be checked")
	}
}

// maxCut and maxEqual count the checkMax comparisons with max below and at
// least the number of Validate's leaf errors.
var maxCut, maxEqual int

var maxPrinter = message.NewPrinter(language.English)

// canonical renders e with sorted causes and sorted property lists, as map
// order makes both vary between calls.
func canonical(e *jsonschema.ValidationError) string {
	if k, ok := e.ErrorKind.(*kind.AdditionalProperties); ok {
		slices.Sort(k.Properties)
	}
	var causes []string
	for _, c := range e.Causes {
		causes = append(causes, canonical(c))
	}
	slices.Sort(causes)
	return fmt.Sprintf("{%s %q %T %q [%s]}", e.SchemaURL, e.InstanceLocation, e.ErrorKind,
		e.ErrorKind.LocalizedString(maxPrinter), strings.Join(causes, " "))
}

func forLeaves(e *jsonschema.ValidationError, f func(*jsonschema.ValidationError)) {
	if len(e.Causes) == 0 {
		f(e)
	}
	for _, c := range e.Causes {
		forLeaves(c, f)
	}
}

// checkMax compares ValidateMax with Validate on v.
func checkMax(t *testing.T, where string, sch *jsonschema.Schema, v any) {
	t.Helper()
	full := sch.Validate(v)
	var fullErr *jsonschema.ValidationError
	fullLeaves := map[string]bool{}
	nfull := 0
	if full != nil {
		fullErr = full.(*jsonschema.ValidationError)
		forLeaves(fullErr, func(e *jsonschema.ValidationError) {
			fullLeaves[firstKey(e)] = true
			nfull++
		})
	}
	for _, max := range []int{1, 2, 3, 100} {
		got := sch.ValidateMax(v, max)
		if (full == nil) != (got == nil) {
			t.Fatalf("%s: max %d:\nValidate: %v\nValidateMax: %v", where, max, full, got)
		}
		if got == nil {
			continue
		}
		gotErr, ok := got.(*jsonschema.ValidationError)
		if !ok {
			t.Fatalf("%s: max %d: got %T, want *ValidationError", where, max, got)
		}
		n := 0
		forLeaves(gotErr, func(e *jsonschema.ValidationError) {
			n++
			if !fullLeaves[firstKey(e)] {
				t.Fatalf("%s: max %d: leaf %s is not a leaf of Validate's error:\nValidate: %v\nValidateMax: %v",
					where, max, firstKey(e), full, got)
			}
		})
		if n > max {
			t.Fatalf("%s: max %d: %d leaf errors:\n%v", where, max, n, got)
		}
		if max < nfull {
			maxCut++
			continue
		}
		maxEqual++
		if canonical(gotErr) != canonical(fullErr) {
			t.Fatalf("%s: max %d, Validate has %d leaf errors, but the errors differ:\nValidate: %v\nValidateMax: %v",
				where, max, nfull, full, got)
		}
	}
}

// itemsExt is a SchemaExt that validates the items of an array with sch, and
// reports each error, or with some, passes if any item is valid.
type itemsExt struct {
	sch  *jsonschema.Schema
	some bool
}

type noValidItem struct{}

func (*noValidItem) KeywordPath() []string                   { return []string{"someItem"} }
func (*noValidItem) LocalizedString(*message.Printer) string { return "no item is valid" }

func (e *itemsExt) Validate(ctx *jsonschema.ValidatorContext, v any) {
	arr, ok := v.([]any)
	if !ok || len(arr) == 0 {
		return
	}
	var errors []*jsonschema.ValidationError
	for i, item := range arr {
		err := ctx.Validate(e.sch, item, []string{fmt.Sprint(i)})
		switch {
		case err == nil && e.some:
			return
		case err == nil:
		case e.some:
			errors = append(errors, err.(*jsonschema.ValidationError))
		default:
			ctx.AddErr(err)
		}
	}
	if e.some {
		ctx.AddErrors(errors, &noValidItem{})
	}
}

func itemsVocab(t *testing.T) *jsonschema.Vocabulary {
	url := "http://example.com/meta/items"
	meta := compileFirst(t, `{"properties": {
		"eachItem": {"$ref": "https://json-schema.org/draft/2020-12/schema"},
		"someItem": {"$ref": "https://json-schema.org/draft/2020-12/schema"}
	}}`)
	return &jsonschema.Vocabulary{
		URL:        url,
		Schema:     meta,
		Subschemas: []jsonschema.SchemaPath{{jsonschema.Prop("eachItem")}, {jsonschema.Prop("someItem")}},
		Compile: func(ctx *jsonschema.CompilerContext, obj map[string]any) (jsonschema.SchemaExt, error) {
			for _, kw := range []string{"eachItem", "someItem"} {
				if _, ok := obj[kw]; ok {
					return &itemsExt{ctx.Enqueue([]string{kw}), kw == "someItem"}, nil
				}
			}
			return nil, nil
		},
	}
}

// TestValidateMaxExtension checks ValidateMax with a SchemaExt that reports
// the errors of its subschemas, or discards them.
func TestValidateMaxExtension(t *testing.T) {
	someValid := intArray(1000).([]any)
	someValid[999] = "s"
	tests := []struct {
		schema   string
		instance any
	}{
		{`{"eachItem": {"type": "string"}}`, intArray(1000)},
		{`{"eachItem": {"type": "string"}}`, intArray(3)},
		{`{"someItem": {"type": "string"}}`, intArray(1000)},
		{`{"someItem": {"type": "string"}}`, intArray(3)},
		{`{"someItem": {"type": "string"}}`, someValid},
		// The discarded errors must not stop the checks that follow.
		{`{"someItem": {"type": "string"}, "unevaluatedItems": false}`, someValid},
	}
	for _, test := range tests {
		doc, err := jsonschema.UnmarshalJSON(strings.NewReader(test.schema))
		if err != nil {
			t.Fatal(err)
		}
		c := jsonschema.NewCompiler()
		c.AssertVocabs()
		c.RegisterVocabulary(itemsVocab(t))
		if err := c.AddResource("http://example.com/schema.json", doc); err != nil {
			t.Fatal(err)
		}
		sch, err := c.Compile("http://example.com/schema.json")
		if err != nil {
			t.Fatal(err)
		}
		checkMax(t, test.schema, sch, test.instance)
	}
}

// TestValidateMaxAllocs checks that ValidateMax's allocations do not grow
// with the number of members that fail once max of them failed, while
// Validate's do.
func TestValidateMaxAllocs(t *testing.T) {
	tests := []struct {
		name     string
		schema   string
		instance func(n int) any
	}{
		{"items", `{"items": {"type": "string"}}`, intArray},
		{"prefixItems", `{"prefixItems": [{"type": "string"}], "items": {"type": "string"}}`, intArray},
		{"draft-07 items", `{"$schema": "http://json-schema.org/draft-07/schema#", "items": [{"type": "string"}], "additionalItems": {"type": "string"}}`, intArray},
		// No patternProperties: under the race detector, regexp's sync.Pool
		// makes the allocations of 100 matches vary between calls.
		{"additionalProperties schema", `{"additionalProperties": {"type": "string"}}`, intObject},
		{"propertyNames", `{"propertyNames": {"maxLength": 1}}`, intObject},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sch := compileFirst(t, test.schema)
			small, large := test.instance(1000), test.instance(100_000)
			for _, v := range []any{small, large} {
				err := sch.ValidateMax(v, 100)
				if err == nil {
					t.Fatal("instance must be invalid")
				}
				n := 0
				forLeaves(err.(*jsonschema.ValidationError), func(*jsonschema.ValidationError) { n++ })
				if n != 100 {
					t.Fatalf("got %d leaf errors, want 100", n)
				}
			}
			maxSmall, maxSmallBytes := allocs(3, func() { _ = sch.ValidateMax(small, 100) })
			maxLarge, maxLargeBytes := allocs(3, func() { _ = sch.ValidateMax(large, 100) })
			fullSmall, fullSmallBytes := allocs(3, func() { _ = sch.Validate(small) })
			fullLarge, fullLargeBytes := allocs(1, func() { _ = sch.Validate(large) })
			t.Logf("1,000 and 100,000 members: ValidateMax(100) %d and %d allocations (%d and %d bytes), Validate %d and %d (%d and %d bytes)",
				maxSmall, maxLarge, maxSmallBytes, maxLargeBytes, fullSmall, fullLarge, fullSmallBytes, fullLargeBytes)
			if maxLarge != maxSmall || maxLargeBytes != maxSmallBytes {
				t.Errorf("ValidateMax: want equal allocations for both sizes")
			}
			if fullLarge <= fullSmall || fullLargeBytes < 50*fullSmallBytes {
				t.Errorf("Validate: want allocations growing with the failing members")
			}
		})
	}
}

func TestValidateMaxPanics(t *testing.T) {
	sch := compileFirst(t, `{}`)
	defer func() {
		if recover() == nil {
			t.Error("ValidateMax(v, 0) did not panic")
		}
	}()
	_ = sch.ValidateMax(1, 0)
}

func ExampleSchema_ValidateMax() {
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

	fmt.Println(sch.ValidateMax([]any{1, 2, 3}, 2))
	// Output:
	// jsonschema validation failed with 'http://example.com/schema.json#'
	// - at '/0': got number, want string
	// - at '/1': got number, want string
}

package schema

import (
	"math"
	"math/big"
	"regexp"
	"runtime"
	"slices"
	"sync"
	"testing"
)

func TestBasicTypeChecks(t *testing.T) {
	checks := []struct {
		name  string
		check Check
	}{
		{"string", isString},
		{"number", isNumber},
		{"integer", isInteger},
		{"boolean", isBoolean},
		{"null", isNull},
		{"array", isArray},
		{"bytes", isBytes},
		{"bigint", isBigInt},
		{"undefined", isUndefined},
		{"object", isObject},
	}
	cases := []struct {
		name  string
		value any
		want  []string
	}{
		{"string", "x", []string{"string"}},
		{"integer", 42, []string{"number", "integer"}},
		{"integer float", 42.0, []string{"number", "integer"}},
		{"fraction", 1.5, []string{"number"}},
		{"float32 fraction", float32(1.5), []string{"number"}},
		{"negative zero", math.Copysign(0, -1), []string{"number", "integer"}},
		{"NaN", math.NaN(), nil},
		{"positive infinity", math.Inf(1), nil},
		{"negative infinity", math.Inf(-1), nil},
		{"float32 NaN", float32(math.NaN()), nil},
		{"float32 infinity", float32(math.Inf(1)), nil},
		{"boolean", false, []string{"boolean"}},
		{"null", nil, []string{"null"}},
		{"array", []any{1}, []string{"array"}},
		{"bytes", []byte{1}, []string{"bytes"}},
		{"bigint", big.NewInt(42), []string{"bigint"}},
		{"nil bigint", (*big.Int)(nil), nil},
		{"undefined", Undefined, []string{"undefined"}},
		{"object", map[string]any{"x": 1}, []string{"object"}},
		{"other Go value", struct{}{}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			for _, entry := range checks {
				if entry.check(tc.value) {
					got = append(got, entry.name)
				}
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("matched types = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNativeIntegerChecks(t *testing.T) {
	for _, value := range []any{
		int(-1), int8(-1), int16(-1), int32(-1), int64(math.MinInt64),
		uint(1), uint8(1), uint16(1), uint32(1), uint64(math.MaxUint64), uintptr(1),
		float32(1), float64(1),
	} {
		if !isNumber(value) || !isInteger(value) {
			t.Errorf("rejected %T(%v) as an integer", value, value)
		}
	}
}

func TestLeafMetadata(t *testing.T) {
	first := leaf(7, "first", func(v any) bool { return v == "first" })
	second := leaf(7, "second", func(v any) bool { return v == "second" })
	for _, check := range []compiledCheck{first, second} {
		info := leafOf(check)
		if info.Code != 7 || !check.check(info.Arg) || !leafOk(info.Code, info.Arg, info.Arg) {
			t.Fatal("check and leaf metadata disagree")
		}
	}
	if first.check("second") || second.check("first") {
		t.Fatal("closures must retain independent arguments")
	}
	plain := plainCheck(isObject)
	if info := leafOf(plain); info.Code != 0 || info.Arg != nil {
		t.Fatal("a plain check must have the zero leaf value")
	}
}

func TestLeafOk(t *testing.T) {
	bounds := Bounds{Min: 1, Max: 4, Xmin: 1, Xmax: 4}
	cases := []struct {
		name string
		code int
		arg  any
		good any
		bad  any
	}{
		{"string", 1, nil, "text", 42},
		{"number", 2, nil, 1.5, math.Inf(1)},
		{"integer", 3, nil, 42.0, 1.5},
		{"boolean", 4, nil, false, 0},
		{"null", 5, nil, nil, Undefined},
		{"constant", 7, 42, 42.0, "42"},
		{"bigint constant", 7, big.NewInt(42), big.NewInt(42), 42},
		{"undefined constant", 7, Undefined, Undefined, nil},
		{"enum", 8, []any{"x", 42, nil}, 42.0, false},
		{"type mask", 9, 17, nil, false},
		{"integer bounds", 10, bounds, uint8(2), 2.5},
		{"number bounds", 11, bounds, 2.5, 4},
		{"string bounds", 12, Bounds{Min: 2, Max: 3}, "a😀", "😀"},
		{"pattern", 13, regexp.MustCompile("^a"), "abc", "bac"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !leafOk(tc.code, tc.good, tc.arg) || leafOk(tc.code, tc.bad, tc.arg) {
				t.Fatal("leaf accepted a bad value or rejected a good value")
			}
		})
	}
	for _, code := range []int{0, 6} {
		if !leafOk(code, nil, nil) {
			t.Errorf("code %d must accept every value", code)
		}
	}
	if leafOk(7, math.NaN(), math.NaN()) || leafOk(8, math.NaN(), []any{math.NaN()}) {
		t.Fatal("constant and small primitive enum checks use strict equality for NaN")
	}
	for _, code := range []int{10, 11} {
		for _, value := range []any{"2", big.NewInt(2), math.NaN(), math.Inf(1), 1, 4} {
			if leafOk(code, value, bounds) {
				t.Errorf("bounded leaf %d accepted %v", code, value)
			}
		}
	}
	if leafOk(12, 42, Bounds{}) || leafOk(13, 42, regexp.MustCompile(".*")) {
		t.Fatal("string leaves must reject non-strings")
	}
}

func TestTypeBits(t *testing.T) {
	cases := []struct {
		value any
		want  int
	}{
		{"x", 1}, {1.5, 2}, {42, 6}, {42.0, 6},
		{math.NaN(), 0}, {math.Inf(1), 0},
		{false, 8}, {nil, 16}, {[]any{}, 32},
		{map[string]any{}, 64}, {[]byte{}, 128},
		{big.NewInt(1), 256}, {(*big.Int)(nil), 0},
		{Undefined, 512}, {struct{}{}, 0},
	}
	for _, tc := range cases {
		if got := typeBits(tc.value); got != tc.want {
			t.Errorf("typeBits(%v) = %d, want %d", tc.value, got, tc.want)
		}
	}
}

func TestInBounds(t *testing.T) {
	cases := []struct {
		name   string
		value  float64
		bounds Bounds
		want   bool
	}{
		{"inclusive minimum", 1, Bounds{1, 3, 0, 4}, true},
		{"inclusive maximum", 3, Bounds{1, 3, 0, 4}, true},
		{"below minimum", 0.5, Bounds{1, 3, 0, 4}, false},
		{"above maximum", 3.5, Bounds{1, 3, 0, 4}, false},
		{"exclusive minimum", 1, Bounds{0, 4, 1, 3}, false},
		{"exclusive maximum", 3, Bounds{0, 4, 1, 3}, false},
		{"inside exclusive bounds", 2, Bounds{0, 4, 1, 3}, true},
		{"NaN", math.NaN(), Bounds{0, 4, -1, 5}, false},
		{"unbounded", 1e100, Bounds{math.Inf(-1), math.Inf(1), math.Inf(-1), math.Inf(1)}, true},
		{"infinity", math.Inf(1), Bounds{math.Inf(-1), math.Inf(1), math.Inf(-1), math.Inf(1)}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := inBounds(tc.value, tc.bounds); got != tc.want {
				t.Fatalf("inBounds = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCodePointsAndLengthOk(t *testing.T) {
	cases := []struct {
		text   string
		points int
	}{
		{"", 0},
		{"abc", 3},
		{"中文", 2},
		{"😀", 1},
		{"e\u0301", 2},
		{"a😀中", 3},
		{"👨‍👩‍👧‍👦", 7},
	}
	limits := []float64{-1, 0, 0.5, 1, 1.5, 2, 3, 4, 7, 8, math.Inf(1), math.NaN()}
	for _, tc := range cases {
		if got := codePoints(tc.text); got != tc.points {
			t.Errorf("codePoints(%q) = %d, want %d", tc.text, got, tc.points)
		}
		// Compare the byte-length fast paths against the known code point count.
		for _, min := range limits {
			for _, max := range limits {
				want := !(float64(tc.points) < min) && float64(tc.points) <= max
				if got := lengthOk(tc.text, min, max); got != want {
					t.Errorf("lengthOk(%q, %v, %v) = %v, want %v", tc.text, min, max, got, want)
				}
			}
		}
	}
}

func TestDecimal(t *testing.T) {
	// Coefficients and exponents follow JavaScript's decimal notation thresholds.
	cases := []struct {
		name        string
		value       float64
		coefficient string
		exponent    int
	}{
		{"zero", 0, "0", 0},
		{"negative zero", math.Copysign(0, -1), "0", 0},
		{"negative fraction", -1.25, "125", -2},
		{"fraction", 0.0075, "75", -4},
		{"smallest fixed notation", 1e-6, "1", -6},
		{"small exponential notation", 1e-7, "1", -7},
		{"million", 1e6, "1000000", 0},
		{"large fixed notation", 1e20, "100000000000000000000", 0},
		{"large exponential notation", 1e21, "1", 21},
		{"rounded integer", 1e23, "1", 23},
		{"subnormal", math.SmallestNonzeroFloat64, "5", -324},
		{"largest finite float", math.MaxFloat64, "17976931348623157", 292},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			coefficient, exponent := decimal(tc.value)
			if coefficient == nil || coefficient.String() != tc.coefficient || exponent != tc.exponent {
				t.Fatalf("decimal = (%v, %d), want (%s, %d)", coefficient, exponent, tc.coefficient, tc.exponent)
			}
		})
	}
}

func TestDeepEqual(t *testing.T) {
	object := map[string]any{"x": math.NaN()}
	array := []any{math.NaN()}
	cycle := map[string]any{}
	cycle["self"] = cycle
	large := new(big.Int).Lsh(big.NewInt(1), 100)
	cases := []struct {
		name string
		a, b any
		want bool
	}{
		{"null", nil, nil, true},
		{"undefined", Undefined, Undefined, true},
		{"null vs undefined", nil, Undefined, false},
		{"string", "a", "a", true},
		{"different strings", "a", "b", false},
		{"boolean", false, false, true},
		{"boolean vs number", false, 0, false},
		{"string vs number", "1", 1, false},
		{"native numbers", 1, 1.0, true},
		{"float32", float32(1.5), 1.5, true},
		{"negative zero", math.Copysign(0, -1), 0, true},
		{"NaN", math.NaN(), math.NaN(), false},
		{"infinity", math.Inf(1), math.Inf(1), true},
		{"bigints", large, new(big.Int).Set(large), true},
		{"different bigints", large, new(big.Int).Add(large, big.NewInt(1)), false},
		{"bigint vs number", big.NewInt(1), 1, false},
		{"array order", []any{1, 2}, []any{2, 1}, false},
		{"array length", []any{1}, []any{1, 2}, false},
		{"array vs object", []any{1}, map[string]any{"0": 1}, false},
		{"empty array vs object", []any{}, map[string]any{}, false},
		{"object key order", map[string]any{"a": 1, "b": []any{2}}, map[string]any{"b": []any{2.0}, "a": 1.0}, true},
		{"object length", map[string]any{"a": 1}, map[string]any{"a": 1, "b": 2}, false},
		{"different keys", map[string]any{"a": nil}, map[string]any{"b": nil}, false},
		{"missing vs null", map[string]any{"a": nil}, map[string]any{}, false},
		{"missing vs undefined", map[string]any{"a": Undefined}, map[string]any{}, false},
		{"undefined property", map[string]any{"a": Undefined}, map[string]any{"a": Undefined}, true},
		{"object identity", object, object, true},
		{"different objects with NaN", object, map[string]any{"x": math.NaN()}, false},
		{"array identity", array, array, true},
		{"different arrays with NaN", array, []any{math.NaN()}, false},
		{"cyclic object identity", cycle, cycle, true},
		{"bytes", []byte{1, 2}, []byte{1, 2}, true},
		{"different bytes", []byte{1, 2}, []byte{1, 3}, false},
		{"bytes vs array", []byte{1, 2}, []any{1, 2}, false},
		{"bytes vs indexed object", []byte{1, 2}, map[string]any{"0": 1, "1": 2}, true},
		{"bytes vs wrong keys", []byte{1}, map[string]any{"1": 1}, false},
		{"empty bytes vs object", []byte{}, map[string]any{}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := deepEqual(tc.a, tc.b); got != tc.want {
				t.Fatalf("deepEqual = %v, want %v", got, tc.want)
			}
			if got := deepEqual(tc.b, tc.a); got != tc.want {
				t.Fatalf("reverse deepEqual = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCheckCombinations(t *testing.T) {
	cases := []struct {
		name    string
		factory func([]compiledCheck) compiledCheck
	}{
		{"and", and},
		{"anyOf", anyCheck},
		{"oneOf", oneCheck},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Exercise every truth table through five checks, covering all and branches.
			for count := 0; count <= 5; count++ {
				for mask := 0; mask < 1<<count; mask++ {
					var calls []int
					checks := make([]compiledCheck, count)
					matches := 0
					for i := range checks {
						accept := mask&(1<<i) != 0
						if accept {
							matches++
						}
						checks[i] = plainCheck(func(v any) bool {
							if v != "input" {
								t.Fatalf("input = %v, want input", v)
							}
							calls = append(calls, i)
							return accept
						})
					}
					var want bool
					switch tc.name {
					case "and":
						want = matches == count
					case "anyOf":
						want = matches > 0
					case "oneOf":
						want = matches == 1
					}
					if got := tc.factory(checks).check("input"); got != want {
						t.Fatalf("count=%d mask=%b: got %v, want %v", count, mask, got, want)
					}
					var wantCalls []int
					accepted := 0
					for i := 0; i < count; i++ {
						wantCalls = append(wantCalls, i)
						accept := mask&(1<<i) != 0
						if accept {
							accepted++
						}
						if tc.name == "and" && !accept || tc.name == "anyOf" && accept || tc.name == "oneOf" && accepted == 2 {
							break
						}
					}
					if !slices.Equal(calls, wantCalls) {
						t.Fatalf("count=%d mask=%b: call order %v, want %v", count, mask, calls, wantCalls)
					}
				}
			}
		})
	}
}

func TestNotCheck(t *testing.T) {
	check := notCheck(typeChecks["string"])
	if check.check("text") || !check.check(42) {
		t.Fatal("not must invert the supplied check")
	}
}

func TestIfCheck(t *testing.T) {
	var calls []string
	cond := func(v any) bool {
		calls = append(calls, "if")
		return isString(v)
	}
	then := func(v any) bool {
		calls = append(calls, "then")
		return v == "yes"
	}
	otherwise := func(v any) bool {
		calls = append(calls, "else")
		return v == 42
	}
	check := ifCheck(plainCheck(cond), plainCheck(then), plainCheck(otherwise))
	for _, tc := range []struct {
		input  any
		want   bool
		branch string
	}{
		{"yes", true, "then"},
		{"no", false, "then"},
		{42, true, "else"},
		{0, false, "else"},
	} {
		calls = nil
		if got := check.check(tc.input); got != tc.want || !slices.Equal(calls, []string{"if", tc.branch}) {
			t.Fatalf("input=%v: result=%v calls=%v", tc.input, got, calls)
		}
	}
}

func TestIndirect(t *testing.T) {
	var slot compiledCheck
	check := indirect(&slot)
	slot = typeChecks["string"]
	if !check.check("text") || check.check(42) {
		t.Fatal("indirect did not use the assigned check")
	}
	slot = typeChecks["integer"]
	if check.check("text") || !check.check(42) {
		t.Fatal("indirect did not read the updated slot")
	}

	// A recursive array schema connects its reference after creating the indirection.
	slot = anyCheck([]compiledCheck{
		typeChecks["integer"],
		listCheck(GuardStrict, 0, math.Inf(1), nil, check),
	})
	if !check.check([]any{1, []any{2, 3}}) || check.check([]any{1, []any{"bad"}}) {
		t.Fatal("recursive indirection returned the wrong result")
	}
}

func TestTypeFactories(t *testing.T) {
	for name, base := range typeChecks {
		bit := base.typeBit
		single := typeCheck([]any{name})
		masked := maskCheck(bit)
		for _, value := range []any{"x", 1, 1.5, true, nil, []any{}, map[string]any{}, []byte{}, big.NewInt(1), Undefined, math.NaN(), math.Inf(1)} {
			want := typeBits(value)&bit != 0
			if single.check(value) != want || masked.check(value) != want || !sameLeafResult(single, value) || !sameLeafResult(masked, value) {
				t.Fatalf("type %q disagrees for %v", name, value)
			}
		}
	}
	for _, types := range [][]any{nil, {}, {nil}, {"unknown"}, {[]any{}}, {map[string]any{}}} {
		if typeCheck(types).check(nil) {
			t.Fatalf("types %v accepted null", types)
		}
	}
	if got := typeMask([]any{"number", "integer", "number", "unknown", []any{}}); got != 6 {
		t.Fatalf("typeMask = %d, want 6", got)
	}
	union := typeCheck([]any{"string", "null"})
	if !union.check("x") || !union.check(nil) || union.check(1) || leafOf(union).Code != 9 {
		t.Fatal("type union or metadata disagrees")
	}
}

func TestMaskedAnyFactory(t *testing.T) {
	if leafOf(maskedAnyCheck(1, nil)).Code != 9 {
		t.Fatal("a mask without remaining checks must retain its leaf")
	}
	calls := 0
	check := maskedAnyCheck(1, []compiledCheck{plainCheck(func(v any) bool { calls++; return v == 42 }), plainCheck(never)})
	if !check.check("x") || calls != 0 {
		t.Fatal("a matching type must short circuit remaining checks")
	}
	if !check.check(42) || calls != 1 || check.check(false) {
		t.Fatal("remaining checks returned the wrong result")
	}
	if leafOf(check).Code != 0 || !maskedAnyCheck(0, []compiledCheck{typeChecks["boolean"]}).check(true) {
		t.Fatal("remaining checks or metadata disagrees")
	}
}

func TestBytesFactory(t *testing.T) {
	check := bytesCheck(1, 2)
	if !check.check([]byte{1}) || !check.check([]byte{1, 2}) || check.check([]byte{}) || check.check([]byte{1, 2, 3}) || check.check([]any{1}) {
		t.Fatal("bytes type or length bounds disagree")
	}
	if !bytesCheck(0, math.Inf(1)).check([]byte{}) || bytesCheck(math.NaN(), 2).check([]byte{1}) {
		t.Fatal("bytes numeric bounds disagree")
	}
}

func TestStringFactory(t *testing.T) {
	cases := []struct {
		name  string
		check compiledCheck
		code  int
		good  any
		bad   any
	}{
		{"strict length", stringCheck(true, 2, 3, nil, nil), 12, "a😀", "😀"},
		{"loose length", stringCheck(false, 2, 3, nil, nil), 0, 42, "😀"},
		{"strict pattern", stringCheck(true, 0, math.Inf(1), regexp.MustCompile("^a"), nil), 13, "abc", 42},
		{"loose pattern", stringCheck(false, 0, math.Inf(1), regexp.MustCompile("^a"), nil), 0, 42, "bac"},
		{"combined", stringCheck(true, 2, 3, regexp.MustCompile("^a"), func(s string) bool { return s != "abc" }), 0, "ab", "abc"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if leafOf(tc.check).Code != tc.code || !tc.check.check(tc.good) || tc.check.check(tc.bad) {
				t.Fatal("string check or metadata disagrees")
			}
			for _, value := range []any{tc.good, tc.bad, nil, "", "abc", "a😀"} {
				if !sameLeafResult(tc.check, value) {
					t.Fatalf("leaf disagrees for %v", value)
				}
			}
		})
	}
	calls := 0
	check := stringCheck(false, 2, 3, regexp.MustCompile("^a"), func(string) bool { calls++; return true })
	if !check.check(42) || check.check("a") || check.check("bb") || calls != 0 {
		t.Fatal("format must run only after type, length and pattern checks")
	}
	if !check.check("ab") || calls != 1 {
		t.Fatal("format was not called for a matching string")
	}
}

func TestNumberFactory(t *testing.T) {
	inf := math.Inf(1)
	large, _ := new(big.Int).SetString("9007199254740993", 10)
	cases := []struct {
		name  string
		check compiledCheck
		code  int
		good  any
		bad   any
	}{
		{"loose", numberCheck(0, 0, 4, -inf, inf, nil), 0, "unrelated", -1},
		{"number", numberCheck(1, 0, 4, -inf, inf, nil), 11, 1.5, big.NewInt(2)},
		{"integer", numberCheck(2, 0, 4, -inf, inf, nil), 10, int8(2), 1.5},
		{"bigint", numberCheck(3, 0, 4, -inf, inf, nil), 0, big.NewInt(2), 2},
		{"exclusive", numberCheck(1, 0, 4, 1, 3, nil), 11, 2, 3},
		{"multiple", numberCheck(1, 0, 4, -inf, inf, 0.1), 0, 0.3, 0.31},
		{"bigint bounds", numberCheck(0, big.NewInt(1), big.NewInt(3), -inf, inf, nil), 0, 1.5, 0.5},
		{"mixed bigint value", numberCheck(0, 1.5, 3.5, -inf, inf, nil), 0, big.NewInt(2), big.NewInt(1)},
		{"precise bigint", numberCheck(3, large, large, -inf, inf, nil), 0, new(big.Int).Set(large), big.NewInt(9007199254740992)},
		{"float below bigint minimum", numberCheck(0, large, inf, -inf, inf, nil), 0, big.NewInt(9007199254740994), float64(9007199254740992)},
		{"bigint above float maximum", numberCheck(0, -inf, float64(9007199254740992), -inf, inf, nil), 0, big.NewInt(9007199254740992), new(big.Int).Set(large)},
		{"bigint multiple", numberCheck(3, -inf, inf, -inf, inf, big.NewInt(3)), 0, big.NewInt(9), big.NewInt(10)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if leafOf(tc.check).Code != tc.code || !tc.check.check(tc.good) || tc.check.check(tc.bad) {
				t.Fatal("number check or metadata disagrees")
			}
			for _, value := range []any{tc.good, tc.bad, nil, "2", math.NaN(), inf, -inf} {
				if !sameLeafResult(tc.check, value) {
					t.Fatalf("leaf disagrees for %v", value)
				}
			}
		})
	}
	for mode := 0; mode <= 3; mode++ {
		check := numberCheck(mode, -inf, inf, -inf, inf, nil)
		for _, value := range []any{math.NaN(), inf, -inf} {
			if check.check(value) {
				t.Errorf("mode %d accepted %v", mode, value)
			}
		}
	}
	for _, check := range []compiledCheck{
		numberCheck(0, math.NaN(), inf, -inf, inf, nil),
		numberCheck(0, -inf, math.NaN(), -inf, inf, nil),
	} {
		if check.check(big.NewInt(1)) || check.check(1) {
			t.Fatal("NaN bounds must be unordered")
		}
	}
}

func TestIsMultiple(t *testing.T) {
	large, _ := new(big.Int).SetString("9007199254740993", 10)
	before := large.String()
	cases := []struct {
		v, d any
		want bool
	}{
		{0.0075, 0.0001, true}, {0.3, 0.1, true}, {0.31, 0.1, false},
		{-0.3, 0.1, true}, {0.3, -0.1, true}, {0, 0.1, true}, {1, 0, false}, {0, 0, false},
		{1e-100, 1e-101, true}, {1e100, 1e99, true},
		{math.SmallestNonzeroFloat64, math.SmallestNonzeroFloat64, true},
		{math.NaN(), 1, false}, {math.Inf(1), 1, false}, {1, math.Inf(1), true},
		{large, 3, true}, {large, big.NewInt(3), true},
		{big.NewInt(3), 0.5, false}, {1.5, big.NewInt(1), false},
		{big.NewInt(3), big.NewInt(0), false}, {big.NewInt(0), big.NewInt(0), false},
		{big.NewInt(-9), big.NewInt(3), true}, {9, big.NewInt(-3), true},
		{math.Inf(1), big.NewInt(1), false}, {big.NewInt(1), math.NaN(), false},
	}
	for _, tc := range cases {
		if got := isMultiple(tc.v, tc.d); got != tc.want {
			t.Errorf("isMultiple(%v, %v) = %v, want %v", tc.v, tc.d, got, tc.want)
		}
	}
	if large.String() != before {
		t.Fatal("multipleOf must not mutate bigint arguments")
	}
}

func TestConstAndEnumFactories(t *testing.T) {
	object := map[string]any{"x": []any{1}}
	cases := []struct {
		name  string
		check compiledCheck
		code  int
		good  any
		bad   any
	}{
		{"constant", constCheck(42), 7, 42.0, "42"},
		{"bigint constant", constCheck(big.NewInt(42)), 7, big.NewInt(42), 42},
		{"object constant", constCheck(object), 0, map[string]any{"x": []any{1.0}}, map[string]any{}},
		{"bytes constant", constCheck([]byte{1}), 0, []byte{1}, []any{1}},
		{"small enum", enumCheck([]any{nil, "x", 42, big.NewInt(2), Undefined}), 8, 42.0, false},
		{"mixed enum", enumCheck([]any{42, object}), 0, map[string]any{"x": []any{1.0}}, false},
		{"empty enum", enumCheck(nil), 8, nil, nil},
		{"large enum", enumCheck([]any{0, 1, 2, 3, 4, 5, 6, 7, 8}), 0, float64(8), "8"},
		{"large bigint enum", enumCheck([]any{0, 1, 2, 3, 4, 5, 6, 7, big.NewInt(8)}), 0, big.NewInt(8), 8},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if leafOf(tc.check).Code != tc.code || tc.check.check(tc.bad) {
				t.Fatal("constant or enum metadata/result disagrees")
			}
			if tc.name != "empty enum" && !tc.check.check(tc.good) {
				t.Fatal("constant or enum rejected a member")
			}
			for _, value := range []any{tc.good, tc.bad, nil, math.NaN(), object} {
				if !sameLeafResult(tc.check, value) {
					t.Fatalf("leaf disagrees for %v", value)
				}
			}
		})
	}
	if constCheck(math.NaN()).check(math.NaN()) || enumCheck([]any{math.NaN()}).check(math.NaN()) {
		t.Fatal("constant and small enum use strict equality for NaN")
	}
	for _, values := range [][]any{
		{math.NaN(), 0, 1, 2, 3, 4, 5, 6, 7},
		{math.NaN(), map[string]any{}},
	} {
		if !enumCheck(values).check(math.NaN()) {
			t.Fatal("the set lookup must match NaN")
		}
	}
	if !enumCheck([]any{[]byte{1}}).check(map[string]any{"0": 1}) {
		t.Fatal("object enum must preserve typed-array equality")
	}
	if !enumCheck([]any{math.Copysign(0, -1), 1, 2, 3, 4, 5, 6, 7, 8}).check(0.0) {
		t.Fatal("Set equality must match positive and negative zero")
	}
}

func sameLeafResult(check compiledCheck, value any) bool {
	info := leafOf(check)
	return info.Code == 0 || check.check(value) == leafOk(info.Code, value, info.Arg)
}

func TestContainerGuards(t *testing.T) {
	for _, tc := range []struct {
		name    string
		factory func(compiledCheck, bool) compiledCheck
		value   any
		other   any
	}{
		{"object", guardObject, map[string]any{}, []any{}},
		{"array", guardArray, []any{}, map[string]any{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			inner := plainCheck(func(any) bool { calls++; return false })
			if tc.factory(inner, true).check(tc.other) || !tc.factory(inner, false).check(tc.other) || calls != 0 {
				t.Fatal("guard must classify other types without calling inner")
			}
			for _, strict := range []bool{false, true} {
				if tc.factory(inner, strict).check(tc.value) {
					t.Fatal("guard must return the inner result for its container type")
				}
			}
			if calls != 2 {
				t.Fatal("inner check was not called")
			}
		})
	}
}

func TestObjectCoreChecks(t *testing.T) {
	entries := []propertyEntry{
		{key: "n", check: typeCheck([]any{"integer"}), required: true},
		{key: "s", check: typeCheck([]any{"string"})},
		{key: "u", check: typeCheck([]any{"undefined"}), required: true},
	}
	for _, guard := range []Guard{GuardNone, GuardStrict, GuardLoose} {
		for _, closed := range []bool{false, true} {
			declared := declaredObject(entries, 2, closed, guard)
			var additional compiledCheck
			if closed {
				additional = plainCheck(never)
			}
			open := openObject(entries, nil, nil, additional, guard)
			for _, tc := range []struct {
				value map[string]any
				want  bool
			}{
				{map[string]any{"n": 1, "u": Undefined}, true},
				{map[string]any{"n": 1, "u": Undefined, "s": "x"}, true},
				{map[string]any{"n": 1, "u": Undefined, "s": Undefined}, true},
				{map[string]any{"n": 1, "u": Undefined, "s": nil}, false},
				{map[string]any{"n": 1, "u": Undefined, "extra": nil}, !closed},
				{map[string]any{"n": 1}, false},
				{map[string]any{"u": Undefined}, false},
				{map[string]any{"n": Undefined, "u": Undefined}, false},
				{map[string]any{"n": nil, "u": Undefined}, false},
			} {
				if declared.check(tc.value) != tc.want || open.check(tc.value) != tc.want {
					t.Fatalf("guard=%d closed=%v value=%v: object checks disagree", guard, closed, tc.value)
				}
			}
			if guard != GuardNone {
				for _, v := range []any{nil, 1, []any{}, []byte{}} {
					want := guard == GuardLoose
					if declared.check(v) != want || open.check(v) != want {
						t.Fatalf("guard=%d misclassified %v", guard, v)
					}
				}
			}
		}
	}
	// Go map keys such as __proto__ and constructor are ordinary own properties.
	for _, key := range []string{"__proto__", "constructor", "toString"} {
		entry := []propertyEntry{{key: key, check: typeCheck([]any{"null"}), required: true}}
		for _, check := range []compiledCheck{
			declaredObject(entry, 1, true, GuardStrict),
			openObject(entry, nil, nil, plainCheck(never), GuardStrict),
		} {
			if !check.check(map[string]any{key: nil}) || check.check(map[string]any{}) {
				t.Fatalf("own property %q was misclassified", key)
			}
		}
	}
}

func TestOpenObjectPatterns(t *testing.T) {
	entries := []propertyEntry{{key: "xz", check: typeCheck([]any{"integer"})}}
	patterns := []patternEntry{
		{regexp.MustCompile("^x"), typeChecks["number"]},
		{regexp.MustCompile("z$"), plainCheck(func(v any) bool { n, ok := numericValue(v); return ok && n >= 0 })},
	}
	check := openObject(entries, []string{"extra"}, patterns, typeChecks["boolean"], GuardStrict)
	for _, tc := range []struct {
		value map[string]any
		want  bool
	}{
		{map[string]any{"xz": 1, "extra": true}, true},
		{map[string]any{"x": 1.5, "extra": false}, true},
		{map[string]any{"xz": 1.5, "extra": true}, false},
		{map[string]any{"xz": -1, "extra": true}, false},
		{map[string]any{"xz": "x", "extra": true}, false},
		{map[string]any{"xz": 1}, false},
		{map[string]any{"xz": 1, "extra": 1}, false},
		{map[string]any{"xz": 1, "extra": true, "other": false}, true},
		{map[string]any{"xz": 1, "extra": true, "other": nil}, false},
	} {
		if got := check.check(tc.value); got != tc.want {
			t.Fatalf("openObject(%v) = %v, want %v", tc.value, got, tc.want)
		}
	}
	// Extra required names check presence, including an explicitly undefined value.
	presence := openObject(nil, []string{"x"}, nil, compiledCheck{}, GuardStrict)
	if !presence.check(map[string]any{"x": Undefined}) || presence.check(map[string]any{}) {
		t.Fatal("extra required key must test presence")
	}
}

func TestObjectExtras(t *testing.T) {
	names := propertyNamesCheck(plainCheck(func(v any) bool { return len(v.(string)) > 0 }))
	count := propertyCountCheck(1, 2)
	required := dependentRequiredCheck([]requiredDependency{{"a", []string{"b", "c"}}})
	calls := 0
	schema := dependentSchemasCheck([]schemaDependency{{"a", plainCheck(func(v any) bool {
		calls++
		return isString(v.(map[string]any)["b"])
	})}})
	if !names.check(map[string]any{"a": 1}) || names.check(map[string]any{"": 1}) || !names.check(map[string]any{}) {
		t.Fatal("property names disagree")
	}
	if !count.check(map[string]any{"a": 1}) || !count.check(map[string]any{"a": 1, "b": 2}) || count.check(map[string]any{}) || count.check(map[string]any{"a": 1, "b": 2, "c": 3}) {
		t.Fatal("property count bounds disagree")
	}
	if !required.check(map[string]any{}) || !required.check(map[string]any{"a": Undefined, "b": nil, "c": Undefined}) || required.check(map[string]any{"a": nil, "b": 1}) {
		t.Fatal("dependentRequired must check own key presence")
	}
	if !schema.check(map[string]any{"b": 1}) || calls != 0 || !schema.check(map[string]any{"a": Undefined, "b": "x"}) || schema.check(map[string]any{"a": nil, "b": 1}) || calls != 2 {
		t.Fatal("dependentSchemas must check the whole object when its key is present")
	}
}

func TestArrayChecks(t *testing.T) {
	for _, rest := range []compiledCheck{typeCheck([]any{"integer"}), plainCheck(isInteger)} {
		for _, guard := range []Guard{GuardNone, GuardStrict, GuardLoose} {
			check := listCheck(guard, 1, 3, []compiledCheck{typeChecks["string"]}, rest)
			for _, tc := range []struct {
				value any
				want  bool
			}{
				{[]any{}, false}, {[]any{"x"}, true}, {[]any{"x", 1, 2}, true},
				{[]any{"x", 1, 2, 3}, false}, {[]any{1}, false}, {[]any{"x", 1.5}, false},
				{nil, guard == GuardLoose}, {map[string]any{}, guard == GuardLoose},
			} {
				if got := check.check(tc.value); got != tc.want {
					t.Fatalf("list guard=%d value=%v: got %v, want %v", guard, tc.value, got, tc.want)
				}
			}
		}
	}
	prefix := []compiledCheck{typeChecks["string"], typeChecks["integer"]}
	if !itemsCheck(prefix, compiledCheck{}).check([]any{"x"}) || !itemsCheck(prefix, compiledCheck{}).check([]any{"x", 1, false}) {
		t.Fatal("absent rest must allow items after a possibly incomplete prefix")
	}
	if itemsCheck(prefix, plainCheck(never)).check([]any{"x", 1, false}) {
		t.Fatal("false rest must reject items after the prefix")
	}
	if !itemsCheck(nil, leaf(6, nil, always)).check([]any{Undefined, nil, false}) {
		t.Fatal("always leaf must accept rest items")
	}
	if !lengthCheck(1, 2).check([]any{nil}) || lengthCheck(1, 2).check([]any{}) || lengthCheck(1, 2).check([]any{1, 2, 3}) {
		t.Fatal("array length bounds disagree")
	}
	calls := 0
	contains := containsCheck(plainCheck(func(v any) bool { calls++; return isInteger(v) }), 1, 2)
	if contains.check([]any{1, 2, 3, 4}) || calls != 3 {
		t.Fatal("contains must stop when the maximum is exceeded")
	}
	if !contains.check([]any{"x", 1, 2}) || contains.check([]any{"x"}) || !containsCheck(plainCheck(never), 0, 0).check([]any{1}) {
		t.Fatal("contains count bounds disagree")
	}
	for _, tc := range []struct {
		value []any
		want  bool
	}{
		{nil, true}, {[]any{1, "1", nil, Undefined, false}, true},
		{[]any{1, 1.0}, false}, {[]any{math.NaN(), math.NaN()}, true},
		{[]any{map[string]any{"x": 1}, map[string]any{"x": 1.0}}, false},
		{[]any{[]any{1}, []any{1}}, false},
	} {
		if uniqueCheck(tc.value) != tc.want {
			t.Fatalf("uniqueItems(%v) disagrees", tc.value)
		}
	}
}

func TestTaggedCheck(t *testing.T) {
	calls := []string{}
	check := taggedCheck("kind", []any{"a", "b", nil, Undefined}, []compiledCheck{
		plainCheck(func(v any) bool { calls = append(calls, "a"); return isInteger(v.(map[string]any)["n"]) }),
		plainCheck(func(any) bool { calls = append(calls, "b"); return false }),
		plainCheck(always), plainCheck(always),
	})
	if !check.check(map[string]any{"kind": "a", "n": 1}) || !slices.Equal(calls, []string{"a"}) {
		t.Fatal("tag must select only the matching branch")
	}
	if check.check(map[string]any{"kind": "b"}) || !slices.Equal(calls, []string{"a", "b"}) || check.check(map[string]any{"kind": "c"}) || check.check(nil) {
		t.Fatal("tag selection returned the wrong result")
	}
	if !check.check(map[string]any{"kind": nil}) || !check.check(map[string]any{}) || !check.check(map[string]any{"kind": Undefined}) {
		t.Fatal("null and absent/undefined tag behavior disagrees")
	}
	if !taggedCheck("kind", []any{1}, []compiledCheck{plainCheck(always)}).check(map[string]any{"kind": 1.0}) || taggedCheck("kind", []any{math.NaN()}, []compiledCheck{plainCheck(always)}).check(map[string]any{"kind": math.NaN()}) {
		t.Fatal("primitive tag equality disagrees")
	}
}

func TestScopedCheck(t *testing.T) {
	scope := &evaluationScope{resources: []string{"outer"}}
	inner := scopedCheck("inner", compiledCheck{run: func(v any, state *evaluationScope) bool {
		if !slices.Equal(state.resources, []string{"outer", "root", "inner"}) {
			t.Fatalf("scope inside inner = %v", state.resources)
		}
		return v == "ok"
	}})
	root := scopedCheck("root", compiledCheck{run: func(v any, state *evaluationScope) bool {
		result := inner.run(v, state)
		if !slices.Equal(state.resources, []string{"outer", "root"}) {
			t.Fatalf("scope after inner = %v", state.resources)
		}
		return result
	}})
	for _, value := range []any{"ok", "bad"} {
		if root.run(value, scope) != (value == "ok") || !slices.Equal(scope.resources, []string{"outer"}) {
			t.Fatal("scoped check must restore scope on success and failure")
		}
	}
	func() {
		defer func() {
			if recover() != "callback failed" {
				t.Fatal("callback panic must propagate")
			}
		}()
		scopedCheck("panic", plainCheck(func(any) bool { panic("callback failed") })).run(nil, scope)
	}()
	if !slices.Equal(scope.resources, []string{"outer"}) {
		t.Fatal("scope must also be restored during panic unwinding")
	}
}

func TestCompiledMetadata(t *testing.T) {
	for _, name := range []string{"string", "number", "integer", "boolean", "null", "array", "object", "bytes", "bigint", "undefined"} {
		base := typeCheck([]any{name})
		combined := and([]compiledCheck{base})
		if combined.typeBit == 0 || combined.typeBit != base.typeBit || leafOf(combined) != leafOf(base) {
			t.Fatalf("single-part and lost metadata for %q", name)
		}
	}
	if and(nil).leaf.Code != 6 {
		t.Fatal("empty and must retain the always leaf")
	}
	if and([]compiledCheck{typeChecks["integer"], typeChecks["number"]}).typeBit != 0 {
		t.Fatal("a composite check must not claim to be a bare type")
	}
}

func TestScopePropagation(t *testing.T) {
	probe := compiledCheck{run: func(v any, state *evaluationScope) bool {
		return state != nil && slices.Equal(state.resources, []string{"root"}) && v != "bad"
	}}
	var slot compiledCheck
	slot = probe
	cases := []struct {
		name  string
		check compiledCheck
		input any
	}{
		{"and", and([]compiledCheck{plainCheck(always), probe}), "ok"},
		{"anyOf", anyCheck([]compiledCheck{plainCheck(never), probe}), "ok"},
		{"oneOf", oneCheck([]compiledCheck{plainCheck(never), probe}), "ok"},
		{"not", notCheck(notCheck(probe)), "ok"},
		{"then", ifCheck(plainCheck(always), probe, plainCheck(never)), "ok"},
		{"else", ifCheck(plainCheck(never), plainCheck(never), probe), "ok"},
		{"mask", maskedAnyCheck(0, []compiledCheck{probe}), "ok"},
		{"indirect", indirect(&slot), "ok"},
		{"object guard", guardObject(probe, true), map[string]any{}},
		{"array guard", guardArray(probe, true), []any{}},
		{"declared property", declaredObject([]propertyEntry{{"x", probe, true}}, 1, true, GuardStrict), map[string]any{"x": "ok"}},
		{"open property", openObject([]propertyEntry{{"x", probe, true}}, nil, nil, compiledCheck{}, GuardStrict), map[string]any{"x": "ok"}},
		{"pattern", openObject(nil, nil, []patternEntry{{regexp.MustCompile("^x"), probe}}, compiledCheck{}, GuardStrict), map[string]any{"x": "ok"}},
		{"additional", openObject(nil, nil, nil, probe, GuardStrict), map[string]any{"x": "ok"}},
		{"property name", propertyNamesCheck(probe), map[string]any{"x": 1}},
		{"dependent schema", dependentSchemasCheck([]schemaDependency{{"x", probe}}), map[string]any{"x": 1}},
		{"prefix", listCheck(GuardStrict, 0, 2, []compiledCheck{probe}, compiledCheck{}), []any{"ok"}},
		{"rest", itemsCheck(nil, probe), []any{"ok"}},
		{"contains", containsCheck(probe, 1, 1), []any{"ok", "bad"}},
		{"tag", taggedCheck("kind", []any{"a"}, []compiledCheck{probe}), map[string]any{"kind": "a"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !scopedCheck("root", tc.check).check(tc.input) {
				t.Fatal("check lost the caller's dynamic scope")
			}
		})
	}
	// An unsuccessful branch must pop its scope before the next branch runs.
	branch := scopedCheck("root", anyCheck([]compiledCheck{scopedCheck("failed", plainCheck(never)), probe}))
	if !branch.check("ok") {
		t.Fatal("a failed branch leaked its dynamic scope")
	}
	// Reentrant public calls begin with a fresh scope, even on the same check.
	var recursive compiledCheck
	recursive = scopedCheck("root", compiledCheck{run: func(v any, scope *evaluationScope) bool {
		if !slices.Equal(scope.resources, []string{"root"}) {
			return false
		}
		return v == 0 || recursive.check(v.(int)-1)
	}})
	if !recursive.check(3) {
		t.Fatal("reentrant calls shared evaluation state")
	}
}

func TestScopedCheckConcurrentUse(t *testing.T) {
	probe := compiledCheck{run: func(v any, scope *evaluationScope) bool {
		runtime.Gosched()
		return slices.Equal(scope.resources, []string{"root", "inner"}) && isInteger(v)
	}}
	check := scopedCheck("root", itemsCheck(nil, scopedCheck("inner", probe)))
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 50 {
				if !check.check([]any{1, 2, 3}) {
					t.Error("concurrent calls shared dynamic scope")
					return
				}
			}
		})
	}
	wg.Wait()
}

package uint128

import (
	"crypto/rand"
	"encoding/binary"
	"math/big"
	"testing"
)

func randUint128() Uint128 {
	randBuf := make([]byte, 16)
	rand.Read(randBuf)
	return FromBytes(randBuf)
}

func TestUint128(t *testing.T) {
	// test non-arithmetic methods
	for i := 0; i < 1000; i++ {
		x, y := randUint128(), randUint128()
		if i%3 == 0 {
			x = x.Rsh(64)
		} else if i%7 == 0 {
			x = x.Lsh(64)
		}

		if FromBig(x.Big()) != x {
			t.Fatal("FromBig is not the inverse of Big for", x)
		}

		b := make([]byte, 16)
		x.PutBytes(b)
		if FromBytes(b) != x {
			t.Fatal("FromBytes is not the inverse of PutBytes for", x)
		}

		if !x.Equals(x) {
			t.Fatalf("%v does not equal itself", x.lo)
		}
		if !From64(x.lo).Equals64(x.lo) {
			t.Fatalf("%v does not equal itself", x.lo)
		}

		if x.Cmp(y) != x.Big().Cmp(y.Big()) {
			t.Fatalf("mismatch: cmp(%v,%v) should equal %v, got %v", x, y, x.Big().Cmp(y.Big()), x.Cmp(y))
		} else if x.Cmp(x) != 0 {
			t.Fatalf("%v does not equal itself", x)
		}

		if x.Cmp64(y.lo) != x.Big().Cmp(From64(y.lo).Big()) {
			t.Fatalf("mismatch: cmp64(%v,%v) should equal %v, got %v", x, y.lo, x.Big().Cmp(From64(y.lo).Big()), x.Cmp64(y.lo))
		} else if From64(x.lo).Cmp64(x.lo) != 0 {
			t.Fatalf("%v does not equal itself", x.lo)
		}

		if x.String() != x.Big().String() {
			t.Fatalf("mismatch:\n%v !=\n%v", x.String(), x.Big().String())
		}
	}

	// Test 0 string
	if Zero.String() != "0" {
		t.Fatalf(`Zero.String() should be "0", got %q`, Zero.String())
	}

	// Check FromBig panics
	checkPanic := func(fn func(), msg string) {
		defer func() {
			r := recover()
			if s, ok := r.(string); !ok || s != msg {
				t.Errorf("expected %q, got %q", msg, r)
			}
		}()
		fn()
	}
	checkPanic(func() { _ = FromBig(big.NewInt(-1)) }, "value cannot be negative")
	checkPanic(func() { _ = FromBig(new(big.Int).Lsh(big.NewInt(1), 129)) }, "value overflows Uint128")
}

func TestArithmetic(t *testing.T) {
	// compare Uint128 arithmetic methods to their math/big equivalents, using
	// random values
	randBuf := make([]byte, 17)
	randUint128 := func() Uint128 {
		rand.Read(randBuf)
		var lo, hi uint64
		if randBuf[16]&1 != 0 {
			lo = binary.LittleEndian.Uint64(randBuf[:8])
		}
		if randBuf[16]&2 != 0 {
			hi = binary.LittleEndian.Uint64(randBuf[8:])
		}
		return New(lo, hi)
	}
	mod128 := func(i *big.Int) *big.Int {
		if i.Sign() == -1 {
			i = i.Add(new(big.Int).Lsh(big.NewInt(1), 128), i)
		}
		_, rem := i.QuoRem(i, new(big.Int).Lsh(big.NewInt(1), 128), new(big.Int))
		return rem
	}
	equalsBig := func(c Uint128, i *big.Int) bool {
		return c.Big().Cmp(i) == 0
	}
	for i := 0; i < 1000; i++ {
		x, y, z := randUint128(), randUint128(), uint(randUint128().lo&0xFF)
		xb, yb := x.Big(), y.Big()
		if !equalsBig(x.Add(y), mod128(new(big.Int).Add(xb, yb))) {
			t.Fatalf("mismatch: %v+%v should equal %v, got %v", x, y, mod128(new(big.Int).Add(xb, yb)), x.Add(y))
		}
		if !equalsBig(x.Sub(y), mod128(new(big.Int).Sub(xb, yb))) {
			t.Fatalf("mismatch: %v-%v should equal %v, got %v", x, y, mod128(new(big.Int).Sub(xb, yb)), x.Sub(y))
		}
		if !equalsBig(x.Mul(y), mod128(new(big.Int).Mul(xb, yb))) {
			t.Fatalf("mismatch: %v*%v should equal %v, got %v", x, y, mod128(new(big.Int).Mul(xb, yb)), x.Mul(y))
		}
		if !y.IsZero() && !equalsBig(x.Div(y), mod128(new(big.Int).Div(xb, yb))) {
			t.Fatalf("mismatch: %v/%v should equal %v, got %v", x, y, mod128(new(big.Int).Div(xb, yb)), x.Div(y))
		}
		if !equalsBig(x.Lsh(uint(z)), mod128(new(big.Int).Lsh(xb, uint(z)))) {
			t.Fatalf("mismatch: %v<<%v should equal %v, got %v", x, y, mod128(new(big.Int).Lsh(xb, uint(z))), x.Lsh(uint(z)))
		}
		if !equalsBig(x.Rsh(uint(z)), mod128(new(big.Int).Rsh(xb, uint(z)))) {
			t.Fatalf("mismatch: %v>>%v should equal %v, got %v", x, y, mod128(new(big.Int).Rsh(xb, uint(z))), x.Rsh(uint(z)))
		}
		if !equalsBig(x.And(y), mod128(new(big.Int).And(xb, yb))) {
			t.Fatalf("mismatch: %v & %v should equal %v, got %v", x, y, mod128(new(big.Int).And(xb, yb)), x.And(y))
		}
		if !equalsBig(x.Or(y), mod128(new(big.Int).Or(xb, yb))) {
			t.Fatalf("mismatch: %v | %v should equal %v, got %v", x, y, mod128(new(big.Int).Or(xb, yb)), x.Or(y))
		}
		if !equalsBig(x.Xor(y), mod128(new(big.Int).Xor(xb, yb))) {
			t.Fatalf("mismatch: %v ^ %v should equal %v, got %v", x, y, mod128(new(big.Int).Xor(xb, yb)), x.Xor(y))
		}

		// check 64-bit variants
		y64 := y.lo
		yb = From64(y64).Big()
		if !equalsBig(x.Add64(y64), mod128(new(big.Int).Add(xb, yb))) {
			t.Fatalf("mismatch: %v+%v should equal %v, got %v", x, y, mod128(new(big.Int).Add(xb, yb)), x.Add64(y64))
		}
		if !equalsBig(x.Sub64(y64), mod128(new(big.Int).Sub(xb, yb))) {
			t.Fatalf("mismatch: %v-%v should equal %v, got %v", x, y, mod128(new(big.Int).Sub(xb, yb)), x.Sub64(y64))
		}
		if !equalsBig(x.Mul64(y64), mod128(new(big.Int).Mul(xb, yb))) {
			t.Fatalf("mismatch: %v*%v should equal %v, got %v", x, y, mod128(new(big.Int).Mul(xb, yb)), x.Mul64(y64))
		}
		if y64 != 0 && !equalsBig(x.Div64(y64), mod128(new(big.Int).Div(xb, yb))) {
			t.Fatalf("mismatch: %v/%v should equal %v, got %v", x, y, mod128(new(big.Int).Div(xb, yb)), x.Div64(y64))
		}
		if !equalsBig(x.And64(y64), mod128(new(big.Int).And(xb, yb))) {
			t.Fatalf("mismatch: %v & %v should equal %v, got %v", x, y, mod128(new(big.Int).And(xb, yb)), x.And64(y64))
		}
		if !equalsBig(x.Or64(y64), mod128(new(big.Int).Or(xb, yb))) {
			t.Fatalf("mismatch: %v | %v should equal %v, got %v", x, y, mod128(new(big.Int).Or(xb, yb)), x.Or64(y64))
		}
		if !equalsBig(x.Xor64(y64), mod128(new(big.Int).Xor(xb, yb))) {
			t.Fatalf("mismatch: %v ^ %v should equal %v, got %v", x, y, mod128(new(big.Int).Xor(xb, yb)), x.Xor64(y64))
		}
	}
}

func BenchmarkArithmetic(b *testing.B) {
	randBuf := make([]byte, 17)
	randUint128 := func() Uint128 {
		rand.Read(randBuf)
		var lo, hi uint64
		if randBuf[16]&1 != 0 {
			lo = binary.LittleEndian.Uint64(randBuf[:8])
		}
		if randBuf[16]&2 != 0 {
			hi = binary.LittleEndian.Uint64(randBuf[8:])
		}
		return New(lo, hi)
	}
	x, y := randUint128(), randUint128()

	b.Run("Add native", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = x.lo * y.lo
		}
	})

	b.Run("Add", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			x.Add(y)
		}
	})

	b.Run("Sub", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			x.Sub(y)
		}
	})

	b.Run("Mul", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			x.Mul(y)
		}
	})

	b.Run("Lsh", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			x.Lsh(17)
		}
	})

	b.Run("Rsh", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			x.Rsh(17)
		}
	})
}

func BenchmarkDivision(b *testing.B) {
	randBuf := make([]byte, 8)
	randU64 := func() uint64 {
		rand.Read(randBuf)
		return binary.LittleEndian.Uint64(randBuf) | 3 // avoid divide-by-zero
	}
	x64 := From64(randU64())
	y64 := From64(randU64())
	x128 := New(randU64(), randU64())
	y128 := New(randU64(), randU64())

	b.Run("native 64/64", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = x64.lo / y64.lo
		}
	})
	b.Run("Div64 64/64", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			x64.Div64(y64.lo)
		}
	})
	b.Run("Div64 128/64", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			x128.Div64(y64.lo)
		}
	})
	b.Run("Div 64/64", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			x64.Div(y64)
		}
	})
	b.Run("Div 128/64-lo", func(b *testing.B) {
		x := x128
		x.hi = y64.lo - 1
		for i := 0; i < b.N; i++ {
			x.Div(y64)
		}
	})
	b.Run("Div 128/64-hi", func(b *testing.B) {
		x := x128
		x.hi = y64.lo + 1
		for i := 0; i < b.N; i++ {
			x.Div(y64)
		}
	})
	b.Run("Div 128/128", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			x128.Div(y128)
		}
	})
	b.Run("big.Int 128/64", func(b *testing.B) {
		xb, yb := x128.Big(), y64.Big()
		q := new(big.Int)
		for i := 0; i < b.N; i++ {
			q = q.Div(xb, yb)
		}
	})
	b.Run("big.Int 128/128", func(b *testing.B) {
		xb, yb := x128.Big(), y128.Big()
		q := new(big.Int)
		for i := 0; i < b.N; i++ {
			q = q.Div(xb, yb)
		}
	})
}

func BenchmarkString(b *testing.B) {
	buf := make([]byte, 16)
	rand.Read(buf)
	x := New(
		binary.LittleEndian.Uint64(buf[:8]),
		binary.LittleEndian.Uint64(buf[8:]),
	)
	xb := x.Big()
	b.Run("Uint128", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = x.String()
		}
	})
	b.Run("big.Int", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = xb.String()
		}
	})
}

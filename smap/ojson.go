// Package smap reads and writes SEER/RoboShop .smap files.
//
// The file is JSON. To edit it safely every field we do not understand must come back out
// unchanged, in the same order, with numbers printed exactly as they were. Go's generic
// map[string]any loses key order and re-prints numbers, so this package keeps its own small
// ordered JSON tree: objects keep their key order, numbers keep their original text.
package smap

import (
	"bytes"
	"fmt"
	"math"
	"strconv"
	"unicode/utf8"
)

// Num is a JSON number kept as its original text.
type Num string

// Obj is a JSON object that remembers key order.
type Obj struct {
	Keys []string
	Vals []any
}

func (o *Obj) idx(k string) int {
	if o == nil {
		return -1
	}
	for i, x := range o.Keys {
		if x == k {
			return i
		}
	}
	return -1
}

// Get returns the value for k, or nil.
func (o *Obj) Get(k string) any {
	if i := o.idx(k); i >= 0 {
		return o.Vals[i]
	}
	return nil
}

// Has reports whether the key is present.
func (o *Obj) Has(k string) bool { return o.idx(k) >= 0 }

// Set replaces the value for k, or appends the key.
func (o *Obj) Set(k string, v any) {
	if i := o.idx(k); i >= 0 {
		o.Vals[i] = v
		return
	}
	o.Keys = append(o.Keys, k)
	o.Vals = append(o.Vals, v)
}

// O returns a child object (nil if missing or not an object).
func (o *Obj) O(k string) *Obj { c, _ := o.Get(k).(*Obj); return c }

// A returns a child array (nil if missing).
func (o *Obj) A(k string) []any { c, _ := o.Get(k).([]any); return c }

// S returns a string field ("" if missing).
func (o *Obj) S(k string) string { c, _ := o.Get(k).(string); return c }

// F returns a number field. protobuf-JSON leaves out zero values, so missing means 0.
func (o *Obj) F(k string) float64 {
	switch v := o.Get(k).(type) {
	case Num:
		f, _ := strconv.ParseFloat(string(v), 64)
		return f
	case float64:
		return v
	}
	return 0
}

// IsNum reports whether field k holds a number.
func (o *Obj) IsNum(k string) bool {
	switch o.Get(k).(type) {
	case Num, float64:
		return true
	}
	return false
}

// SetF stores a number, printed the way JavaScript's JSON.stringify prints it.
func (o *Obj) SetF(k string, v float64) { o.Set(k, FmtNum(v)) }

// FmtNum prints a float the way JSON.stringify does for ordinary map values.
func FmtNum(v float64) Num {
	if v == 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return "0"
	}
	a := math.Abs(v)
	if a >= 1e21 || a < 1e-6 {
		return Num(strconv.FormatFloat(v, 'g', -1, 64))
	}
	return Num(strconv.FormatFloat(v, 'f', -1, 64))
}

// Objs returns the elements of an array that are objects.
func Objs(a []any) []*Obj {
	out := make([]*Obj, 0, len(a))
	for _, v := range a {
		if o, ok := v.(*Obj); ok {
			out = append(out, o)
		}
	}
	return out
}

// ---------------------------------------------------------------- parse

type parser struct {
	b []byte
	i int
}

// Parse reads JSON into the ordered tree. The root of a .smap is an object.
func Parse(b []byte) (any, error) {
	b = bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF})
	p := &parser{b: b}
	v, err := p.value()
	if err != nil {
		return nil, err
	}
	p.ws()
	if p.i != len(p.b) {
		return nil, fmt.Errorf("unexpected data at byte %d", p.i)
	}
	return v, nil
}

func (p *parser) ws() {
	for p.i < len(p.b) {
		switch p.b[p.i] {
		case ' ', '\n', '\r', '\t':
			p.i++
		default:
			return
		}
	}
}

func (p *parser) value() (any, error) {
	p.ws()
	if p.i >= len(p.b) {
		return nil, fmt.Errorf("unexpected end of file")
	}
	switch c := p.b[p.i]; {
	case c == '{':
		p.i++
		o := &Obj{}
		p.ws()
		if p.i < len(p.b) && p.b[p.i] == '}' {
			p.i++
			return o, nil
		}
		for {
			p.ws()
			if p.i >= len(p.b) || p.b[p.i] != '"' {
				return nil, fmt.Errorf("expected a key at byte %d", p.i)
			}
			k, err := p.str()
			if err != nil {
				return nil, err
			}
			p.ws()
			if p.i >= len(p.b) || p.b[p.i] != ':' {
				return nil, fmt.Errorf("expected ':' at byte %d", p.i)
			}
			p.i++
			v, err := p.value()
			if err != nil {
				return nil, err
			}
			o.Keys = append(o.Keys, k)
			o.Vals = append(o.Vals, v)
			p.ws()
			if p.i >= len(p.b) {
				return nil, fmt.Errorf("unexpected end of file")
			}
			if p.b[p.i] == ',' {
				p.i++
				continue
			}
			if p.b[p.i] == '}' {
				p.i++
				return o, nil
			}
			return nil, fmt.Errorf("expected ',' or '}' at byte %d", p.i)
		}
	case c == '[':
		p.i++
		a := []any{}
		p.ws()
		if p.i < len(p.b) && p.b[p.i] == ']' {
			p.i++
			return a, nil
		}
		for {
			v, err := p.value()
			if err != nil {
				return nil, err
			}
			a = append(a, v)
			p.ws()
			if p.i >= len(p.b) {
				return nil, fmt.Errorf("unexpected end of file")
			}
			if p.b[p.i] == ',' {
				p.i++
				continue
			}
			if p.b[p.i] == ']' {
				p.i++
				return a, nil
			}
			return nil, fmt.Errorf("expected ',' or ']' at byte %d", p.i)
		}
	case c == '"':
		return p.str()
	case c == 't' && bytes.HasPrefix(p.b[p.i:], []byte("true")):
		p.i += 4
		return true, nil
	case c == 'f' && bytes.HasPrefix(p.b[p.i:], []byte("false")):
		p.i += 5
		return false, nil
	case c == 'n' && bytes.HasPrefix(p.b[p.i:], []byte("null")):
		p.i += 4
		return nil, nil
	case c == '-' || (c >= '0' && c <= '9'):
		s := p.i
		for p.i < len(p.b) {
			d := p.b[p.i]
			if (d >= '0' && d <= '9') || d == '-' || d == '+' || d == '.' || d == 'e' || d == 'E' {
				p.i++
			} else {
				break
			}
		}
		t := string(p.b[s:p.i])
		if _, err := strconv.ParseFloat(t, 64); err != nil {
			return nil, fmt.Errorf("bad number %q at byte %d", t, s)
		}
		return Num(t), nil
	}
	return nil, fmt.Errorf("unexpected character %q at byte %d", p.b[p.i], p.i)
}

func (p *parser) str() (string, error) {
	p.i++ // opening quote
	s := p.i
	for p.i < len(p.b) && p.b[p.i] != '"' && p.b[p.i] != '\\' {
		p.i++
	}
	if p.i < len(p.b) && p.b[p.i] == '"' { // fast path, no escapes
		out := string(p.b[s:p.i])
		p.i++
		return out, nil
	}
	buf := append([]byte{}, p.b[s:p.i]...)
	for p.i < len(p.b) {
		c := p.b[p.i]
		if c == '"' {
			p.i++
			return string(buf), nil
		}
		if c != '\\' {
			buf = append(buf, c)
			p.i++
			continue
		}
		p.i++
		if p.i >= len(p.b) {
			break
		}
		switch e := p.b[p.i]; e {
		case '"', '\\', '/':
			buf = append(buf, e)
		case 'b':
			buf = append(buf, '\b')
		case 'f':
			buf = append(buf, '\f')
		case 'n':
			buf = append(buf, '\n')
		case 'r':
			buf = append(buf, '\r')
		case 't':
			buf = append(buf, '\t')
		case 'u':
			r, n := p.hex4(p.i + 1)
			if n == 0 {
				return "", fmt.Errorf("bad \\u escape at byte %d", p.i)
			}
			p.i += 4
			if r >= 0xD800 && r < 0xDC00 && p.i+6 < len(p.b) && p.b[p.i+1] == '\\' && p.b[p.i+2] == 'u' {
				if r2, n2 := p.hex4(p.i + 3); n2 != 0 && r2 >= 0xDC00 && r2 < 0xE000 {
					r = 0x10000 + (r-0xD800)<<10 + (r2 - 0xDC00)
					p.i += 6
				}
			}
			buf = utf8.AppendRune(buf, r)
		default:
			return "", fmt.Errorf("bad escape at byte %d", p.i)
		}
		p.i++
	}
	return "", fmt.Errorf("string not closed")
}

func (p *parser) hex4(at int) (rune, int) {
	if at+4 > len(p.b) {
		return 0, 0
	}
	v, err := strconv.ParseUint(string(p.b[at:at+4]), 16, 32)
	if err != nil {
		return 0, 0
	}
	return rune(v), 4
}

// ---------------------------------------------------------------- write

// Marshal writes the tree as compact JSON, escaping strings like JSON.stringify.
func Marshal(v any) []byte {
	var b bytes.Buffer
	b.Grow(1 << 20)
	write(&b, v)
	return b.Bytes()
}

func write(b *bytes.Buffer, v any) {
	switch t := v.(type) {
	case nil:
		b.WriteString("null")
	case *Obj:
		if t == nil {
			b.WriteString("null")
			return
		}
		b.WriteByte('{')
		for i, k := range t.Keys {
			if i > 0 {
				b.WriteByte(',')
			}
			writeStr(b, k)
			b.WriteByte(':')
			write(b, t.Vals[i])
		}
		b.WriteByte('}')
	case []any:
		b.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				b.WriteByte(',')
			}
			write(b, e)
		}
		b.WriteByte(']')
	case string:
		writeStr(b, t)
	case Num:
		b.WriteString(string(t))
	case float64:
		b.WriteString(string(FmtNum(t)))
	case int:
		b.WriteString(strconv.Itoa(t))
	case bool:
		if t {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	default:
		panic(fmt.Sprintf("smap: cannot write %T", v))
	}
}

const hexd = "0123456789abcdef"

func writeStr(b *bytes.Buffer, s string) {
	b.WriteByte('"')
	st := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 0x20 && c != '"' && c != '\\' {
			continue
		}
		b.WriteString(s[st:i])
		switch c {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		default:
			b.WriteString(`\u00`)
			b.WriteByte(hexd[c>>4])
			b.WriteByte(hexd[c&15])
		}
		st = i + 1
	}
	b.WriteString(s[st:])
	b.WriteByte('"')
}

// Clone makes a deep copy of a tree.
func Clone(v any) any {
	switch t := v.(type) {
	case *Obj:
		if t == nil {
			return t
		}
		n := &Obj{Keys: append([]string(nil), t.Keys...), Vals: make([]any, len(t.Vals))}
		for i, e := range t.Vals {
			n.Vals[i] = Clone(e)
		}
		return n
	case []any:
		n := make([]any, len(t))
		for i, e := range t {
			n[i] = Clone(e)
		}
		return n
	}
	return v
}

// Equal compares two trees by content. Numbers are compared by value (the same number can be
// printed with different digits by different programs), with a tolerance of one part in 1e9.
func Equal(a, b any) bool {
	switch x := a.(type) {
	case *Obj:
		y, ok := b.(*Obj)
		if !ok || (x == nil) != (y == nil) {
			return false
		}
		if x == nil {
			return true
		}
		if len(x.Keys) != len(y.Keys) {
			return false
		}
		for i, k := range x.Keys {
			j := i
			if y.Keys[i] != k {
				if j = y.idx(k); j < 0 {
					return false
				}
			}
			if !Equal(x.Vals[i], y.Vals[j]) {
				return false
			}
		}
		return true
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !Equal(x[i], y[i]) {
				return false
			}
		}
		return true
	case Num:
		y, ok := b.(Num)
		if !ok {
			return false
		}
		if x == y {
			return true
		}
		fa, _ := strconv.ParseFloat(string(x), 64)
		fb, _ := strconv.ParseFloat(string(y), 64)
		return math.Abs(fa-fb) <= 1e-9*math.Max(1, math.Max(math.Abs(fa), math.Abs(fb)))
	}
	return a == b
}

// Del removes a key (no-op when it is absent).
func (o *Obj) Del(k string) {
	if i := o.idx(k); i >= 0 {
		o.Keys = append(o.Keys[:i], o.Keys[i+1:]...)
		o.Vals = append(o.Vals[:i], o.Vals[i+1:]...)
	}
}

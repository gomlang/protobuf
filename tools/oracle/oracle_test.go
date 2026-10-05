package oracle_test

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

var binary string
var root string

func TestMain(m *testing.M) {
	var err error
	root, err = filepath.Abs("../..")
	if err != nil {
		panic(err)
	}
	driver := os.Getenv("GOML_VERIFY_DRIVER")
	if driver == "" {
		driver = os.Getenv("GOML")
	}
	if driver == "" {
		driver, err = exec.LookPath("goml")
	}
	if driver == "" || err != nil {
		fmt.Fprintln(os.Stderr, "set GOML_VERIFY_DRIVER or GOML to the pinned GoML driver")
		os.Exit(1)
	}
	work, err := os.MkdirTemp("", "protobuf-oracle-")
	if err != nil {
		panic(err)
	}
	code := func() int {
		defer os.RemoveAll(work)
		manifest := "[module]\npath=\"audit::protobuf_oracle\"\n[dependencies]\n\"ecosystem::protobuf\"={path=" + strconv.Quote(root) + "}\n"
		if err = os.WriteFile(filepath.Join(work, "goml.toml"), []byte(manifest), 0600); err != nil {
			panic(err)
		}
		source, e := os.ReadFile(filepath.Join(root, "examples/basic/main.goml"))
		if e != nil {
			panic(e)
		}
		if e = os.WriteFile(filepath.Join(work, "main.goml"), source, 0600); e != nil {
			panic(e)
		}
		cmd := exec.Command(driver, "build")
		cmd.Dir = work
		cmd.Env = os.Environ()
		out, e := cmd.CombinedOutput()
		if e != nil {
			fmt.Fprintf(os.Stderr, "oracle consumer build: %v\n%s", e, out)
			return 1
		}
		binary = filepath.Join(work, "_artifact/bin/protobuf_oracle")
		return m.Run()
	}()
	os.Exit(code)
}

type item struct {
	N int     `json:"n"`
	V []value `json:"v"`
}
type message struct {
	Fields  []item   `json:"fields"`
	Unknown []string `json:"unknown"`
}
type value struct {
	K       string   `json:"k"`
	S       string   `json:"s,omitempty"`
	Message *message `json:"message,omitempty"`
}
type request struct {
	Op        string   `json:"op"`
	Proto2    bool     `json:"proto2"`
	Packed    bool     `json:"packed"`
	Explicit  bool     `json:"explicit"`
	Hex       string   `json:"hex"`
	Canonical bool     `json:"canonical"`
	Discard   bool     `json:"discard"`
	Message   *message `json:"message,omitempty"`
}
type response struct {
	Error   string   `json:"error"`
	Hex     string   `json:"hex"`
	Message *message `json:"message"`
	Decoded *message `json:"decoded"`
	Raw     []string `json:"raw"`
}

func run(t *testing.T, requests []request) []response {
	t.Helper()
	in, e := json.Marshal(requests)
	if e != nil {
		t.Fatal(e)
	}
	cmd := exec.Command(binary, "--json")
	cmd.Stdin = bytes.NewReader(in)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatalf("consumer %v: %s\nstdout%s", e, stderr.String(), out)
	}
	var got []response
	if e = json.Unmarshal(out, &got); e != nil {
		t.Fatalf("JSON: %v %s", e, out)
	}
	if len(got) != len(requests) {
		t.Fatalf("response count %d != %d", len(got), len(requests))
	}
	return got
}
func decodeHex(t *testing.T, s string) []byte {
	t.Helper()
	b, e := hex.DecodeString(s)
	if e != nil {
		t.Fatal(e)
	}
	return b
}

var kinds = []descriptorpb.FieldDescriptorProto_Type{
	descriptorpb.FieldDescriptorProto_TYPE_DOUBLE, descriptorpb.FieldDescriptorProto_TYPE_FLOAT,
	descriptorpb.FieldDescriptorProto_TYPE_INT64, descriptorpb.FieldDescriptorProto_TYPE_UINT64,
	descriptorpb.FieldDescriptorProto_TYPE_INT32, descriptorpb.FieldDescriptorProto_TYPE_FIXED64,
	descriptorpb.FieldDescriptorProto_TYPE_FIXED32, descriptorpb.FieldDescriptorProto_TYPE_BOOL,
	descriptorpb.FieldDescriptorProto_TYPE_STRING, descriptorpb.FieldDescriptorProto_TYPE_BYTES,
	descriptorpb.FieldDescriptorProto_TYPE_UINT32, descriptorpb.FieldDescriptorProto_TYPE_ENUM,
	descriptorpb.FieldDescriptorProto_TYPE_SFIXED32, descriptorpb.FieldDescriptorProto_TYPE_SFIXED64,
	descriptorpb.FieldDescriptorProto_TYPE_SINT32, descriptorpb.FieldDescriptorProto_TYPE_SINT64,
}

func descriptor(t *testing.T, p2, packed, explicit bool) protoreflect.MessageDescriptor {
	t.Helper()
	syntax := "proto3"
	if p2 {
		syntax = "proto2"
	}
	r := &descriptorpb.DescriptorProto{Name: proto.String("Root")}
	c := &descriptorpb.DescriptorProto{Name: proto.String("Child")}
	field := func(n int, name string, kind descriptorpb.FieldDescriptorProto_Type, repeat bool) *descriptorpb.FieldDescriptorProto {
		label := descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL
		if repeat {
			label = descriptorpb.FieldDescriptorProto_LABEL_REPEATED
		}
		f := &descriptorpb.FieldDescriptorProto{Name: proto.String(name), Number: proto.Int32(int32(n)), Type: kind.Enum(), Label: label.Enum()}
		if kind == descriptorpb.FieldDescriptorProto_TYPE_ENUM {
			f.TypeName = proto.String(".audit.Mode")
		}
		if repeat && kind != descriptorpb.FieldDescriptorProto_TYPE_STRING && kind != descriptorpb.FieldDescriptorProto_TYPE_BYTES && kind != descriptorpb.FieldDescriptorProto_TYPE_MESSAGE {
			f.Options = &descriptorpb.FieldOptions{Packed: proto.Bool(packed)}
		}
		return f
	}
	for i, k := range kinds {
		f := field(i+1, fmt.Sprintf("s%d", i+1), k, false)
		if explicit && !p2 {
			f.Proto3Optional = proto.Bool(true)
			f.OneofIndex = proto.Int32(int32(len(r.OneofDecl)))
			r.OneofDecl = append(r.OneofDecl, &descriptorpb.OneofDescriptorProto{Name: proto.String(fmt.Sprintf("_s%d", i+1))})
		}
		r.Field = append(r.Field, f, field(i+21, fmt.Sprintf("r%d", i+21), k, true))
	}
	for _, n := range []int{17, 18, 41} {
		f := field(n, fmt.Sprintf("m%d", n), descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, n == 18)
		f.TypeName = proto.String(".audit.Child")
		if n == 41 {
			f.TypeName = proto.String(".audit.Root")
		}
		r.Field = append(r.Field, f)
	}
	f := field(1, "id", descriptorpb.FieldDescriptorProto_TYPE_INT32, false)
	if p2 {
		f.Label = descriptorpb.FieldDescriptorProto_LABEL_REQUIRED.Enum()
	}
	c.Field = append(c.Field, f, field(2, "text", descriptorpb.FieldDescriptorProto_TYPE_STRING, false), field(3, "numbers", descriptorpb.FieldDescriptorProto_TYPE_INT32, true))
	c.Field[2].Options = &descriptorpb.FieldOptions{Packed: proto.Bool(true)}
	if explicit && !p2 {
		c.OneofDecl = []*descriptorpb.OneofDescriptorProto{{Name: proto.String("_text")}}
		c.Field[1].Proto3Optional = proto.Bool(true)
		c.Field[1].OneofIndex = proto.Int32(0)
	}
	file, e := protodesc.NewFile(&descriptorpb.FileDescriptorProto{Name: proto.String("audit.proto"), Package: proto.String("audit"), Syntax: proto.String(syntax), MessageType: []*descriptorpb.DescriptorProto{r, c}, EnumType: []*descriptorpb.EnumDescriptorProto{{Name: proto.String("Mode"), Value: []*descriptorpb.EnumValueDescriptorProto{{Name: proto.String("ZERO"), Number: proto.Int32(0)}, {Name: proto.String("ONE"), Number: proto.Int32(1)}, {Name: proto.String("NEGATIVE"), Number: proto.Int32(-1)}}}}}, nil)
	if e != nil {
		t.Fatal(e)
	}
	return file.Messages().Get(0)
}
func describeValue(fd protoreflect.FieldDescriptor, v protoreflect.Value) value {
	switch fd.Kind() {
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		return value{K: "i32", S: strconv.FormatInt(v.Int(), 10)}
	case protoreflect.EnumKind:
		return value{K: "i32", S: strconv.FormatInt(int64(v.Enum()), 10)}
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		return value{K: "i64", S: strconv.FormatInt(v.Int(), 10)}
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		return value{K: "u32", S: strconv.FormatUint(v.Uint(), 10)}
	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return value{K: "u64", S: strconv.FormatUint(v.Uint(), 10)}
	case protoreflect.BoolKind:
		return value{K: "bool", S: strconv.FormatBool(v.Bool())}
	case protoreflect.StringKind:
		return value{K: "string", S: v.String()}
	case protoreflect.BytesKind:
		return value{K: "bytes", S: hex.EncodeToString(v.Bytes())}
	case protoreflect.FloatKind:
		s := "nan"
		if !math.IsNaN(v.Float()) {
			s = strconv.FormatUint(uint64(math.Float32bits(float32(v.Float()))), 10)
		}
		return value{K: "f32", S: s}
	case protoreflect.DoubleKind:
		s := "nan"
		if !math.IsNaN(v.Float()) {
			s = strconv.FormatUint(math.Float64bits(v.Float()), 10)
		}
		return value{K: "f64", S: s}
	case protoreflect.MessageKind:
		m := describe(v.Message())
		return value{K: "message", Message: &m}
	}
	panic("unhandled kind")
}
func rawFields(b []byte) ([]string, error) {
	out := []string{}
	for len(b) > 0 {
		_, _, n := protowire.ConsumeField(b)
		if n < 0 {
			return nil, protowire.ParseError(n)
		}
		out = append(out, hex.EncodeToString(b[:n]))
		b = b[n:]
	}
	return out, nil
}
func describe(m protoreflect.Message) message {
	out := message{Fields: []item{}, Unknown: []string{}}
	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		x := item{N: int(fd.Number()), V: []value{}}
		if fd.IsList() {
			for i := 0; i < v.List().Len(); i++ {
				x.V = append(x.V, describeValue(fd, v.List().Get(i)))
			}
		} else {
			x.V = append(x.V, describeValue(fd, v))
		}
		out.Fields = append(out.Fields, x)
		return true
	})
	sort.Slice(out.Fields, func(i, j int) bool { return out.Fields[i].N < out.Fields[j].N })
	var e error
	out.Unknown, e = rawFields(m.GetUnknown())
	if e != nil {
		panic(e)
	}
	return out
}
func eqMessage(t *testing.T, got *message, want message, context string) {
	t.Helper()
	if got == nil || !reflect.DeepEqual(*got, want) {
		a, _ := json.Marshal(got)
		b, _ := json.Marshal(want)
		t.Fatalf("%s\ngot %s\nwant%s", context, a, b)
	}
}

func TestRawWireAndMalformed(t *testing.T) {
	vectors := []string{"", "089601", "120774657374696e67", "8880808080808080800001", "08ffffffffffffffffff01", "0b0c", "0b13140881000c", "f8ffffff0f01", "80a30901", "0801120200ff1d78563412210807060504030201", "0a808000", "0a00"}
	for _, s := range []string{"00", "06", "0f", "80", "0880", "08ffffffffffffffffff02", "0880808080808080808080", "0d010203", "0901020304050607", "0a02ff", "0b", "0b14", "0c", "08010c", "0b080114"} {
		vectors = append(vectors, s)
	}
	rng := rand.New(rand.NewSource(1006))
	for i := 0; i < 250; i++ {
		b := []byte{}
		for j := 0; j < 1+rng.Intn(8); j++ {
			n := protowire.Number(1 + rng.Intn(50000))
			switch rng.Intn(5) {
			case 0:
				b = protowire.AppendTag(b, n, protowire.VarintType)
				b = protowire.AppendVarint(b, rng.Uint64())
			case 1:
				b = protowire.AppendTag(b, n, protowire.Fixed32Type)
				b = protowire.AppendFixed32(b, rng.Uint32())
			case 2:
				b = protowire.AppendTag(b, n, protowire.Fixed64Type)
				b = protowire.AppendFixed64(b, rng.Uint64())
			case 3:
				b = protowire.AppendTag(b, n, protowire.BytesType)
				p := make([]byte, rng.Intn(80))
				rng.Read(p)
				b = protowire.AppendBytes(b, p)
			case 4:
				b = protowire.AppendTag(b, n, protowire.StartGroupType)
				b = protowire.AppendTag(b, 1, protowire.VarintType)
				b = protowire.AppendVarint(b, rng.Uint64())
				b = protowire.AppendTag(b, n, protowire.EndGroupType)
			}
		}
		vectors = append(vectors, hex.EncodeToString(b))
		if len(b) > 0 {
			vectors = append(vectors, hex.EncodeToString(b[:len(b)-1]))
		}
	}
	corpus := []frozen{}
	for i, s := range vectors {
		if i < 40 || i%5 == 0 {
			raw, e := rawFields(decodeHex(t, s))
			corpus = append(corpus, frozen{Request: request{Op: "wire", Hex: s}, Error: e != nil, Raw: raw})
		}
	}
	recordCorpus(t, "wire", corpus)
	req := make([]request, len(vectors))
	for i, s := range vectors {
		req[i] = request{Op: "wire", Hex: s}
	}
	got := run(t, req)
	for i, s := range vectors {
		b := decodeHex(t, s)
		want, e := rawFields(b)
		if e != nil {
			if got[i].Error == "" {
				t.Fatalf("accepted invalid wire %s", s)
			}
			continue
		}
		if got[i].Error != "" || got[i].Hex != s || !reflect.DeepEqual(got[i].Raw, want) {
			t.Fatalf("raw %s => %+v want%v", s, got[i], want)
		}
	}
	// Standard field numbers are29 bits, stricter than the low-level Go protowire scanner.
	wide := protowire.AppendTag(nil, protowire.Number(1<<29), protowire.VarintType)
	wide = protowire.AppendVarint(wide, 1)
	if _, e := rawFields(wide); e != nil {
		t.Fatalf("reference changed: %v", e)
	}
	if run(t, []request{{Op: "wire", Hex: hex.EncodeToString(wide)}})[0].Error == "" {
		t.Fatal("accepted out-of-range field number")
	}
	if run(t, []request{{Op: "wire", Hex: "088100", Canonical: true}})[0].Error == "" {
		t.Fatal("strict accepted nonminimal")
	}
	t.Logf("%d protowire complete-message acceptance/preservation cases", len(vectors))
}

func TestTypedWireCompatibility(t *testing.T) {
	type testCase struct {
		r    request
		desc protoreflect.MessageDescriptor
	}
	cases := []testCase{}
	rng := rand.New(rand.NewSource(6265))
	for _, p2 := range []bool{false, true} {
		for _, packed := range []bool{false, true} {
			for _, explicit := range []bool{false, true} {
				if p2 && explicit {
					continue
				}
				desc := descriptor(t, p2, packed, explicit)
				known := []string{"", "2800", "28ffffffff0f", "288080808010", "588080808010", "40ff01", "290100000000000000", "810101", "8a010208018a010412027879", "8a01038a0100", "8a0100", "aa01020102a80103aa0100aa010104", "ca01020102c80103ca0100ca010104", "ca010100", "490000000000000000", "090000000000000080", "1500000080", "09010000000000f07f", "150100807f", "0801", "888080808080808080008100", "9b060801a301a4019c06"}
				// Explicitly exercise child merge with required id appearing only in the second occurrence.
				known = append(known, "8a0104120278798a01020807", "8a010208078a0100", "920100")
				for _, s := range known {
					cases = append(cases, testCase{request{Op: "decode", Hex: s, Proto2: p2, Packed: packed, Explicit: explicit}, desc})
				}
				for i := 0; i < 65; i++ {
					m := dynamicpb.NewMessage(desc)
					for n := 1; n <= 16; n++ {
						fd := desc.Fields().ByNumber(protoreflect.FieldNumber(n))
						if rng.Intn(4) == 0 {
							continue
						}
						m.Set(fd, randomValue(fd, rng))
					}
					for n := 21; n <= 36; n++ {
						fd := desc.Fields().ByNumber(protoreflect.FieldNumber(n))
						list := m.Mutable(fd).List()
						for j := 0; j < rng.Intn(5); j++ {
							list.Append(randomValue(fd, rng))
						}
					}
					if i%3 == 0 {
						fd := desc.Fields().ByNumber(17)
						child := m.Mutable(fd).Message()
						child.Set(child.Descriptor().Fields().ByNumber(1), protoreflect.ValueOfInt32(int32(i)))
						child.Set(child.Descriptor().Fields().ByNumber(2), protoreflect.ValueOfString("nested😀"))
					}
					unknown := protowire.AppendTag(nil, 100, protowire.VarintType)
					unknown = append(unknown, 0x81, 0)
					m.SetUnknown(unknown)
					b, e := proto.MarshalOptions{Deterministic: true}.Marshal(m)
					if e != nil {
						t.Fatal(e)
					}
					cases = append(cases, testCase{request{Op: "decode", Hex: hex.EncodeToString(b), Proto2: p2, Packed: !packed, Explicit: explicit, Discard: i%5 == 0}, desc})
					if len(b) > 1 && i%3 == 0 {
						cases = append(cases, testCase{request{Op: "decode", Hex: hex.EncodeToString(b[:len(b)-1]), Proto2: p2, Packed: packed, Explicit: explicit}, desc})
					}
				}
			}
		}
	}
	corpus := []frozen{}
	for i, c := range cases {
		if i < 28 || i%5 == 0 {
			m := dynamicpb.NewMessage(c.desc)
			e := proto.UnmarshalOptions{DiscardUnknown: c.r.Discard}.Unmarshal(decodeHex(t, c.r.Hex), m)
			row := frozen{Request: c.r, Error: e != nil}
			if e == nil {
				d := describe(m)
				row.Message = &d
			}
			corpus = append(corpus, row)
		}
	}
	recordCorpus(t, "typed", corpus)
	requests := make([]request, len(cases))
	for i, c := range cases {
		requests[i] = c.r
	}
	got := run(t, requests)
	for i, c := range cases {
		wire := decodeHex(t, c.r.Hex)
		want := dynamicpb.NewMessage(c.desc)
		e := proto.UnmarshalOptions{DiscardUnknown: c.r.Discard}.Unmarshal(wire, want)
		if e != nil {
			if got[i].Error == "" {
				t.Fatalf("case%d accepted Go-invalid: %s %v", i, c.r.Hex, e)
			}
			continue
		}
		if got[i].Error != "" {
			t.Fatalf("case%d Go accepted %s but GoML: %s", i, c.r.Hex, got[i].Error)
		}
		eqMessage(t, got[i].Decoded, describe(want), fmt.Sprintf("decode%d %s", i, c.r.Hex))
		round := dynamicpb.NewMessage(c.desc)
		if e = proto.Unmarshal(decodeHex(t, got[i].Hex), round); e != nil {
			t.Fatalf("case%d Go rejected encoded: %v", i, e)
		}
		if !proto.Equal(want, round) {
			t.Fatalf("case%d returned wire changed semantic value", i)
		}
		eqMessage(t, got[i].Message, describe(round), fmt.Sprintf("round%d", i))
	}
	t.Logf("%d dynamicpb decode/error and independent Google decode of GoML re-encoding cases", len(cases))
}
func randomValue(fd protoreflect.FieldDescriptor, r *rand.Rand) protoreflect.Value {
	switch fd.Kind() {
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		return protoreflect.ValueOfInt32(int32(r.Uint32()))
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		return protoreflect.ValueOfInt64(int64(r.Uint64()))
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		return protoreflect.ValueOfUint32(r.Uint32())
	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return protoreflect.ValueOfUint64(r.Uint64())
	case protoreflect.FloatKind:
		return protoreflect.ValueOfFloat32(math.Float32frombits(r.Uint32()))
	case protoreflect.DoubleKind:
		return protoreflect.ValueOfFloat64(math.Float64frombits(r.Uint64()))
	case protoreflect.BoolKind:
		return protoreflect.ValueOfBool(r.Intn(2) == 0)
	case protoreflect.StringKind:
		return protoreflect.ValueOfString([]string{"", "a", "é中😀", "\x00\r\n"}[r.Intn(4)])
	case protoreflect.BytesKind:
		b := make([]byte, r.Intn(17))
		r.Read(b)
		return protoreflect.ValueOfBytes(b)
	case protoreflect.EnumKind:
		return protoreflect.ValueOfEnum(protoreflect.EnumNumber([]int32{-1, 0, 1, 7, 2147483647}[r.Intn(5)]))
	}
	panic("random kind")
}

func TestConstructedMessagesAndDocumentedDifferences(t *testing.T) {
	req := []request{}
	want := []*dynamicpb.Message{}
	for _, p2 := range []bool{false, true} {
		for _, packed := range []bool{false, true} {
			for _, explicit := range []bool{false, true} {
				if p2 && explicit {
					continue
				}
				desc := descriptor(t, p2, packed, explicit)
				rng := rand.New(rand.NewSource(9530))
				for i := 0; i < 20; i++ {
					m := dynamicpb.NewMessage(desc)
					for n := 1; n <= 16; n++ {
						fd := desc.Fields().ByNumber(protoreflect.FieldNumber(n))
						m.Set(fd, randomValue(fd, rng))
					}
					for n := 21; n <= 36; n++ {
						fd := desc.Fields().ByNumber(protoreflect.FieldNumber(n))
						for j := 0; j < 3; j++ {
							m.Mutable(fd).List().Append(randomValue(fd, rng))
						}
					}
					input := describe(m)
					for ei := range input.Fields {
						for vi := range input.Fields[ei].V {
							v := &input.Fields[ei].V[vi]
							if v.S == "nan" {
								if v.K == "f32" {
									v.S = "2143289344"
								} else {
									v.S = "9221120237041090560"
								}
							}
						}
					}
					req = append(req, request{Op: "encode", Proto2: p2, Packed: packed, Explicit: explicit, Message: &input})
					want = append(want, m)
				}
			}
		}
	}
	corpus := []frozen{}
	for i, r := range req {
		if i%5 == 0 {
			d := describe(want[i])
			corpus = append(corpus, frozen{Request: r, Message: &d})
		}
	}
	recordCorpus(t, "constructed", corpus)
	got := run(t, req)
	for i, r := range got {
		if r.Error != "" {
			t.Fatalf("construct%d:%s", i, r.Error)
		}
		decoded := dynamicpb.NewMessage(want[i].Descriptor())
		if e := proto.Unmarshal(decodeHex(t, r.Hex), decoded); e != nil {
			t.Fatal(e)
		}
		if !proto.Equal(want[i], decoded) {
			t.Fatalf("constructed%d differs from Google message", i)
		}
	}
	// Unlike permissive proto2 Go strings, this library always requires Unicode UTF8.
	invalid := "4a01ff"
	m := dynamicpb.NewMessage(descriptor(t, true, false, false))
	if e := proto.Unmarshal(decodeHex(t, invalid), m); e != nil {
		t.Fatalf("reference proto2 UTF8 behavior changed: %v", e)
	}
	if run(t, []request{{Op: "decode", Proto2: true, Hex: invalid}})[0].Error == "" {
		t.Fatal("accepted invalid UTF8 string")
	}
	t.Logf("%d independently constructed public Message encodings decoded by Google", len(req))
}

func TestConcurrentIndependentConsumers(t *testing.T) {
	// Separate consumer processes exercise the same frozen public schema/data concurrently;
	// Go's race detector also covers native reference construction and response handling.
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got := run(t, []request{{Op: "decode", Hex: "28014a0161ca0103010203", Packed: true}})
			if got[0].Error != "" {
				t.Error(got[0].Error)
			}
		}()
	}
	wg.Wait()
}

// Retained test output records the pinned module and actual public consumer protocol.
func TestVersionContract(t *testing.T) {
	if !strings.Contains(binary, "protobuf-oracle-") {
		t.Fatal("unexpected consumer fixture")
	}
	t.Log("oracle google.golang.org/protobuf v1.36.10; consumer compiled by GOML_VERIFY_DRIVER, then GOML; GOFLAGS inherited")
}

// Expected data comes exclusively from the pinned Google implementation.
// Update explicitly; ordinary native and race runs verify the checked-in corpus.
type frozen struct {
	Request request  `json:"request"`
	Error   bool     `json:"error"`
	Raw     []string `json:"raw,omitempty"`
	Message *message `json:"message,omitempty"`
}

func recordCorpus(t *testing.T, name string, rows []frozen) {
	t.Helper()
	var data bytes.Buffer
	for _, row := range rows {
		b, e := json.Marshal(row)
		if e != nil {
			t.Fatal(e)
		}
		data.Write(b)
		data.WriteByte('\n')
	}
	path := filepath.Join(root, "examples/basic/tests/data", name+".jsonl")
	if os.Getenv("PROTOBUF_UPDATE_VECTORS") == "1" {
		if e := os.MkdirAll(filepath.Dir(path), 0755); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(path, data.Bytes(), 0644); e != nil {
			t.Fatal(e)
		}
	}
	stored, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(stored, data.Bytes()) {
		t.Fatalf("%s: frozen Google corpus differs; review and explicitly regenerate", name)
	}
	t.Logf("%s: %d frozen independently generated cases", name, len(rows))
}

func TestZeroDefaultsAndFloatingPresence(t *testing.T) {
	// Zero defaults and their explicit-presence counterparts are intentional cases,
	// not values we hope to encounter in the deterministic random matrix.
	for _, p2 := range []bool{false, true} {
		for _, explicit := range []bool{false, true} {
			if p2 && explicit {
				continue
			}
			desc := descriptor(t, p2, true, explicit)
			for _, wire := range []string{
				"09000000000000000015000000004a005200",
				"0900000000000000801500000080",
				"09010000000000f07f150100807f",
			} {
				want := dynamicpb.NewMessage(desc)
				if e := proto.Unmarshal(decodeHex(t, wire), want); e != nil {
					t.Fatal(e)
				}
				got := run(t, []request{{Op: "decode", Proto2: p2, Packed: true, Explicit: explicit, Hex: wire}})[0]
				if got.Error != "" {
					t.Fatal(got.Error)
				}
				eqMessage(t, got.Decoded, describe(want), "zero/negative-zero/NaN presence")
				round := dynamicpb.NewMessage(desc)
				if e := proto.Unmarshal(decodeHex(t, got.Hex), round); e != nil {
					t.Fatal(e)
				}
				if !proto.Equal(want, round) {
					t.Fatal("floating presence changed")
				}
			}
		}
	}
	t.Log("9 explicit zero/negative-zero/NaN and empty string/bytes presence cases")
}

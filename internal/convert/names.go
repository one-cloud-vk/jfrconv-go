// Portions derived from async-profiler 4.1 and jfrconv-rs.
// SPDX-License-Identifier: Apache-2.0
package convert

import (
	"jfrconv-go/internal/jfr"
	"strconv"
	"strings"
)

func javaName(symbol string, array, dotted bool, o Options) string {
	depth := 0
	if array {
		for depth < len(symbol) && symbol[depth] == '[' {
			depth++
		}
	}
	s := symbol[depth:]
	if depth > 0 {
		switch s {
		case "B":
			s = "byte"
		case "C":
			s = "char"
		case "S":
			s = "short"
		case "I":
			s = "int"
		case "J":
			s = "long"
		case "Z":
			s = "boolean"
		case "F":
			s = "float"
		case "D":
			s = "double"
		default:
			if strings.HasPrefix(s, "L") && strings.HasSuffix(s, ";") {
				s = s[1 : len(s)-1]
			}
		}
	}
	if o.Norm {
		if i := strings.LastIndexAny(s, "/."); i >= 0 && i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '9' {
			end := i
			if i > 19 && s[i-19] == '+' && s[i-18] == '0' {
				end = i - 19
			}
			s = s[:end]
		}
	}
	if o.Simple {
		if i := strings.LastIndexByte(s, '/'); i >= 0 && (i+1 == len(s) || s[i+1] < '0' || s[i+1] > '9') {
			s = s[i+1:]
		}
	}
	if dotted {
		s = strings.ReplaceAll(s, "/", ".")
	}
	if depth > 0 {
		s += strings.Repeat("[]", depth)
	}
	return s
}

func methodName(c *jfr.Chunk, id uint64, kind uint8, o Options) string {
	m, ok := c.Method(id)
	if !ok {
		return "unknown"
	}
	class := ""
	if ref, exists := c.Class(m.Class); exists {
		class = c.Symbol(ref.Name)
	}
	name := c.Symbol(m.Name)
	_, hasAPNative := c.Enums["jdk.types.FrameType"][5]
	native := kind == 4 || kind == 5 || kind == 3 && hasAPNative
	if class == "" || native {
		return name
	}
	class = javaName(class, false, o.Dot, o)
	if name == "" {
		return class
	}
	return class + "." + name
}
func className(c *jfr.Chunk, id uint64, o Options) string {
	if ref, ok := c.Class(id); ok {
		return javaName(c.Symbol(ref.Name), true, true, o)
	}
	return "null"
}
func threadName(c *jfr.Chunk, id uint64) string {
	name := c.ThreadName(id)
	if strings.HasPrefix(name, "[tid=") {
		return name
	}
	if name != "" {
		return "[" + name + " tid=" + strconv.FormatUint(id, 10) + "]"
	}
	return "[tid=" + strconv.FormatUint(id, 10) + "]"
}

type nameKind struct {
	Name string
	Kind uint8
}

func category(frames []nameKind) (string, uint8) {
	vm := false
	for i := len(frames) - 1; i >= 0; i-- {
		f := frames[i]
		if f.Kind == 4 {
			switch f.Name {
			case "CompileBroker::compiler_thread_loop":
				return "[jit]", 4
			case "GCTaskThread::run", "WorkerThread::run":
				return "[gc]", 4
			case "java_start", "thread_native_entry":
				vm = true
			}
		} else if f.Kind != 3 {
			break
		}
	}
	if vm {
		return "[vm]", 4
	}
	for i, f := range frames {
		switch {
		case f.Name == "Verifier::verify":
			return "[class_verify]", 4
		case strings.HasPrefix(f.Name, "InstanceKlass::initialize"):
			return "[class_init]", 4
		case strings.HasPrefix(f.Name, "LinkResolver::"), strings.HasPrefix(f.Name, "InterpreterRuntime::resolve"), strings.HasPrefix(f.Name, "SystemDictionary::resolve"):
			return "[class_resolve]", 4
		case strings.HasSuffix(f.Name, "ClassLoader.loadClass"):
			return "[class_load]", 4
		case strings.HasSuffix(f.Name, "LambdaMetafactory.metafactory"), strings.HasSuffix(f.Name, "LambdaMetafactory.altMetafactory"):
			return "[lambda_init]", 4
		case strings.HasSuffix(f.Name, "table stub"):
			return "[vtable_stubs]", 3
		case f.Name == "Interpreter":
			return "[Interpreter]", 3
		case strings.HasPrefix(f.Name, "I2C/C2I"):
			if i+1 < len(frames) && frames[i+1].Kind == 0 {
				return "[Interpreter]", 3
			}
			return "[c2i_adapter]", 2
		}
	}
	java := true
	for _, f := range frames {
		switch f.Kind {
		case 0:
			if java {
				return "[Interpreter]", 3
			}
			return "[native]", 3
		case 1:
			if java {
				return "[c2_comp]", 2
			}
			return "[native]", 3
		case 2:
			java = true
		case 3:
			if strings.HasPrefix(f.Name, "JVM_") || strings.HasPrefix(f.Name, "Unsafe_") || strings.HasPrefix(f.Name, "MHN_") || strings.HasPrefix(f.Name, "jni_") {
				return "[vm]", 4
			}
			switch f.Name {
			case "call_stub", "deoptimization", "unknown_Java", "not_walkable_Java", "InlineCacheBuffer":
				return "[vm]", 4
			}
			if !strings.HasSuffix(f.Name, "_arraycopy") && !strings.Contains(f.Name, "pthread_cond") {
				java = false
			}
		case 4:
			if strings.HasPrefix(f.Name, "Runtime1::") {
				return "[c1_comp]", 6
			}
		case 6:
			if java {
				return "[c1_comp]", 6
			}
			return "[native]", 3
		}
	}
	return "[native]", 3
}

type methodKey struct {
	ID   uint64
	Kind uint8
}
type nameCache map[methodKey]string

func cachedName(c *jfr.Chunk, id uint64, kind uint8, o Options, names nameCache) string {
	key := methodKey{id, kind}
	if s, ok := names[key]; ok {
		return s
	}
	s := methodName(c, id, kind, o)
	names[key] = s
	return s
}

type description struct {
	Name       string
	Kind, Role uint8
	Line       int32
}

// A resolver belongs to one chunk. Returned tier counts never alias its scratch.
type pathResolver struct {
	names          nameCache
	descriptions   []description
	ids            []uint32
	classification []nameKind
}

func (r *pathResolver) resolve(c *jfr.Chunk, e *jfr.Event, o Options, p *profile, format string) (cachedPath, bool) {
	flame := format != "pprof" && format != "pb.gz" && format != "otlp"
	stack := c.Stack(e.StackID)
	if flame && e.MethodID == 0 && stack.Len() == 0 && !c.HasStack(e.StackID) {
		return cachedPath{}, false
	}
	defer func() {
		clear(r.descriptions)
		r.descriptions = r.descriptions[:0]
		clear(r.classification)
		r.classification = r.classification[:0]
	}()
	if o.Threads {
		r.descriptions = append(r.descriptions, description{Name: threadName(c, e.TID), Kind: 3, Role: 1})
	}
	if o.Classify {
		for i := 0; i < stack.Len(); i++ {
			f := stack.At(i)
			r.classification = append(r.classification, nameKind{cachedName(c, f.Method, f.Kind, o, r.names), f.Kind})
		}
		name, kind := category(r.classification)
		r.descriptions = append(r.descriptions, description{Name: name, Kind: kind, Role: 2})
	}
	for i := stack.Len() - 1; i >= 0; i-- {
		f := stack.At(i)
		name := cachedName(c, f.Method, f.Kind, o, r.names)
		line := int32(uint16(f.Line))
		storedLine := line
		if flame {
			storedLine = 0
			if o.Lines && line != 0 {
				name += ":" + strconv.FormatInt(int64(line), 10)
			} else if o.BCI && f.BCI != 0 {
				name += "@" + strconv.FormatUint(uint64(uint16(f.BCI)), 10)
			}
		}
		r.descriptions = append(r.descriptions, description{Name: name, Kind: f.Kind, Line: storedLine})
	}
	if e.ClassID != 0 && format != "otlp" {
		kind := uint8(2)
		if e.Kind == jfr.Allocation && !e.TLAB {
			kind = 5
		}
		r.descriptions = append(r.descriptions, description{Name: className(c, e.ClassID, o), Kind: kind})
	}
	if e.MethodID != 0 {
		r.descriptions = append(r.descriptions, description{Name: cachedName(c, e.MethodID, 1, o, r.names), Kind: 1})
	}
	if flame {
		included := o.Include == nil
		for _, d := range r.descriptions {
			if o.Exclude != nil && o.Exclude.MatchString(d.Name) {
				return cachedPath{}, false
			}
			if o.Include != nil && o.Include.MatchString(d.Name) {
				included = true
			}
		}
		if !included {
			return cachedPath{}, false
		}
	}
	r.ids = r.ids[:0]
	for _, d := range r.descriptions {
		r.ids = append(r.ids, p.frameName(d.Name, d.Kind, d.Line, 0, d.Role))
	}
	ids := r.ids
	if flame {
		ids = layoutPath(ids, o, p)
	}
	return p.path(ids, true), true
}

func transformPath(ids []uint32, o Options, p *profile) ([]uint32, bool) {
	included := o.Include == nil
	for _, id := range ids {
		name := p.strings.text(p.frames.at(id).Name)
		if o.Exclude != nil && o.Exclude.MatchString(name) {
			return nil, false
		}
		if o.Include != nil && o.Include.MatchString(name) {
			included = true
		}
	}
	if !included {
		return nil, false
	}
	return layoutPath(ids, o, p), true
}

func layoutPath(ids []uint32, o Options, p *profile) []uint32 {
	if len(ids) > p.originalDepth {
		p.originalDepth = len(ids)
	}
	if !o.Reverse {
		skip := o.Skip
		if skip > len(ids) {
			skip = len(ids)
		}
		return ids[skip:]
	}
	skip := o.Skip
	if skip == 0 && len(ids) > 0 && p.frames.at(ids[0]).Role == 1 {
		skip = 1
	}
	if skip >= len(ids) {
		if skip == 1 && o.Skip == 0 && len(ids) > 0 {
			return ids[:1]
		}
		return ids[:0]
	}
	start := 0
	if o.Skip > 0 {
		ids = ids[skip:]
	} else {
		start = skip
	}
	for i, j := start, len(ids)-1; i < j; i, j = i+1, j-1 {
		ids[i], ids[j] = ids[j], ids[i]
	}
	return ids
}

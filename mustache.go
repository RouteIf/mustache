package mustache

import (
	"bytes"
	"fmt"
	"html/template"
	"io"
	"os"
	"path"
	"reflect"
	"strconv"
	"strings"
)

var (
	// AllowMissingVariables defines the behavior for a variable "miss." If it
	// is true (the default), an empty string is emitted. If it is false, an error
	// is generated instead.
	AllowMissingVariables = true
)

// RenderFunc is provided to lambda functions for rendering.
type RenderFunc func(text string) (string, error)

// LambdaFunc is the signature for lambda functions.
type LambdaFunc func(text string, render RenderFunc) (string, error)

// EscapeFunc is used for escaping non-raw values in templates.
type EscapeFunc func(text string) string

// FormatterFunc is used for formatting values in templates.
type FormatterFunc func(any) (string, error)

// CallbackInterface provides a way to lookup values in a custom way.
type CallbackInterface interface {
	Lookup(name string) (interface{}, error)
}

// A TagType represents the specific type of mustache tag that a Tag
// represents. The zero TagType is not a valid type.
type TagType uint

// Defines representing the possible Tag types
const (
	Invalid TagType = iota
	Variable
	Section
	InvertedSection
	Partial
)

// Skip all whitespaces apeared after these types of tags until end of line
// if the line only contains a tag and whitespaces.
const (
	SkipWhitespaceTagTypes = "#^/<>=!"
)

func (t TagType) String() string {
	if int(t) < len(tagNames) {
		return tagNames[t]
	}
	return "type" + strconv.Itoa(int(t))
}

var tagNames = []string{
	Invalid:         "Invalid",
	Variable:        "Variable",
	Section:         "Section",
	InvertedSection: "InvertedSection",
	Partial:         "Partial",
}

// Tag represents the different mustache tag types.
//
// Not all methods apply to all kinds of tags. Restrictions, if any, are noted
// in the documentation for each method. Use the Type method to find out the
// type of tag before calling type-specific methods. Calling a method
// inappropriate to the type of tag causes a run time panic.
type Tag interface {
	// Type returns the type of the tag.
	Type() TagType
	// Name returns the name of the tag.
	Name() string
	// Tags returns any child tags. It panics for tag types which cannot contain
	// child tags (i.e. variable tags).
	Tags() []Tag
}

type textElement struct {
	text []byte
}

type varElement struct {
	name string
	raw  bool
}

type sectionElement struct {
	name      string
	inverted  bool
	startline int
	elems     []interface{}
}

type partialElement struct {
	name   string
	indent string
	prov   PartialProvider
}

// Template represents a compilde mustache template
type Template struct {
	data      string
	otag      string
	ctag      string
	p         int
	curline   int
	elems     []interface{}
	forceRaw  bool
	partial   PartialProvider
	escape    EscapeFunc
	formatter FormatterFunc
}

// Tags returns the mustache tags for the given template
func (tmpl *Template) Tags() []Tag {
	return extractTags(tmpl.elems)
}

// Escape sets custom escape function. By-default it is HTMLEscape.
func (tmpl *Template) Escape(fn EscapeFunc) {
	tmpl.escape = fn
}

func (tmpl *Template) Formatter(fn FormatterFunc) {
	tmpl.formatter = fn
}

func extractTags(elems []interface{}) []Tag {
	tags := make([]Tag, 0, len(elems))
	for _, elem := range elems {
		switch elem := elem.(type) {
		case *varElement:
			tags = append(tags, elem)
		case *sectionElement:
			tags = append(tags, elem)
		case *partialElement:
			tags = append(tags, elem)
		}
	}
	return tags
}

func (e *varElement) Type() TagType {
	return Variable
}

func (e *varElement) Name() string {
	return e.name
}

func (e *varElement) Tags() []Tag {
	panic("mustache: Tags on Variable type")
}

func (e *sectionElement) Type() TagType {
	if e.inverted {
		return InvertedSection
	}
	return Section
}

func (e *sectionElement) Name() string {
	return e.name
}

func (e *sectionElement) Tags() []Tag {
	return extractTags(e.elems)
}

func (e *partialElement) Type() TagType {
	return Partial
}

func (e *partialElement) Name() string {
	return e.name
}

func (e *partialElement) Tags() []Tag {
	return nil
}

func (tmpl *Template) readString(s string) (string, error) {
	newlines := 0
	for i := tmpl.p; ; i++ {
		//are we at the end of the string?
		if i+len(s) > len(tmpl.data) {
			return tmpl.data[tmpl.p:], io.EOF
		}

		if tmpl.data[i] == '\n' {
			newlines++
		}

		if tmpl.data[i] != s[0] {
			continue
		}

		match := true
		for j := 1; j < len(s); j++ {
			if s[j] != tmpl.data[i+j] {
				match = false
				break
			}
		}

		if match {
			e := i + len(s)
			text := tmpl.data[tmpl.p:e]
			tmpl.p = e

			tmpl.curline += newlines
			return text, nil
		}
	}
}

type textReadingResult struct {
	text          string
	padding       string
	mayStandalone bool
}

func (tmpl *Template) readText() (*textReadingResult, error) {
	pPrev := tmpl.p
	text, err := tmpl.readString(tmpl.otag)
	if err == io.EOF {
		return &textReadingResult{
			text:          text,
			padding:       "",
			mayStandalone: false,
		}, err
	}

	var i int
	for i = tmpl.p - len(tmpl.otag); i > pPrev; i-- {
		if tmpl.data[i-1] != ' ' && tmpl.data[i-1] != '\t' {
			break
		}
	}

	mayStandalone := (i == 0 || tmpl.data[i-1] == '\n')

	if mayStandalone {
		return &textReadingResult{
			text:          tmpl.data[pPrev:i],
			padding:       tmpl.data[i : tmpl.p-len(tmpl.otag)],
			mayStandalone: true,
		}, nil
	}

	return &textReadingResult{
		text:          tmpl.data[pPrev : tmpl.p-len(tmpl.otag)],
		padding:       "",
		mayStandalone: false,
	}, nil
}

type tagReadingResult struct {
	tag        string
	standalone bool
}

func (tmpl *Template) readTag(mayStandalone bool) (*tagReadingResult, error) {
	var text string
	var err error
	if tmpl.p < len(tmpl.data) && tmpl.data[tmpl.p] == '{' {
		text, err = tmpl.readString("}" + tmpl.ctag)
	} else {
		text, err = tmpl.readString(tmpl.ctag)
	}

	if err == io.EOF {
		//put the remaining text in a block
		return nil, newError(tmpl.curline, ErrUnmatchedOpenTag)
	}

	text = text[:len(text)-len(tmpl.ctag)]

	//trim the close tag off the text
	tag := strings.TrimSpace(text)
	if len(tag) == 0 {
		return nil, newError(tmpl.curline, ErrEmptyTag)
	}

	eow := tmpl.p
	for i := tmpl.p; i < len(tmpl.data); i++ {
		if !(tmpl.data[i] == ' ' || tmpl.data[i] == '\t') {
			eow = i
			break
		}
	}

	standalone := true
	if mayStandalone {
		if !strings.Contains(SkipWhitespaceTagTypes, tag[0:1]) {
			standalone = false
		} else {
			if eow == len(tmpl.data) {
				standalone = true
				tmpl.p = eow
			} else if eow < len(tmpl.data) && tmpl.data[eow] == '\n' {
				standalone = true
				tmpl.p = eow + 1
				tmpl.curline++
			} else if eow+1 < len(tmpl.data) && tmpl.data[eow] == '\r' && tmpl.data[eow+1] == '\n' {
				standalone = true
				tmpl.p = eow + 2
				tmpl.curline++
			} else {
				standalone = false
			}
		}
	}

	return &tagReadingResult{
		tag:        tag,
		standalone: standalone,
	}, nil
}

func (tmpl *Template) parsePartial(name, indent string) (*partialElement, error) {
	return &partialElement{
		name:   name,
		indent: indent,
		prov:   tmpl.partial,
	}, nil
}

func (tmpl *Template) parseSection(section *sectionElement) error {
	for {
		textResult, err := tmpl.readText()
		text := textResult.text
		padding := textResult.padding
		mayStandalone := textResult.mayStandalone

		if err == io.EOF {
			//put the remaining text in a block
			return newErrorWithReason(section.startline, ErrSectionNoClosingTag, section.name)
		}

		// put text into an item
		section.elems = append(section.elems, &textElement{[]byte(text)})

		tagResult, err := tmpl.readTag(mayStandalone)
		if err != nil {
			return err
		}

		if !tagResult.standalone {
			section.elems = append(section.elems, &textElement{[]byte(padding)})
		}

		tag := tagResult.tag
		switch tag[0] {
		case '!':
			//ignore comment
		case '#', '^':
			name := strings.TrimSpace(tag[1:])
			se := sectionElement{name, tag[0] == '^', tmpl.curline, []interface{}{}}
			err := tmpl.parseSection(&se)
			if err != nil {
				return err
			}
			section.elems = append(section.elems, &se)
		case '/':
			name := strings.TrimSpace(tag[1:])
			if name != section.name {
				return newErrorWithReason(tmpl.curline, ErrInterleavedClosingTag, name)
			}
			return nil
		case '>':
			name := strings.TrimSpace(tag[1:])
			partial, err := tmpl.parsePartial(name, textResult.padding)
			if err != nil {
				return err
			}
			section.elems = append(section.elems, partial)
		case '=':
			if tag[len(tag)-1] != '=' {
				return newError(tmpl.curline, ErrInvalidMetaTag)
			}
			tag = strings.TrimSpace(tag[1 : len(tag)-1])
			newtags := strings.SplitN(tag, " ", 2)
			if len(newtags) == 2 {
				tmpl.otag = newtags[0]
				tmpl.ctag = newtags[1]
			}
		case '{':
			if tag[len(tag)-1] == '}' {
				//use a raw tag
				name := strings.TrimSpace(tag[1 : len(tag)-1])
				section.elems = append(section.elems, &varElement{name, true})
			}
		case '&':
			name := strings.TrimSpace(tag[1:])
			section.elems = append(section.elems, &varElement{name, true})
		default:
			section.elems = append(section.elems, &varElement{tag, tmpl.forceRaw})
		}
	}
}

func (tmpl *Template) parse() error {
	for {
		textResult, err := tmpl.readText()
		text := textResult.text
		padding := textResult.padding
		mayStandalone := textResult.mayStandalone

		if err == io.EOF {
			//put the remaining text in a block
			tmpl.elems = append(tmpl.elems, &textElement{[]byte(text)})
			return nil
		}

		// put text into an item
		tmpl.elems = append(tmpl.elems, &textElement{[]byte(text)})

		tagResult, err := tmpl.readTag(mayStandalone)
		if err != nil {
			return err
		}

		if !tagResult.standalone {
			tmpl.elems = append(tmpl.elems, &textElement{[]byte(padding)})
		}

		tag := tagResult.tag
		switch tag[0] {
		case '!':
			//ignore comment
		case '#', '^':
			name := strings.TrimSpace(tag[1:])
			se := sectionElement{name, tag[0] == '^', tmpl.curline, []interface{}{}}
			err := tmpl.parseSection(&se)
			if err != nil {
				return err
			}
			tmpl.elems = append(tmpl.elems, &se)
		case '/':
			return newError(tmpl.curline, ErrUnmatchedCloseTag)
		case '>':
			name := strings.TrimSpace(tag[1:])
			partial, err := tmpl.parsePartial(name, textResult.padding)
			if err != nil {
				return err
			}
			tmpl.elems = append(tmpl.elems, partial)
		case '=':
			if tag[len(tag)-1] != '=' {
				return newError(tmpl.curline, ErrInvalidMetaTag)
			}
			tag = strings.TrimSpace(tag[1 : len(tag)-1])
			newtags := strings.SplitN(tag, " ", 2)
			if len(newtags) == 2 {
				tmpl.otag = newtags[0]
				tmpl.ctag = newtags[1]
			}
		case '{':
			//use a raw tag
			if tag[len(tag)-1] == '}' {
				name := strings.TrimSpace(tag[1 : len(tag)-1])
				tmpl.elems = append(tmpl.elems, &varElement{name, true})
			}
		case '&':
			name := strings.TrimSpace(tag[1:])
			tmpl.elems = append(tmpl.elems, &varElement{name, true})
		default:
			tmpl.elems = append(tmpl.elems, &varElement{tag, tmpl.forceRaw})
		}
	}
}

func lookupAllowMissing(contextChain []interface{}, name string, allowMissing bool) (reflect.Value, error) {
	value, err := lookup(contextChain, nil, name)
	if err != nil && allowMissing {
		if IsMissingVariableError(err) {
			return reflect.Value{}, nil
		}
	}

	return value, err
}

// Evaluate interfaces and pointers looking for a value that can look up the name, via a
// struct field, method, or map key, and return the result of the lookup.
func lookup(contextChain []interface{}, left any, name string) (reflect.Value, error) {
	//fmt.Printf("Lookup %q Left: %v\n", name, left)
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("Panic while looking up %q: %s\n", name, r)
		}
	}()

	// we go from left to right
	index := strings.IndexAny(name, ".[(")
	start := ""
	if index != -1 {
		start = string(name[index])
	}

	// dot notation
	if start == "." && name != "." && strings.Contains(name, ".") {
		parts := strings.SplitN(name, ".", 2)

		v, err := lookup(contextChain, left, parts[0])
		if err != nil {
			return v, err
		}
		if startsWithNumber(parts[1]) {
			return v, newErrorWithReason(0, ErrInvalidVariable, name)
		}

		return lookup(contextChain, v, parts[1])
	} else if start == "[" && strings.Contains(name, "[") && strings.Contains(name, "]") {
		// split into the array name and the index, and the right hand side
		openIndex := strings.Index(name, "[")
		closeIndex := strings.Index(name, "]")

		var arrayVariable, indexVariable, rest string
		// the array is already in the left hand side
		if openIndex > 0 {
			arrayVariable = name[:openIndex]
		}

		indexVariable = name[openIndex+1 : closeIndex]
		if closeIndex+1 < len(name) {
			rest = name[closeIndex+1:]
		}

		if rest != "" && rest[0] == '.' {
			rest = rest[1:]
		}

		var v reflect.Value
		var err error
		if arrayVariable != "" {
			// look up the array
			v, err = lookup(contextChain, left, arrayVariable)
			if err != nil {
				return v, err
			}
		} else {
			v = left.(reflect.Value)
		}
		v = unwrap(v)

		// look up the index
		indexValue, err := lookup(contextChain, nil, indexVariable)
		if err != nil {
			return v, err
		}
		indexValue = unwrap(indexValue)

		//fmt.Printf("left kind %v \n", v.Kind()) //, kindToString(v.Kind()))

		if v.Kind() == reflect.Map {
			v = v.MapIndex(indexValue)
			if !v.IsValid() {
				return v, newErrorWithReason(0, ErrInvalidVariable, name)
			}
		} else if v.Kind() == reflect.Array || v.Kind() == reflect.Slice {
			if indexValue.Kind() != reflect.Int {
				if !indexValue.CanConvert(reflect.TypeOf(int(0))) {
					return v, newErrorWithReason(0, ErrInvalidVariable, name)
				}

				indexValue = indexValue.Convert(reflect.TypeOf(int(0)))
			}

			v = v.Index(int(indexValue.Int()))
		} else {
			return v, newErrorWithReason(0, ErrInvalidVariable, name)
		}

		if rest == "" {
			return v, nil
		}

		return lookup(contextChain, v, rest)
	} else if start == "(" && strings.Contains(name, "(") && strings.Contains(name, ")") {
		openIndex := strings.Index(name, "(")
		closeIndex := strings.Index(name, ")")

		funcVariable := name[:openIndex]
		argsVariable := name[openIndex+1 : closeIndex]
		rest := name[closeIndex+1:]
		if rest != "" && rest[0] == '.' {
			rest = rest[1:]
		}

		// function notation
		// parts := strings.SplitN(name, "(", 2)
		args := []string{}
		if argsVariable != "" {
			args = strings.Split(argsVariable, ",")
		}

		// find the function
		v, err := lookupFunction(contextChain, left, funcVariable, len(args))
		if err != nil {
			return v, err
		}
		v = unwrap(v)

		if v.Kind() != reflect.Func {
			return v, newErrorWithReason(0, ErrInvalidVariable, name)
		}

		// call the function
		in := make([]reflect.Value, 0, len(args))
		for _, arg := range args {
			arg = strings.TrimSpace(arg)
			val, err := lookup(contextChain, nil, arg)
			if err != nil {
				return v, err
			}
			in = append(in, val)
		}

		ret := v.Call(in)
		// If the function returns an error, return the error
		// The error will be the second return value (by convention)
		if len(ret) > 1 {
			if !ret[1].IsNil() {
				return v, ret[1].Interface().(error)
			}
		}

		if rest == "" {
			return ret[0], nil
		}

		return lookup(contextChain, ret[0], rest)

		// Otherwise, return the value
		//return ret[0], nil
	} else if (strings.HasPrefix(name, "\"") && strings.HasSuffix(name, "\"")) || (strings.HasPrefix(name, "'") && strings.HasSuffix(name, "'")) {
		// string literal
		return reflect.ValueOf(name[1 : len(name)-1]), nil
	} else if val, err := strconv.ParseInt(name, 0, 64); err == nil {
		// integer literal
		return reflect.ValueOf(val), nil
	} else if val, err := strconv.ParseUint(name, 0, 64); err == nil {
		// unsigned integer literal
		return reflect.ValueOf(val), nil
	} else if val, err := strconv.ParseFloat(name, 64); err == nil {
		// float literal
		return reflect.ValueOf(val), nil
	} else if val, err := strconv.ParseBool(name); err == nil {
		// boolean literal
		return reflect.ValueOf(val), nil
	} else if val, err := strconv.ParseComplex(name, 64); err == nil {
		// complex literal
		return reflect.ValueOf(val), nil
	}

	localChain := contextChain
	if left != nil {
		localChain = []any{left}
	}

Outer:
	for _, ctx := range localChain {
		v := ctx.(reflect.Value)
		for v.IsValid() {
			typ := v.Type()
			if n := v.Type().NumMethod(); n > 0 {
				for i := 0; i < n; i++ {
					m := typ.Method(i)
					mtyp := m.Type

					// If the method is called Lookup and has the right signature, use it
					if m.Name == "Lookup" && mtyp.NumIn() == 2 && mtyp.NumOut() == 2 {
						// The variable is a struct that implements the Lookup method
						// Lookup(name string) (interface{}, error)
						ret := v.Method(i).Call([]reflect.Value{reflect.ValueOf(name)})

						// If the method returns an error, return the error
						if !ret[1].IsNil() {
							return v, ret[1].Interface().(error)
						}

						// Otherwise, return the value
						return ret[0], nil
					}

					if m.Name == name && mtyp.NumIn() == 1 {
						return v.Method(i).Call(nil)[0], nil
					}
				}
			}
			if name == "." {
				return v, nil
			}
			switch av := v; av.Kind() {
			case reflect.Ptr:
				v = av.Elem()
			case reflect.Interface:
				v = av.Elem()
			case reflect.Struct:
				ret := av.FieldByName(name)
				if ret.IsValid() {
					return ret, nil
				}
				// try to find field by json tag
				ret = av.FieldByNameFunc(func(fieldName string) bool {
					field, _ := av.Type().FieldByName(fieldName)
					// if field doesn't have json tag, skip it
					// if json tag is "-", skip it
					jsonTag := field.Tag.Get("json")
					if jsonTag == "" || jsonTag == "-" {
						return false
					}
					// strip omitempty from json tag
					jsonTag = strings.Split(jsonTag, ",")[0]
					return jsonTag == name
				})
				if ret.IsValid() {
					return ret, nil
				}
				continue Outer
			case reflect.Map:
				ret := av.MapIndex(reflect.ValueOf(name))
				if ret.IsValid() {
					return ret, nil
				}
				continue Outer
			case reflect.Slice:
				if name == "length" || name == "len" {
					return reflect.ValueOf(av.Len()), nil
				}

				index, err := strconv.Atoi(name)
				if err != nil {
					return v, err
				}
				ret := av.Index(index)
				if ret.IsValid() {
					return ret, nil
				}
				continue Outer
			default:
				continue Outer
			}
		}
	}

	return reflect.Value{}, newMissingVariableError(name)
}

func unwrap(v reflect.Value) reflect.Value {
	for v.IsValid() {
		switch v.Kind() {
		case reflect.Ptr:
			v = v.Elem()
		case reflect.Interface:
			v = v.Elem()
		default:
			return v
		}
	}

	return v
}

// lookupFunction looks up a function in the context chain.
// The function can be a method on a struct, a function in a map, or a function in a parent context.
// The function must have the signature func(args...) (ret, error) or func(args...) ret.
func lookupFunction(contextChain []interface{}, left any, s string, numInputs int) (reflect.Value, error) {
	localChain := contextChain
	if left != nil {
		localChain = []any{left}
	}

Outer:
	for _, ctx := range localChain {
		v := ctx.(reflect.Value)
		for v.IsValid() {
			typ := v.Type()
			if n := v.Type().NumMethod(); n > 0 {
				for i := 0; i < n; i++ {
					m := typ.Method(i)
					mtyp := m.Type

					// If the method is called Lookup and has the right signature, use it
					if m.Name == s && mtyp.NumIn() == (numInputs+1) && (mtyp.NumOut() == 1 || mtyp.NumOut() == 2) {
						return v.Method(i), nil
					}
				}
			}
			switch av := v; av.Kind() {
			case reflect.Func:
				mtyp := av.Type()

				if mtyp.NumIn() == numInputs && (mtyp.NumOut() == 1 || mtyp.NumOut() == 2) {
					return av, nil
				}

				continue Outer

			case reflect.Ptr:
				v = av.Elem()
			case reflect.Interface:
				v = av.Elem()
			case reflect.Map:
				v = av.MapIndex(reflect.ValueOf(s))
				continue
			default:
				continue Outer
			}
		}
	}

	return reflect.Value{}, fmt.Errorf("missing function %q", s)
}

func startsWithNumber(s string) bool {
	if len(s) > 0 && s[0] >= '0' && s[0] <= '9' {
		return true
	}

	return false
}

func isEmpty(v reflect.Value) bool {
	if !v.IsValid() || v.Interface() == nil {
		return true
	}

	valueInd := indirect(v)
	if !valueInd.IsValid() {
		return true
	}
	switch val := valueInd; val.Kind() {
	case reflect.Array, reflect.Slice:
		return val.Len() == 0
	case reflect.String:
		return len(strings.TrimSpace(val.String())) == 0
	default:
		return valueInd.IsZero()
	}
}

func indirect(v reflect.Value) reflect.Value {
loop:
	for v.IsValid() {
		switch av := v; av.Kind() {
		case reflect.Ptr:
			v = av.Elem()
		case reflect.Interface:
			v = av.Elem()
		default:
			break loop
		}
	}
	return v
}

func (tmpl *Template) renderSection(section *sectionElement, contextChain []interface{}, buf io.Writer) error {
	value, err := lookupAllowMissing(contextChain, section.name, true)
	if err != nil {
		return err
	}
	var context = contextChain[0].(reflect.Value)
	var contexts = []interface{}{}
	// if the value is nil, check if it's an inverted section
	isEmpty := isEmpty(value)
	if isEmpty && !section.inverted || !isEmpty && section.inverted {
		return nil
	} else if !section.inverted {
		valueInd := indirect(value)
		switch val := valueInd; val.Kind() {
		case reflect.Slice:
			for i := 0; i < val.Len(); i++ {
				contexts = append(contexts, val.Index(i))
			}
		case reflect.Array:
			for i := 0; i < val.Len(); i++ {
				contexts = append(contexts, val.Index(i))
			}
		case reflect.Map, reflect.Struct:
			contexts = append(contexts, value)
		case reflect.Func:
			if val.Type().NumIn() != 2 || val.Type().NumOut() != 2 {
				return fmt.Errorf("lambda %q doesn't match required LambaFunc signature", section.name)
			}
			var text bytes.Buffer
			if err := getSectionText(section.elems, &text); err != nil {
				return err
			}
			render := func(text string) (string, error) {
				tmpl, err := ParseString(text)
				if err != nil {
					return "", err
				}
				var buf bytes.Buffer
				if err := tmpl.renderTemplate(contextChain, &buf); err != nil {
					return "", err
				}
				return buf.String(), nil
			}
			in := []reflect.Value{reflect.ValueOf(text.String()), reflect.ValueOf(render)}
			res := val.Call(in)
			if !res[1].IsNil() {
				return fmt.Errorf("lambda %q: %w", section.name, res[1].Interface().(error))
			}
			fmt.Fprint(buf, res[0].String())
			return nil
		default:
			// Spec: Non-false sections have their value at the top of context,
			// accessible as {{.}} or through the parent context. This gives
			// a simple way to display content conditionally if a variable exists.
			contexts = append(contexts, value)
		}
	} else if section.inverted {
		contexts = append(contexts, context)
	}

	chain2 := make([]interface{}, len(contextChain)+1)
	copy(chain2[1:], contextChain)
	//by default we execute the section
	for _, ctx := range contexts {
		chain2[0] = ctx
		for _, elem := range section.elems {
			if err := tmpl.renderElement(elem, chain2, buf); err != nil {
				return err
			}
		}
	}
	return nil
}

func getSectionText(elements []interface{}, buf io.Writer) error {
	for _, element := range elements {
		if err := getElementText(element, buf); err != nil {
			return err
		}
	}
	return nil
}

func getElementText(element interface{}, buf io.Writer) error {
	switch elem := element.(type) {
	case *textElement:
		fmt.Fprintf(buf, "%s", elem.text)
	case *varElement:
		if elem.raw {
			fmt.Fprintf(buf, "{{{%s}}}", elem.name)
		} else {
			fmt.Fprintf(buf, "{{%s}}", elem.name)
		}
	case *sectionElement:
		if elem.inverted {
			fmt.Fprintf(buf, "{{^%s}}", elem.name)
		} else {
			fmt.Fprintf(buf, "{{#%s}}", elem.name)
		}
		for _, nelem := range elem.elems {
			if err := getElementText(nelem, buf); err != nil {
				return err
			}
		}
		fmt.Fprintf(buf, "{{/%s}}", elem.name)
	default:
		return fmt.Errorf("unexpected element type %T", elem)
	}
	return nil
}

func (tmpl *Template) renderElement(element interface{}, contextChain []interface{}, buf io.Writer) error {
	switch elem := element.(type) {
	case *textElement:
		_, err := buf.Write(elem.text)
		return err
	case *varElement:
		defer func() {
			if r := recover(); r != nil {
				fmt.Printf("Panic while looking up %q: %s\n", elem.name, r)
			}
		}()
		val, err := lookupAllowMissing(contextChain, elem.name, AllowMissingVariables)
		if err != nil {
			return err
		}

		if val.IsValid() {
			if tmpl.formatter != nil {
				s, err := tmpl.formatter(val.Interface())
				if err != nil {
					return err
				}
				_, _ = buf.Write([]byte(s))
			} else if elem.raw {
				fmt.Fprint(buf, val.Interface())
			} else {
				s := fmt.Sprint(val.Interface())
				_, _ = buf.Write([]byte(tmpl.escape(s)))
			}
		}
	case *sectionElement:
		if err := tmpl.renderSection(elem, contextChain, buf); err != nil {
			return err
		}
	case *partialElement:
		partial, err := getPartials(elem.prov, elem.name, elem.indent)
		if err != nil {
			return err
		}
		if err := partial.renderTemplate(contextChain, buf); err != nil {
			return err
		}
	}
	return nil
}

func (tmpl *Template) renderTemplate(contextChain []interface{}, buf io.Writer) error {
	for _, elem := range tmpl.elems {
		if err := tmpl.renderElement(elem, contextChain, buf); err != nil {
			return err
		}
	}
	return nil
}

// FRender uses the given data source - generally a map or struct - to
// render the compiled template to an io.Writer.
func (tmpl *Template) FRender(out io.Writer, context ...interface{}) error {
	var contextChain []interface{}
	for _, c := range context {
		val := reflect.ValueOf(c)
		contextChain = append(contextChain, val)
	}
	return tmpl.renderTemplate(contextChain, out)
}

// Render uses the given data source - generally a map or struct - to render
// the compiled template and return the output.
func (tmpl *Template) Render(context ...interface{}) (string, error) {
	var buf bytes.Buffer
	err := tmpl.FRender(&buf, context...)
	return buf.String(), err
}

// RenderInLayout uses the given data source - generally a map or struct - to
// render the compiled template and layout "wrapper" template and return the
// output.
func (tmpl *Template) RenderInLayout(layout *Template, context ...interface{}) (string, error) {
	var buf bytes.Buffer
	err := tmpl.FRenderInLayout(&buf, layout, context...)
	if err != nil {
		return "", err
	}
	return buf.String(), nil
}

// FRenderInLayout uses the given data source - generally a map or
// struct - to render the compiled templated a loayout "wrapper"
// template to an io.Writer.
func (tmpl *Template) FRenderInLayout(out io.Writer, layout *Template, context ...interface{}) error {
	content, err := tmpl.Render(context...)
	if err != nil {
		return err
	}
	allContext := make([]interface{}, len(context)+1)
	copy(allContext[1:], context)
	allContext[0] = map[string]string{"content": content}
	return layout.FRender(out, allContext...)
}

// ParseString compiles a mustache template string. The resulting output can
// be used to efficiently render the template multiple times with different data
// sources.
func ParseString(data string) (*Template, error) {
	return ParseStringRaw(data, false)
}

// ParseStringRaw compiles a mustache template string. The resulting output can
// be used to efficiently render the template multiple times with different data
// sources.
func ParseStringRaw(data string, forceRaw bool) (*Template, error) {
	cwd := os.Getenv("CWD")
	partials := &FileProvider{
		Paths: []string{cwd, " "},
	}

	return ParseStringPartialsRaw(data, partials, forceRaw)
}

// ParseStringWithFormatter compiles a mustache template string. The resulting output can
// be used to efficiently render the template multiple times with different data
// sources.
// The formatter function is used to format the output of the template.
func ParseStringWithFormatter(data string, formatter FormatterFunc) (*Template, error) {
	cwd := os.Getenv("CWD")
	partials := &FileProvider{
		Paths: []string{cwd, " "},
	}

	return ParseStringPartialsWithFormatter(data, partials, formatter)
}

// ParseStringPartials compiles a mustache template string, retrieving any
// required partials from the given provider. The resulting output can be used
// to efficiently render the template multiple times with different data
// sources.
func ParseStringPartials(data string, partials PartialProvider) (*Template, error) {
	return ParseStringPartialsRaw(data, partials, false)
}

// ParseStringPartialsRaw compiles a mustache template string, retrieving any
// required partials from the given provider. The resulting output can be used
// to efficiently render the template multiple times with different data
// sources.
func ParseStringPartialsRaw(data string, partials PartialProvider, forceRaw bool) (*Template, error) {
	tmpl := Template{data, "{{", "}}", 0, 1, []interface{}{}, forceRaw, partials, template.HTMLEscapeString, nil}
	err := tmpl.parse()

	if err != nil {
		return nil, err
	}

	return &tmpl, err
}

// ParseStringPartialsWithFormatter compiles a mustache template string, retrieving any
// required partials from the given provider. The resulting output can be used
// to efficiently render the template multiple times with different data
// sources.
// The formatter function is used to format the output of the template.

func ParseStringPartialsWithFormatter(data string, partials PartialProvider, formatter FormatterFunc) (*Template, error) {
	tmpl := Template{data, "{{", "}}", 0, 1, []interface{}{}, true, partials, template.HTMLEscapeString, formatter}
	err := tmpl.parse()

	if err != nil {
		return nil, err
	}

	return &tmpl, err
}

// ParseFile loads a mustache template string from a file and compiles it. The
// resulting output can be used to efficiently render the template multiple
// times with different data sources.
func ParseFile(filename string) (*Template, error) {
	dirname, _ := path.Split(filename)
	partials := &FileProvider{
		Paths: []string{dirname, " "},
	}

	return ParseFilePartials(filename, partials)
}

// ParseFilePartials loads a mustache template string from a file, retrieving any
// required partials from the given provider, and compiles it. The resulting
// output can be used to efficiently render the template multiple times with
// different data sources.
func ParseFilePartials(filename string, partials PartialProvider) (*Template, error) {
	return ParseFilePartialsRaw(filename, false, partials)
}

// ParseFileWithFormatter loads a mustache template string from a file and compiles it. The
// resulting output can be used to efficiently render the template multiple
// times with different data sources.
// The formatter function is used to format the output of the template.

func ParseFileWithFormatter(filename string, formatter FormatterFunc) (*Template, error) {
	dirname, _ := path.Split(filename)
	partials := &FileProvider{
		Paths: []string{dirname, " "},
	}

	return ParseFilePartialsWithFormatter(filename, partials, formatter)
}

// ParseFilePartialsRaw loads a mustache template string from a file, retrieving
// any required partials from the given provider, and compiles it. The resulting
// output can be used to efficiently render the template multiple times with
// different data sources.
func ParseFilePartialsRaw(filename string, forceRaw bool, partials PartialProvider) (*Template, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	tmpl := Template{string(data), "{{", "}}", 0, 1, []interface{}{}, forceRaw, partials, template.HTMLEscapeString, nil}
	err = tmpl.parse()

	if err != nil {
		return nil, err
	}

	return &tmpl, nil
}

// ParseFilePartialsWithFormatter loads a mustache template string from a file, retrieving
// any required partials from the given provider, and compiles it. The resulting
// output can be used to efficiently render the template multiple times with
// different data sources.
// The formatter function is used to format the output of the template.
func ParseFilePartialsWithFormatter(filename string, partials PartialProvider, formatter FormatterFunc) (*Template, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	tmpl := Template{string(data), "{{", "}}", 0, 1, []interface{}{}, true, partials, template.HTMLEscapeString, formatter}
	err = tmpl.parse()

	if err != nil {
		return nil, err
	}

	return &tmpl, nil
}

// Render compiles a mustache template string and uses the the given data source
// - generally a map or struct - to render the template and return the output.
func Render(data string, context ...interface{}) (string, error) {
	return RenderRaw(data, false, context...)
}

// RenderRaw compiles a mustache template string and uses the the given data
// source - generally a map or struct - to render the template and return the
// output.
func RenderRaw(data string, forceRaw bool, context ...interface{}) (string, error) {
	return RenderPartialsRaw(data, nil, forceRaw, context...)
}

// RenderPartialsWithFormatter compiles a mustache template string and uses the the given partial
// provider and data source - generally a map or struct - to render the template
// and return the output.
// The formatter function is used to format the output of the template.
func RenderWithFormatter(data string, formatter FormatterFunc, context ...interface{}) (string, error) {
	return RenderPartialsWithFormatter(data, nil, formatter, context...)
}

// RenderPartials compiles a mustache template string and uses the the given partial
// provider and data source - generally a map or struct - to render the template
// and return the output.
func RenderPartials(data string, partials PartialProvider, context ...interface{}) (string, error) {
	return RenderPartialsRaw(data, partials, false, context...)
}

// RenderPartialsRaw compiles a mustache template string and uses the the given
// partial provider and data source - generally a map or struct - to render the
// template and return the output.
func RenderPartialsRaw(data string, partials PartialProvider, forceRaw bool, context ...interface{}) (string, error) {
	var tmpl *Template
	var err error
	if partials == nil {
		tmpl, err = ParseStringRaw(data, forceRaw)
	} else {
		tmpl, err = ParseStringPartialsRaw(data, partials, forceRaw)
	}
	if err != nil {
		return "", err
	}
	return tmpl.Render(context...)
}

// RenderPartialsWithFormatter compiles a mustache template string and uses the the given partial
// provider and data source - generally a map or struct - to render the template
// and return the output.
// The formatter function is used to format the output of the template.
func RenderPartialsWithFormatter(data string, partials PartialProvider, formatter FormatterFunc, context ...interface{}) (string, error) {
	var tmpl *Template
	var err error
	if partials == nil {
		tmpl, err = ParseStringWithFormatter(data, formatter)
	} else {
		tmpl, err = ParseStringPartialsWithFormatter(data, partials, formatter)
	}
	if err != nil {
		return "", err
	}
	tmpl.formatter = formatter
	return tmpl.Render(context...)
}

// RenderInLayout compiles a mustache template string and layout "wrapper" and
// uses the given data source - generally a map or struct - to render the
// compiled templates and return the output.
func RenderInLayout(data string, layoutData string, context ...interface{}) (string, error) {
	return RenderInLayoutPartials(data, layoutData, nil, context...)
}

// RenderInLayoutPartials compiles a mustache template string and layout
// "wrapper" and uses the given data source - generally a map or struct - to
// render the compiled templates and return the output.
func RenderInLayoutPartials(data string, layoutData string, partials PartialProvider, context ...interface{}) (string, error) {
	var layoutTmpl, tmpl *Template
	var err error
	if partials == nil {
		layoutTmpl, err = ParseString(layoutData)
	} else {
		layoutTmpl, err = ParseStringPartials(layoutData, partials)
	}
	if err != nil {
		return "", err
	}

	if partials == nil {
		tmpl, err = ParseString(data)
	} else {
		tmpl, err = ParseStringPartials(data, partials)
	}

	if err != nil {
		return "", err
	}

	return tmpl.RenderInLayout(layoutTmpl, context...)
}

// RenderFile loads a mustache template string from a file and compiles it, and
// then uses the given data source - generally a map or struct - to render the
// template and return the output.
func RenderFile(filename string, context ...interface{}) (string, error) {
	tmpl, err := ParseFile(filename)
	if err != nil {
		return "", err
	}
	return tmpl.Render(context...)
}

// RenderFileInLayout loads a mustache template string and layout "wrapper"
// template string from files and compiles them, and  then uses the the given
// data source - generally a map or struct - to render the compiled templates
// and return the output.
func RenderFileInLayout(filename string, layoutFile string, context ...interface{}) (string, error) {
	layoutTmpl, err := ParseFile(layoutFile)
	if err != nil {
		return "", err
	}

	tmpl, err := ParseFile(filename)
	if err != nil {
		return "", err
	}
	return tmpl.RenderInLayout(layoutTmpl, context...)
}

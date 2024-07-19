package mustache

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

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
			return v, newInvalidVariableError(name)
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
				return v, newInvalidVariableError(name)
			}
		} else if v.Kind() == reflect.Array || v.Kind() == reflect.Slice {
			if indexValue.Kind() != reflect.Int {
				if !indexValue.CanConvert(reflect.TypeOf(int(0))) {
					return v, newInvalidVariableError(name)
				}

				indexValue = indexValue.Convert(reflect.TypeOf(int(0)))
			}

			v = v.Index(int(indexValue.Int()))
		} else {
			return v, newInvalidVariableError(name)
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
			return v, newInvalidVariableError(name)
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

// Package validation validates structs by `validate` tags, in the spirit of
// Laravel's validator, and binds requests to them (Bind).
//
//	type SignUp struct {
//	    Name     string `json:"name" validate:"required,max=100"`
//	    Email    string `json:"email" validate:"required,email"`
//	    Password string `json:"password" validate:"required,min=8"`
//	    Confirm  string `json:"password_confirmation" validate:"same=Password"`
//	    Age      int    `json:"age" validate:"omitempty,between=13 120"`
//	    Role     string `json:"role" validate:"oneof=user admin"`
//	}
//
//	if err := validation.Struct(&in); err != nil {
//	    errs := err.(validation.Errors) // {"email": ["The email field must be a valid email address."]}
//	}
//
// Rules other than required, required_with, required_without and accepted
// are skipped for empty values (as in Laravel): "", whitespace, nil and empty
// collections. 0 and false are values; with "omitempty" (or "nullable") they
// count as empty too. Field names in errors are the json (or form) tag names;
// nested structs and slices of structs are validated too, as "address.city"
// and "items.0.name".
package validation

import (
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// Errors maps a field to its messages. It is the error Struct returns.
type Errors map[string][]string

func (e Errors) Error() string {
	keys := make([]string, 0, len(e))
	for k := range e {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, strings.Join(e[k], " "))
	}
	return strings.Join(parts, " ")
}

// Add appends a message for a field (for checks done by hand, such as "the
// email is already taken").
func (e Errors) Add(field, msg string) { e[field] = append(e[field], msg) }

// First returns the first message of a field ("" if it has none).
func (e Errors) First(field string) string {
	if m := e[field]; len(m) > 0 {
		return m[0]
	}
	return ""
}

// Has reports whether a field has errors.
func (e Errors) Has(field string) bool { return len(e[field]) > 0 }

// Field is what a rule checks.
type Field struct {
	Name   string        // the field's name in errors ("email", "items.0.name")
	Value  reflect.Value // the value, pointers dereferenced (invalid for a nil pointer)
	Param  string        // the rule's parameter: "8" in min=8
	Parent reflect.Value // the struct holding the field (for same=, required_with=)
}

// String is the value as a string (strings as they are, numbers formatted).
func (f Field) String() string {
	if !f.Value.IsValid() {
		return ""
	}
	if f.Value.Kind() == reflect.String {
		return f.Value.String()
	}
	return fmt.Sprint(f.Value.Interface())
}

// Rule reports whether a field passes.
type Rule func(f Field) bool

// Validator holds rules and messages. The zero value is not usable; use New.
type Validator struct {
	mu       sync.RWMutex
	rules    map[string]Rule
	implicit map[string]bool // rules that run on empty values
	messages map[string]string
}

// Default is the validator of the package-level functions.
var Default = New()

// New returns a validator with the built-in rules and English messages.
func New() *Validator {
	v := &Validator{rules: map[string]Rule{}, implicit: map[string]bool{}, messages: map[string]string{}}
	for name, r := range builtin {
		v.rules[name] = r
	}
	for name := range implicitRules {
		v.implicit[name] = true
	}
	for k, m := range defaultMessages {
		v.messages[k] = m
	}
	return v
}

// Register adds or replaces a rule with its message ({field} and {param}
// are replaced). An implicit rule also runs on empty values.
func (v *Validator) Register(name string, r Rule, message string, implicit ...bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.rules[name] = r
	v.messages[name] = message
	v.implicit[name] = len(implicit) > 0 && implicit[0]
}

// SetMessage replaces the message of a rule ("required"), or of a rule for
// one field ("email.required").
func (v *Validator) SetMessage(key, message string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.messages[key] = message
}

// Register adds a rule to Default.
func Register(name string, r Rule, message string, implicit ...bool) {
	Default.Register(name, r, message, implicit...)
}

// Struct validates a struct (or a pointer to one) with Default.
func Struct(s any) error { return Default.Struct(s) }

// Struct validates a struct (or a pointer to one): nil, Errors, or an error
// for an unknown rule or a value that is not a struct.
func (v *Validator) Struct(s any) error {
	rv := reflect.ValueOf(s)
	for rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			return errors.New("validation: nil pointer")
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return fmt.Errorf("validation: Struct expects a struct, got %T", s)
	}
	errs := Errors{}
	if err := v.validateStruct(rv, "", errs); err != nil {
		return err
	}
	if len(errs) == 0 {
		return nil
	}
	return errs
}

func (v *Validator) validateStruct(rv reflect.Value, prefix string, errs Errors) error {
	t := rv.Type()
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if !sf.IsExported() {
			continue
		}
		fv := rv.Field(i)
		if sf.Anonymous && sf.Type.Kind() == reflect.Struct {
			if err := v.validateStruct(fv, prefix, errs); err != nil {
				return err
			}
			continue
		}
		name := prefix + FieldName(sf)
		if name == prefix+"-" {
			continue
		}
		tag := sf.Tag.Get("validate")
		if tag != "-" && tag != "" {
			if err := v.validateField(name, fv, rv, tag, errs); err != nil {
				return err
			}
		}
		if tag == "-" {
			continue
		}
		// Nested structs and slices of structs.
		d := deref(fv)
		switch {
		case d.IsValid() && d.Kind() == reflect.Struct && d.Type() != timeType:
			if err := v.validateStruct(d, name+".", errs); err != nil {
				return err
			}
		case d.IsValid() && (d.Kind() == reflect.Slice || d.Kind() == reflect.Array):
			for j := 0; j < d.Len(); j++ {
				e := deref(d.Index(j))
				if e.IsValid() && e.Kind() == reflect.Struct && e.Type() != timeType {
					if err := v.validateStruct(e, name+"."+strconv.Itoa(j)+".", errs); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func (v *Validator) validateField(name string, fv, parent reflect.Value, tag string, errs Errors) error {
	v.mu.RLock()
	defer v.mu.RUnlock()
	parts := splitRules(tag)
	empty := isEmpty(fv)
	for _, part := range parts {
		if p := strings.TrimSpace(part); (p == "omitempty" || p == "nullable") && !empty {
			empty = deref(fv).IsValid() && deref(fv).IsZero() // 0 and false count as empty too
		}
	}
	for _, part := range parts {
		rule, param, _ := strings.Cut(part, "=")
		rule = strings.TrimSpace(rule)
		if rule == "" || rule == "omitempty" || rule == "nullable" {
			continue
		}
		r, ok := v.rules[rule]
		if !ok {
			return fmt.Errorf("validation: unknown rule %q on %s", rule, name)
		}
		if empty && !v.implicit[rule] {
			continue
		}
		f := Field{Name: name, Value: deref(fv), Param: param, Parent: parent}
		if !r(f) {
			errs.Add(name, v.message(rule, f))
			if rule == "required" {
				return nil // the other messages would only repeat it
			}
		}
	}
	return nil
}

// splitRules splits a tag on commas, except inside a regex=/.../ parameter.
func splitRules(tag string) []string {
	if i := strings.Index(tag, "regex="); i >= 0 {
		head := strings.TrimSuffix(tag[:i], ",")
		var out []string
		if head != "" {
			out = strings.Split(head, ",")
		}
		return append(out, tag[i:])
	}
	return strings.Split(tag, ",")
}

func (v *Validator) message(rule string, f Field) string {
	base := f.Name
	if i := strings.LastIndexByte(base, '.'); i >= 0 {
		base = base[i+1:]
	}
	m, ok := v.messages[base+"."+rule]
	if !ok {
		m, ok = v.messages[rule]
	}
	if !ok {
		m = "The {field} field is invalid."
	}
	if rule == "min" || rule == "max" || rule == "between" || rule == "size" {
		if k := sizeKind(f.Value); k != "" {
			if km, ok := v.messages[rule+"."+k]; ok {
				m = km
			}
		}
	}
	param := f.Param
	if rule == "between" {
		param = strings.Join(strings.Fields(param), " and ")
	}
	if rule == "oneof" {
		param = strings.Join(strings.Fields(param), ", ")
	}
	switch rule {
	case "same", "different", "required_with", "required_without":
		param = humanize(nameOf(f.Parent, param))
	}
	return strings.NewReplacer("{field}", humanize(f.Name), "{param}", param).Replace(m)
}

// nameOf returns the error name (json/form tag) of a sibling given by Go name.
func nameOf(parent reflect.Value, name string) string {
	if parent.IsValid() {
		if sf, ok := parent.Type().FieldByName(name); ok {
			return FieldName(sf)
		}
	}
	return name
}

// humanize turns "address.zip_code" into "zip code".
func humanize(name string) string {
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		name = name[i+1:]
	}
	var b strings.Builder
	for i, r := range name {
		switch {
		case r == '_' || r == '-':
			b.WriteByte(' ')
		case unicode.IsUpper(r) && i > 0:
			b.WriteByte(' ')
			b.WriteRune(unicode.ToLower(r))
		default:
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

// FieldName is the name of a struct field in errors and forms: its json
// tag, else its form tag, else the Go name.
func FieldName(sf reflect.StructField) string {
	for _, key := range []string{"json", "form"} {
		if n, _, _ := strings.Cut(sf.Tag.Get(key), ","); n != "" {
			return n
		}
	}
	return sf.Name
}

var timeType = reflect.TypeOf(time.Time{})

func deref(v reflect.Value) reflect.Value {
	for v.IsValid() && (v.Kind() == reflect.Ptr || v.Kind() == reflect.Interface) {
		if v.IsNil() {
			return reflect.Value{}
		}
		v = v.Elem()
	}
	return v
}

func isEmpty(v reflect.Value) bool {
	v = deref(v)
	if !v.IsValid() {
		return true
	}
	switch v.Kind() {
	case reflect.String:
		return strings.TrimSpace(v.String()) == ""
	case reflect.Slice, reflect.Map, reflect.Array:
		return v.Len() == 0
	case reflect.Struct:
		if v.Type() == timeType {
			return v.Interface().(time.Time).IsZero()
		}
		return false
	}
	return false // numbers and bools: 0 and false are values
}

// sizeKind names how min/max measure a value: string, numeric or array.
func sizeKind(v reflect.Value) string {
	switch v.Kind() {
	case reflect.String:
		return "string"
	case reflect.Slice, reflect.Map, reflect.Array:
		return "array"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return "numeric"
	}
	return ""
}

// size is a string's length in characters, a number's value, a
// collection's length.
func size(v reflect.Value) (float64, bool) {
	switch v.Kind() {
	case reflect.String:
		return float64(utf8.RuneCountInString(v.String())), true
	case reflect.Slice, reflect.Map, reflect.Array:
		return float64(v.Len()), true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(v.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(v.Uint()), true
	case reflect.Float32, reflect.Float64:
		return v.Float(), true
	}
	return 0, false
}

func compareSize(f Field, ok func(have, want float64) bool) bool {
	want, err := strconv.ParseFloat(strings.TrimSpace(f.Param), 64)
	if err != nil {
		return false
	}
	have, valid := size(f.Value)
	return valid && ok(have, want)
}

func sibling(f Field, name string) reflect.Value {
	if !f.Parent.IsValid() {
		return reflect.Value{}
	}
	// By Go name, or by json/form name.
	if sv := f.Parent.FieldByName(name); sv.IsValid() {
		return sv
	}
	t := f.Parent.Type()
	for i := 0; i < t.NumField(); i++ {
		if FieldName(t.Field(i)) == name {
			return f.Parent.Field(i)
		}
	}
	return reflect.Value{}
}

var (
	alphaRe    = regexp.MustCompile(`^\pL+$`)
	alphaNumRe = regexp.MustCompile(`^[\pL\pN]+$`)
	alphaDash  = regexp.MustCompile(`^[\pL\pN_-]+$`)
	uuidRe     = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	regexCache sync.Map
)

var implicitRules = map[string]bool{"required": true, "required_with": true, "required_without": true, "accepted": true}

var builtin = map[string]Rule{
	"required": func(f Field) bool { return !isEmpty(f.Value) },
	"required_with": func(f Field) bool {
		return isEmpty(sibling(f, f.Param)) || !isEmpty(f.Value)
	},
	"required_without": func(f Field) bool {
		return !isEmpty(sibling(f, f.Param)) || !isEmpty(f.Value)
	},
	"accepted": func(f Field) bool {
		switch strings.ToLower(f.String()) {
		case "1", "true", "yes", "on":
			return true
		}
		return false
	},
	"email": func(f Field) bool {
		s := f.String()
		a, err := mail.ParseAddress(s)
		return err == nil && a.Address == s && strings.Contains(s[strings.LastIndexByte(s, '@'):], ".")
	},
	"url": func(f Field) bool {
		u, err := url.Parse(f.String())
		return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
	},
	"uuid":       func(f Field) bool { return uuidRe.MatchString(f.String()) },
	"alpha":      func(f Field) bool { return alphaRe.MatchString(f.String()) },
	"alpha_num":  func(f Field) bool { return alphaNumRe.MatchString(f.String()) },
	"alpha_dash": func(f Field) bool { return alphaDash.MatchString(f.String()) },
	"numeric": func(f Field) bool {
		if sizeKind(f.Value) == "numeric" {
			return true
		}
		_, err := strconv.ParseFloat(strings.TrimSpace(f.String()), 64)
		return err == nil
	},
	"integer": func(f Field) bool {
		switch f.Value.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			return true
		}
		_, err := strconv.ParseInt(strings.TrimSpace(f.String()), 10, 64)
		return err == nil
	},
	"min":  func(f Field) bool { return compareSize(f, func(h, w float64) bool { return h >= w }) },
	"max":  func(f Field) bool { return compareSize(f, func(h, w float64) bool { return h <= w }) },
	"size": func(f Field) bool { return compareSize(f, func(h, w float64) bool { return h == w }) },
	"gt":   func(f Field) bool { return compareSize(f, func(h, w float64) bool { return h > w }) },
	"lt":   func(f Field) bool { return compareSize(f, func(h, w float64) bool { return h < w }) },
	"between": func(f Field) bool {
		p := strings.Fields(f.Param)
		if len(p) != 2 {
			return false
		}
		return compareSize(Field{Value: f.Value, Param: p[0]}, func(h, w float64) bool { return h >= w }) &&
			compareSize(Field{Value: f.Value, Param: p[1]}, func(h, w float64) bool { return h <= w })
	},
	"oneof": func(f Field) bool {
		s := f.String()
		for _, opt := range strings.Fields(f.Param) {
			if s == opt {
				return true
			}
		}
		return false
	},
	"same": func(f Field) bool {
		o := deref(sibling(f, f.Param))
		return o.IsValid() && reflect.DeepEqual(o.Interface(), f.Value.Interface())
	},
	"different": func(f Field) bool {
		o := deref(sibling(f, f.Param))
		return !o.IsValid() || !reflect.DeepEqual(o.Interface(), f.Value.Interface())
	},
	"regex": func(f Field) bool {
		p := f.Param
		if len(p) >= 2 && p[0] == '/' && p[len(p)-1] == '/' {
			p = p[1 : len(p)-1]
		}
		re, ok := regexCache.Load(p)
		if !ok {
			c, err := regexp.Compile(p)
			if err != nil {
				return false
			}
			re, _ = regexCache.LoadOrStore(p, c)
		}
		return re.(*regexp.Regexp).MatchString(f.String())
	},
	"date": func(f Field) bool {
		if f.Value.Type() == timeType {
			return true
		}
		s := f.String()
		for _, layout := range []string{time.RFC3339, "2006-01-02", "2006-01-02 15:04:05"} {
			if _, err := time.Parse(layout, s); err == nil {
				return true
			}
		}
		return false
	},
}

var defaultMessages = map[string]string{
	"required":         "The {field} field is required.",
	"required_with":    "The {field} field is required when {param} is present.",
	"required_without": "The {field} field is required when {param} is not present.",
	"accepted":         "The {field} field must be accepted.",
	"email":            "The {field} field must be a valid email address.",
	"url":              "The {field} field must be a valid URL.",
	"uuid":             "The {field} field must be a valid UUID.",
	"alpha":            "The {field} field must only contain letters.",
	"alpha_num":        "The {field} field must only contain letters and numbers.",
	"alpha_dash":       "The {field} field must only contain letters, numbers, dashes, and underscores.",
	"numeric":          "The {field} field must be a number.",
	"integer":          "The {field} field must be an integer.",
	"min":              "The {field} field must be at least {param}.",
	"min.string":       "The {field} field must be at least {param} characters.",
	"min.array":        "The {field} field must have at least {param} items.",
	"max":              "The {field} field must not be greater than {param}.",
	"max.string":       "The {field} field must not be greater than {param} characters.",
	"max.array":        "The {field} field must not have more than {param} items.",
	"size":             "The {field} field must be {param}.",
	"size.string":      "The {field} field must be {param} characters.",
	"size.array":       "The {field} field must contain {param} items.",
	"between":          "The {field} field must be between {param}.",
	"between.string":   "The {field} field must be between {param} characters.",
	"between.array":    "The {field} field must have between {param} items.",
	"gt":               "The {field} field must be greater than {param}.",
	"lt":               "The {field} field must be less than {param}.",
	"oneof":            "The selected {field} is invalid (one of {param}).",
	"same":             "The {field} field must match {param}.",
	"different":        "The {field} field and {param} must be different.",
	"regex":            "The {field} field format is invalid.",
	"date":             "The {field} field must be a valid date.",
}

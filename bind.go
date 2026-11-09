package validation

import (
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"

	"fullstack/web"
)

// MaxMemory is the part of a multipart form kept in memory by Bind (32 MiB);
// files beyond it go to temporary files.
var MaxMemory int64 = 32 << 20

// InvalidMessage is the message of Bind's 422 response.
var InvalidMessage = "The given data was invalid."

// Bind reads the request into dst and validates it with Default: a JSON
// body (Content-Type application/json), a form (urlencoded or multipart) or,
// for GET and HEAD, the query string. Invalid data is a 422 HTTPError whose
// JSON response is {"error": "The given data was invalid.", "errors": {...}};
// malformed JSON is a 400.
//
//	r.Post("/users", func(c *web.Ctx) error {
//	    var in SignUp
//	    if err := validation.Bind(c, &in); err != nil {
//	        return err
//	    }
//	    ...
//	})
func Bind(c *web.Ctx, dst any) error { return Default.Bind(c, dst) }

// Bind is the package-level Bind with this validator.
func (v *Validator) Bind(c *web.Ctx, dst any) error {
	errs := Errors{}
	r := c.R
	ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	switch {
	case ct == "application/json":
		if err := c.DecodeJSON(dst); err != nil {
			return err
		}
	case r.Method == http.MethodGet || r.Method == http.MethodHead:
		decodeValues(r.URL.Query(), dst, errs)
	case ct == "multipart/form-data":
		if err := r.ParseMultipartForm(MaxMemory); err != nil {
			return web.Wrap(http.StatusBadRequest, err)
		}
		decodeValues(r.PostForm, dst, errs)
	default:
		if err := r.ParseForm(); err != nil {
			return web.Wrap(http.StatusBadRequest, err)
		}
		decodeValues(r.PostForm, dst, errs)
	}
	err := v.Struct(dst)
	if verrs, ok := err.(Errors); ok {
		for k, m := range verrs {
			if !errs.Has(k) { // a type error says it better
				errs[k] = m
			}
		}
	} else if err != nil {
		return err
	}
	if len(errs) > 0 {
		return Invalid(errs)
	}
	return nil
}

// ErrorsOf returns the validation Errors inside err (a Bind or Invalid
// error), to show them again in a form:
//
//	if err := validation.Bind(c, &in); err != nil {
//	    if errs, ok := validation.ErrorsOf(err); ok {
//	        return c.HTML(422, "pages/register", map[string]any{"Errors": errs})
//	    }
//	    return err
//	}
func ErrorsOf(err error) (Errors, bool) {
	var e Errors
	ok := errors.As(err, &e)
	return e, ok
}

// Invalid is the 422 HTTPError of a set of Errors.
func Invalid(errs Errors) *web.HTTPError {
	return &web.HTTPError{Status: http.StatusUnprocessableEntity, Message: InvalidMessage, Err: errs,
		Fields: map[string]any{"errors": errs}}
}

// DecodeForm fills dst from form values by `form` tags (else json tags,
// else Go names): strings, numbers, bools (a checkbox's "on"), time.Time,
// pointers and slices of those, nested structs as "address.city". Values
// that do not parse are returned as Errors.
func DecodeForm(values url.Values, dst any) error {
	errs := Errors{}
	decodeValues(values, dst, errs)
	if len(errs) > 0 {
		return errs
	}
	return nil
}

func decodeValues(values url.Values, dst any, errs Errors) {
	rv := reflect.ValueOf(dst)
	if rv.Kind() != reflect.Ptr || rv.Elem().Kind() != reflect.Struct {
		errs.Add("_", fmt.Sprintf("validation: decoding expects a pointer to a struct, got %T", dst))
		return
	}
	decodeStruct(values, rv.Elem(), "", errs)
}

func formName(sf reflect.StructField) string {
	for _, key := range []string{"form", "json"} {
		if n, _, _ := strings.Cut(sf.Tag.Get(key), ","); n != "" {
			return n
		}
	}
	return sf.Name
}

func decodeStruct(values url.Values, rv reflect.Value, prefix string, errs Errors) {
	t := rv.Type()
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if !sf.IsExported() {
			continue
		}
		fv := rv.Field(i)
		if sf.Anonymous && sf.Type.Kind() == reflect.Struct {
			decodeStruct(values, fv, prefix, errs)
			continue
		}
		name := formName(sf)
		if name == "-" {
			continue
		}
		key := prefix + name
		ft := sf.Type
		if ft.Kind() == reflect.Struct && ft != timeType {
			decodeStruct(values, fv, key+".", errs)
			continue
		}
		vals, ok := values[key]
		if !ok {
			vals, ok = values[key+"[]"]
		}
		if !ok {
			continue
		}
		if ft.Kind() == reflect.Slice && ft.Elem().Kind() != reflect.Uint8 {
			out := reflect.MakeSlice(ft, 0, len(vals))
			for _, s := range vals {
				e := reflect.New(ft.Elem()).Elem()
				if msg := setValue(e, s); msg != "" {
					errs.Add(key, strings.ReplaceAll(msg, "{field}", humanize(key)))
					break
				}
				out = reflect.Append(out, e)
			}
			fv.Set(out)
			continue
		}
		if msg := setValue(fv, vals[len(vals)-1]); msg != "" {
			errs.Add(key, strings.ReplaceAll(msg, "{field}", humanize(key)))
		}
	}
}

// setValue parses s into v; it returns a message for a value that does not
// parse.
func setValue(v reflect.Value, s string) string {
	if v.Kind() == reflect.Ptr {
		if s == "" {
			v.Set(reflect.Zero(v.Type())) // an empty input is "not given"
			return ""
		}
		p := reflect.New(v.Type().Elem())
		if msg := setValue(p.Elem(), s); msg != "" {
			return msg
		}
		v.Set(p)
		return ""
	}
	if v.Type() == timeType {
		if s == "" {
			v.Set(reflect.Zero(timeType))
			return ""
		}
		for _, layout := range []string{time.RFC3339, "2006-01-02T15:04", "2006-01-02 15:04:05", "2006-01-02"} {
			if t, err := time.Parse(layout, s); err == nil {
				v.Set(reflect.ValueOf(t))
				return ""
			}
		}
		return "The {field} field must be a valid date."
	}
	s2 := strings.TrimSpace(s)
	switch v.Kind() {
	case reflect.String:
		v.SetString(s)
	case reflect.Bool:
		switch strings.ToLower(s2) {
		case "1", "true", "on", "yes":
			v.SetBool(true)
		case "", "0", "false", "off", "no":
			v.SetBool(false)
		default:
			return "The {field} field must be true or false."
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if s2 == "" {
			v.SetInt(0)
			return ""
		}
		n, err := strconv.ParseInt(s2, 10, v.Type().Bits())
		if err != nil {
			return "The {field} field must be an integer."
		}
		v.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if s2 == "" {
			v.SetUint(0)
			return ""
		}
		n, err := strconv.ParseUint(s2, 10, v.Type().Bits())
		if err != nil {
			return "The {field} field must be a positive integer."
		}
		v.SetUint(n)
	case reflect.Float32, reflect.Float64:
		if s2 == "" {
			v.SetFloat(0)
			return ""
		}
		n, err := strconv.ParseFloat(s2, v.Type().Bits())
		if err != nil {
			return "The {field} field must be a number."
		}
		v.SetFloat(n)
	default:
		return "The {field} field cannot be read from a form."
	}
	return ""
}

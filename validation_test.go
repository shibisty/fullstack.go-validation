package validation

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

type address struct {
	City string `json:"city" validate:"required"`
	Zip  string `json:"zip_code" validate:"regex=^[0-9]{5}$"`
}

type item struct {
	Name string `json:"name" validate:"required,max=5"`
}

type Base struct {
	Token string `json:"token" validate:"required"`
}

type signUp struct {
	Base
	Name     string     `json:"name" validate:"required,max=10"`
	Email    string     `json:"email" validate:"required,email"`
	Password string     `json:"password" validate:"required,min=8"`
	Confirm  string     `json:"password_confirmation" validate:"same=Password"`
	Age      int        `json:"age" validate:"omitempty,between=13 120"`
	Role     string     `json:"role" validate:"oneof=user admin"`
	Site     string     `json:"site" validate:"url"`
	Terms    string     `json:"terms" validate:"accepted"`
	Phone    string     `json:"phone" validate:"required_without=Email"`
	Tags     []string   `json:"tags" validate:"max=2"`
	Nick     *string    `json:"nick" validate:"nullable,alpha_dash,min=2"`
	Address  address    `json:"address"`
	Items    []item     `json:"items" validate:"min=1"`
	Ptr      *address   `json:"ptr"`
	Born     time.Time  `json:"born" validate:"required"`
	Skip     string     `json:"-" validate:"required"`
	Ignored  string     `validate:"-"`
	private  string     `validate:"required"`
	When     *time.Time `json:"when"`
}

func valid() signUp {
	nick := "ann_1"
	return signUp{
		Base: Base{Token: "t"}, Name: "Ann", Email: "ann@example.com", Password: "secret123", Confirm: "secret123",
		Role: "user", Site: "https://example.com", Terms: "on", Tags: []string{"a"}, Nick: &nick,
		Address: address{City: "Kyiv", Zip: "01001"}, Items: []item{{Name: "x"}}, Born: time.Now(),
	}
}

func TestValid(t *testing.T) {
	s := valid()
	if err := Struct(&s); err != nil {
		t.Fatal(err)
	}
	if err := Struct(s); err != nil {
		t.Fatal("value:", err)
	}
}

func TestInvalid(t *testing.T) {
	s := valid()
	bad := "!"
	s.Token, s.Name, s.Email, s.Password, s.Confirm = "", "Anastasia Long", "not-an-email", "short", "other"
	s.Age, s.Role, s.Site, s.Terms, s.Tags, s.Nick = 7, "root", "ftp://x", "no", []string{"a", "b", "c"}, &bad
	s.Address = address{Zip: "abc"}
	s.Items = []item{{Name: "toolong"}, {}}
	s.Ptr = &address{}
	s.Born = time.Time{}
	err := Struct(&s)
	errs, ok := err.(Errors)
	if !ok {
		t.Fatalf("%T %v", err, err)
	}
	want := map[string]string{
		"token":                 "The token field is required.",
		"name":                  "The name field must not be greater than 10 characters.",
		"email":                 "The email field must be a valid email address.",
		"password":              "The password field must be at least 8 characters.",
		"password_confirmation": "The password confirmation field must match password.",
		"age":                   "The age field must be between 13 and 120.",
		"role":                  "The selected role is invalid (one of user, admin).",
		"site":                  "The site field must be a valid URL.",
		"terms":                 "The terms field must be accepted.",
		"tags":                  "The tags field must not have more than 2 items.",
		"nick":                  "The nick field must only contain letters, numbers, dashes, and underscores.",
		"address.city":          "The city field is required.",
		"address.zip_code":      "The zip code field format is invalid.",
		"items.0.name":          "The name field must not be greater than 5 characters.",
		"items.1.name":          "The name field is required.",
		"ptr.city":              "The city field is required.",
		"born":                  "The born field is required.",
	}
	for k, m := range want {
		if errs.First(k) != m {
			t.Errorf("%s: got %q, want %q", k, errs.First(k), m)
		}
	}
	for k := range errs {
		if _, ok := want[k]; !ok {
			t.Errorf("unexpected error for %s: %v", k, errs[k])
		}
	}
	if !errs.Has("email") || errs.Has("phone") || errs.First("nope") != "" || !strings.Contains(errs.Error(), "token field is required") {
		t.Fatal(errs)
	}
	if len(errs["nick"]) != 2 { // alpha_dash and min both fail
		t.Fatal(errs["nick"])
	}
}

func TestRequiredAndEmpty(t *testing.T) {
	type form struct {
		Email string         `json:"email" validate:"required_without=Phone"`
		Phone string         `json:"phone" validate:"required_with=Code"`
		Code  string         `json:"code"`
		Count int            `json:"count" validate:"min=0"` // 0 is a value, not empty
		Blank string         `json:"blank" validate:"required"`
		Opt   *int           `json:"opt" validate:"min=5"` // nil: skipped
		Maybe *string        `json:"maybe" validate:"required"`
		M     map[string]int `json:"m" validate:"required"`
	}
	err := Struct(form{Code: "+380", Blank: "   "}).(Errors)
	for _, k := range []string{"email", "phone", "blank", "maybe", "m"} {
		if !err.Has(k) {
			t.Errorf("%s not reported: %v", k, err)
		}
	}
	if err.Has("count") || err.Has("opt") || err.First("phone") != "The phone field is required when code is present." {
		t.Fatal(err)
	}
	if e := Struct(form{Phone: "1", Blank: "x", Maybe: new(string), M: map[string]int{"a": 1}}).(Errors); e.Has("email") || !e.Has("maybe") {
		t.Fatal(e)
	}
}

func TestRules(t *testing.T) {
	cases := []struct {
		rule string
		v    any
		ok   bool
	}{
		{"email", "a@b.co", true}, {"email", "A <a@b.co>", false}, {"email", "a@localhost", false},
		{"uuid", "123e4567-e89b-12d3-a456-426614174000", true}, {"uuid", "123", false},
		{"alpha", "Привіт", true}, {"alpha", "a1", false},
		{"alpha_num", "a1", true}, {"alpha_num", "a-1", false},
		{"numeric", "1.5", true}, {"numeric", 3, true}, {"numeric", "x", false},
		{"integer", "-3", true}, {"integer", uint8(3), true}, {"integer", "1.5", false},
		{"min=3", "abc", true}, {"min=3", "ab", false}, {"min=3", 2.5, false}, {"min=x", 5, false}, {"min=1", true, false},
		{"max=2", []int{1, 2}, true}, {"max=2", map[string]int{"a": 1, "b": 2, "c": 3}, false}, {"max=10", uint(11), false},
		{"size=2", "ab", true}, {"size=2", "abc", false},
		{"gt=1", 2, true}, {"gt=1", 1, false}, {"lt=1", 0, true}, {"lt=1", 1, false},
		{"between=1 3", 2, true}, {"between=1", 2, false},
		{"date", "2026-01-02", true}, {"date", "2026-01-02T03:04:05Z", true}, {"date", time.Now(), true}, {"date", "tomorrow", false},
		{"regex=/^a+$/", "aaa", true}, {"regex=^a+$", "ab", false}, {"regex=(", "x", false},
		{"accepted", "yes", true}, {"accepted", "", false},
		{"url", "http://x.com/a", true}, {"url", "x.com", false},
	}
	for _, c := range cases {
		typ := reflect.StructOf([]reflect.StructField{{Name: "V", Type: reflect.TypeOf(c.v), Tag: reflect.StructTag(`json:"v" validate:"` + c.rule + `"`)}})
		s := reflect.New(typ).Elem()
		s.Field(0).Set(reflect.ValueOf(c.v))
		err := Struct(s.Interface())
		if (err == nil) != c.ok {
			t.Errorf("%s on %#v: %v", c.rule, c.v, err)
		}
	}
	type pair struct {
		A string `validate:"different=B"`
		B string
	}
	if Struct(pair{A: "x", B: "y"}) != nil || Struct(pair{A: "x", B: "x"}) == nil {
		t.Error("different")
	}
	type regexComma struct {
		V string `validate:"required,regex=^[a-z]{1,3}$"`
	}
	if Struct(regexComma{V: "ab"}) != nil || Struct(regexComma{V: "abcd"}) == nil {
		t.Error("regex with a comma")
	}
	type unknownSame struct {
		A string `validate:"same=Nope"`
	}
	if Struct(unknownSame{A: "x"}) == nil {
		t.Error("same with a missing field")
	}
}

func TestCustomRulesAndMessages(t *testing.T) {
	v := New()
	v.Register("even", func(f Field) bool { n, _ := size(f.Value); return int(n)%2 == 0 }, "The {field} must be even.")
	v.Register("present", func(f Field) bool { return f.Value.IsValid() }, "The {field} must be present.", true)
	v.SetMessage("required", "{field} is needed.")
	v.SetMessage("code.required", "Enter the code.")
	type form struct {
		N     int     `json:"n" validate:"even"`
		Name  string  `json:"name" validate:"required"`
		Code  string  `json:"code" validate:"required"`
		Thing *string `json:"thing" validate:"present"`
		Bad   string  `json:"bad" validate:"min=1"`
	}
	err := v.Struct(form{N: 3, Bad: "x"}).(Errors)
	if err.First("n") != "The n must be even." || err.First("name") != "name is needed." || err.First("code") != "Enter the code." || err.First("thing") != "The thing must be present." {
		t.Fatal(err)
	}
	type unknown struct {
		X string `validate:"nope"`
	}
	if e := v.Struct(unknown{X: "a"}); e == nil || !strings.Contains(e.Error(), `unknown rule "nope"`) {
		t.Fatal(e)
	}
	type nestedUnknown struct {
		In unknown `json:"in"`
	}
	if e := v.Struct(nestedUnknown{In: unknown{X: "a"}}); e == nil || errors.As(e, new(Errors)) {
		t.Fatal(e)
	}
	type sliceUnknown struct {
		In []unknown `json:"in"`
	}
	if e := v.Struct(sliceUnknown{In: []unknown{{X: "a"}}}); e == nil || errors.As(e, new(Errors)) {
		t.Fatal(e)
	}
	type embedUnknown struct{ unknownEmbed }
	if e := v.Struct(embedUnknown{}); e != nil {
		t.Fatal("unexported embedding is skipped:", e)
	}
	type EmbedUnknown struct{ unknown }
	_ = EmbedUnknown{}
	Register("global_rule", func(Field) bool { return false }, "global {field}")
	type g struct {
		X string `validate:"global_rule"`
	}
	if Struct(g{X: "a"}).(Errors).First("X") != "global x" {
		t.Fatal("package-level Register")
	}
	type noMsg struct {
		X string `validate:"nomsg"`
	}
	v.Register("nomsg", func(Field) bool { return false }, "")
	v.SetMessage("nomsg", "")
	delete(v.messages, "nomsg")
	if v.Struct(noMsg{X: "a"}).(Errors).First("X") != "The x field is invalid." {
		t.Fatal("fallback message")
	}
}

type unknownEmbed struct {
	X string `validate:"nope"`
}

func TestNotAStruct(t *testing.T) {
	var p *signUp
	if err := Struct(p); err == nil {
		t.Fatal("nil pointer")
	}
	if err := Struct(3); err == nil {
		t.Fatal("int")
	}
	if humanize("zipCode") != "zip code" || humanize("a.b_c") != "b c" {
		t.Fatal(humanize("zipCode"))
	}
	if (Field{}).String() != "" {
		t.Fatal("invalid field")
	}
}

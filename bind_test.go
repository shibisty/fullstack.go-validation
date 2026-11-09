package validation

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"fullstack/web"
)

type profile struct {
	Name    string    `json:"name" form:"name" validate:"required,max=20"`
	Age     int       `json:"age" form:"age" validate:"omitempty,min=13"`
	Score   float64   `json:"score" form:"score"`
	Count   uint      `json:"count" form:"count"`
	Admin   bool      `json:"admin" form:"admin"`
	Tags    []string  `json:"tags" form:"tags" validate:"max=3"`
	Nums    []int     `json:"nums" form:"nums"`
	Born    time.Time `json:"born" form:"born"`
	Nick    *string   `json:"nick" form:"nick" validate:"omitempty,min=2"`
	Address struct {
		City string `json:"city" form:"city" validate:"required"`
	} `json:"address" form:"address"`
	Skip string `form:"-"`
	Base
}

func serve(t *testing.T, h web.Handler, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	r := web.NewRouter()
	r.SetLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))
	r.Handle("", "/p", h)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestBindForm(t *testing.T) {
	var got profile
	h := func(c *web.Ctx) error {
		got = profile{}
		if err := Bind(c, &got); err != nil {
			return err
		}
		return c.Text(200, "ok")
	}
	form := url.Values{"name": {"Ann"}, "age": {"30"}, "score": {"1.5"}, "count": {"2"}, "admin": {"on"},
		"tags[]": {"a", "b"}, "nums": {"1", "2"}, "born": {"2000-01-02"}, "nick": {"an"}, "address.city": {"Kyiv"}, "token": {"t"}, "Skip": {"x"}}
	req := httptest.NewRequest("POST", "/p", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if w := serve(t, h, req); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if got.Name != "Ann" || got.Age != 30 || got.Score != 1.5 || got.Count != 2 || !got.Admin || len(got.Tags) != 2 || got.Nums[1] != 2 ||
		got.Born.Year() != 2000 || *got.Nick != "an" || got.Address.City != "Kyiv" || got.Token != "t" || got.Skip != "" {
		t.Fatalf("%+v", got)
	}

	// Type errors and validation errors together, as JSON for an API client.
	bad := url.Values{"name": {""}, "age": {"old"}, "score": {"x"}, "count": {"-1"}, "admin": {"maybe"}, "nums": {"1", "x"}, "born": {"soon"}}
	req = httptest.NewRequest("POST", "/p", strings.NewReader(bad.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	w := serve(t, h, req)
	body := w.Body.String()
	for _, want := range []string{`"error":"The given data was invalid."`, `"name":["The name field is required."]`, `"age":["The age field must be an integer."]`,
		`"score":["The score field must be a number."]`, `"count":["The count field must be a positive integer."]`, `"admin":["The admin field must be true or false."]`,
		`"nums":["The nums field must be an integer."]`, `"born":["The born field must be a valid date."]`, `"address.city":["The city field is required."]`, `"token"`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s in %s", want, body)
		}
	}
	if w.Code != 422 {
		t.Fatal(w.Code)
	}

	// Empty inputs are "not given".
	empty := url.Values{"name": {"A"}, "age": {""}, "score": {""}, "count": {""}, "admin": {""}, "born": {""}, "nick": {""}, "address.city": {"K"}, "token": {"t"}}
	req = httptest.NewRequest("POST", "/p", strings.NewReader(empty.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if w := serve(t, h, req); w.Code != 200 || got.Nick != nil || got.Age != 0 {
		t.Fatalf("%d %s %+v", w.Code, w.Body, got)
	}
}

func TestBindJSONQueryMultipart(t *testing.T) {
	var got profile
	h := func(c *web.Ctx) error {
		got = profile{}
		if err := Bind(c, &got); err != nil {
			return err
		}
		return c.Text(200, got.Name)
	}
	req := httptest.NewRequest("POST", "/p", strings.NewReader(`{"name":"Bob","address":{"city":"Lviv"},"token":"t","age":20}`))
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	if w := serve(t, h, req); w.Code != 200 || got.Age != 20 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	req = httptest.NewRequest("POST", "/p", strings.NewReader(`{"name":`))
	req.Header.Set("Content-Type", "application/json")
	if w := serve(t, h, req); w.Code != 400 {
		t.Fatal("malformed JSON", w.Code)
	}
	req = httptest.NewRequest("POST", "/p", strings.NewReader(`{"name":"","address":{"city":"x"},"token":"t"}`))
	req.Header.Set("Content-Type", "application/json")
	if w := serve(t, h, req); w.Code != 422 || !strings.Contains(w.Body.String(), "name field is required") {
		t.Fatal(w.Code, w.Body)
	}
	if w := serve(t, h, httptest.NewRequest("GET", "/p?name=Q&address.city=c&token=t", nil)); w.Body.String() != "Q" {
		t.Fatal("query", w.Code, w.Body)
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.WriteField("name", "Multi")
	mw.WriteField("address.city", "c")
	mw.WriteField("token", "t")
	fw, _ := mw.CreateFormFile("avatar", "a.png")
	fw.Write([]byte("png"))
	mw.Close()
	req = httptest.NewRequest("POST", "/p", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if w := serve(t, h, req); w.Body.String() != "Multi" {
		t.Fatal("multipart", w.Code, w.Body)
	}
	req = httptest.NewRequest("POST", "/p", strings.NewReader("garbage"))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	if w := serve(t, h, req); w.Code != 400 {
		t.Fatal("broken multipart", w.Code)
	}
	req = httptest.NewRequest("POST", "/p", strings.NewReader("%zz"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if w := serve(t, h, req); w.Code != 400 {
		t.Fatal("broken form", w.Code)
	}
	// A destination that is not a struct pointer.
	h2 := func(c *web.Ctx) error { var n int; return Bind(c, &n) }
	if w := serve(t, h2, httptest.NewRequest("GET", "/p", nil)); w.Code != 500 && w.Code != 422 {
		t.Fatal(w.Code)
	}
	// An unknown rule is a 500 (a programming error), not a 422.
	type broken struct {
		X string `form:"x" validate:"nope"`
	}
	h3 := func(c *web.Ctx) error { var b broken; return Bind(c, &b) }
	if w := serve(t, h3, httptest.NewRequest("GET", "/p?x=1", nil)); w.Code != 500 {
		t.Fatal(w.Code)
	}
}

func TestDecodeForm(t *testing.T) {
	var p struct {
		N    int            `form:"n"`
		M    map[string]int `form:"m"`
		Data []byte         `form:"data"`
	}
	err := DecodeForm(url.Values{"n": {"5"}, "m": {"x"}}, &p)
	if p.N != 5 || err == nil || !strings.Contains(err.Error(), "cannot be read") {
		t.Fatal(err, p)
	}
	if err := DecodeForm(url.Values{"n": {"1"}}, &p); err != nil {
		t.Fatal(err)
	}
	if err := DecodeForm(nil, p); err == nil {
		t.Fatal("non-pointer")
	}
	if Invalid(Errors{"a": {"b"}}).Status != 422 {
		t.Fatal()
	}
	if e, ok := ErrorsOf(Invalid(Errors{"a": {"b"}})); !ok || e.First("a") != "b" {
		t.Fatal(e, ok)
	}
	if _, ok := ErrorsOf(errors.New("x")); ok {
		t.Fatal("plain error")
	}
}

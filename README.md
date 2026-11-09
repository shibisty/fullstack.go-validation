# fullstack-validation

Struct validation by tags and request binding for [fullstack](https://github.com/shibisty/fullstack.go),
in the spirit of Laravel's validator. Standard library only.

## Installation

```json
{
    "dependencies": {
        "fullstack": "github:shibisty/fullstack.go",
        "fullstack-validation": "github:shibisty/fullstack.go-validation"
    }
}
```

```go
import "fullstack-validation" // package validation
```

## Usage

```go
type SignUp struct {
    Name     string `json:"name" validate:"required,max=100"`
    Email    string `json:"email" validate:"required,email"`
    Password string `json:"password" validate:"required,min=8"`
    Confirm  string `json:"password_confirmation" validate:"same=Password"`
    Age      int    `json:"age" validate:"omitempty,between=13 120"`
    Role     string `json:"role" validate:"oneof=user admin"`
    Terms    bool   `json:"terms" validate:"accepted"`
}

r.Post("/register", func(c *web.Ctx) error {
    var in SignUp
    if err := validation.Bind(c, &in); err != nil {
        return err // 422 with the errors, or 400 for malformed JSON
    }
    ...
})
```

`Bind` reads a JSON body (`Content-Type: application/json`), a form (urlencoded or multipart) or,
for GET, the query string, then validates. An API client gets:

```json
{
  "error": "The given data was invalid.",
  "errors": {
    "email": ["The email field must be a valid email address."],
    "password": ["The password field must be at least 8 characters."]
  }
}
```

Without a request, validate any struct:

```go
if err := validation.Struct(in); err != nil {
    errs, ok := err.(validation.Errors) // map[string][]string; ok is false for a programming error
    errs.First("email")
}
```

Checks done by hand (an email that is already taken) join the same response:

```go
errs := validation.Errors{}
errs.Add("email", "The email has already been taken.")
return validation.Invalid(errs)
```

## Rules

| Rule | Passes when |
|---|---|
| `required` | not empty: `""` and whitespace, nil, empty slices and maps, zero `time.Time` are empty |
| `required_with=Field`, `required_without=Field` | present when the other field is present / absent |
| `accepted` | `yes`, `on`, `1`, `true` (terms checkboxes) |
| `email`, `url` (http/https), `uuid`, `date` | the format |
| `alpha`, `alpha_num`, `alpha_dash` | letters (any script), digits, `-` and `_` |
| `numeric`, `integer` | a number (also in a string) |
| `min=N`, `max=N`, `size=N`, `between=A B`, `gt=N`, `lt=N` | characters of a string, value of a number, items of a slice or map |
| `oneof=a b c` | one of the values |
| `same=Field`, `different=Field` | equal to / different from another field (Go or json name) |
| `regex=^[0-9]{5}$` | the pattern (must be the last rule; may contain commas; `/.../` allowed) |

Rules other than `required*` and `accepted` skip empty values, as in Laravel, so `email` on an empty
optional field passes. `0` and `false` are values; add `omitempty` (or `nullable`) to treat them as
empty too. Nested structs and slices of structs are validated; their errors are named
`address.city`, `items.0.name`.

In an HTML form handler, show the errors again instead of the JSON response:

```go
if err := validation.Bind(c, &in); err != nil {
    if errs, ok := validation.ErrorsOf(err); ok {
        return c.HTML(422, "pages/register", map[string]any{"Errors": errs, "Old": in})
    }
    return err
}
```

(`{{index .Errors "email" 0}}`, or `.Errors.First "email"` in the template.)

## Forms

Form fields are found by the `form` tag, then the `json` tag, then the Go name. Supported: strings,
integers, floats, bools (a checkbox's `on`), `time.Time` (`2006-01-02`, `datetime-local`, RFC 3339),
pointers (an empty input is nil), slices (`tags` or `tags[]` repeated), nested structs as
`address.city`. A value that does not parse is an error of that field ("must be an integer").
Give a field the same name in `json` and `form` tags, so that errors use the name the form sends.

## Custom rules and messages

```go
validation.Register("phone", func(f validation.Field) bool {
    return phoneRe.MatchString(f.String())
}, "The {field} field must be a phone number.")

validation.Default.SetMessage("required", "Please fill in {field}.")
validation.Default.SetMessage("email.required", "We need your email.") // one field
```

`validation.New()` gives an independent validator with its own rules and messages.

## License

MIT
